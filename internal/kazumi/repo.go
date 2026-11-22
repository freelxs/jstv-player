package kazumi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
)

const (
	maxIndexBytes  = 4 << 20
	maxRuleBytes   = 1 << 20
	maxFetchWorker = 6
)

var errHTTPNotFound = errors.New("404 not found")

// LooksLikeRepoURL reports whether raw points at a GitHub repository, tree/blob
// path, or a Kazumi index.json URL so auto-detection can try Kazumi first.
func LooksLikeRepoURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	cleanPath := strings.Trim(u.Path, "/")
	lowerPath := strings.ToLower(cleanPath)
	if strings.HasSuffix(lowerPath, "/index.json") || lowerPath == "index.json" {
		return true
	}
	if host != "github.com" && host != "www.github.com" {
		return false
	}
	parts := strings.Split(cleanPath, "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return false
	}
	if len(parts) == 2 {
		return true
	}
	if parts[2] == "tree" && len(parts) >= 4 {
		return true
	}
	if parts[2] == "blob" && len(parts) >= 5 && strings.HasSuffix(strings.ToLower(parts[len(parts)-1]), ".json") {
		return true
	}
	return false
}

// NormalizeSourceURL converts GitHub repository/tree/blob URLs or direct HTTP(S)
// rule URLs into candidate raw JSON URLs to probe in order.
func NormalizeSourceURL(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, errors.New("Kazumi 规则地址必须是有效的 HTTP(S) 链接")
	}
	host := strings.ToLower(u.Hostname())
	if host == "github.com" || host == "www.github.com" {
		parts := splitPathSegments(u.Path)
		if len(parts) < 2 {
			return nil, errors.New("GitHub 仓库地址格式无效，应类似 https://github.com/Predidit/KazumiRules")
		}
		owner := parts[0]
		repo := strings.TrimSuffix(parts[1], ".git")
		if owner == "" || repo == "" {
			return nil, errors.New("GitHub 仓库地址缺少用户名或仓库名")
		}
		if len(parts) == 2 {
			return []string{
				fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/main/index.json", owner, repo),
				fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/master/index.json", owner, repo),
			}, nil
		}
		if parts[2] == "tree" && len(parts) >= 4 {
			branch := parts[3]
			sub := strings.Join(parts[4:], "/")
			if sub != "" {
				sub += "/"
			}
			return []string{
				fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%sindex.json", owner, repo, branch, sub),
			}, nil
		}
		if parts[2] == "blob" && len(parts) >= 5 {
			branch := parts[3]
			filePath := strings.Join(parts[4:], "/")
			return []string{
				fmt.Sprintf("https://raw.githubusercontent.com/%s/%s/%s/%s", owner, repo, branch, filePath),
			}, nil
		}
		return nil, fmt.Errorf("不支持的 GitHub 路径: %s", raw)
	}
	if strings.HasSuffix(u.Path, "/") && !strings.HasSuffix(strings.ToLower(u.Path), ".json") {
		clone := *u
		clone.Path = strings.TrimRight(clone.Path, "/") + "/index.json"
		return []string{clone.String()}, nil
	}
	return []string{raw}, nil
}

func splitPathSegments(p string) []string {
	trimmed := strings.Trim(p, "/")
	if trimmed == "" {
		return nil
	}
	return strings.Split(trimmed, "/")
}

// FetchBundle fetches a Kazumi rules repository (index.json + <name>.json files)
// or a single Kazumi rule JSON file. The boolean indicates whether the fetched
// payload was recognized as Kazumi rules.
func FetchBundle(ctx context.Context, client *http.Client, rawURL string) (*Bundle, bool, error) {
	if client == nil {
		client = http.DefaultClient
	}
	candidates, err := NormalizeSourceURL(rawURL)
	if err != nil {
		return nil, false, err
	}
	var body []byte
	var finalURL string
	var lastErr error
	for _, candidate := range candidates {
		b, final, ferr := fetchBytes(ctx, client, candidate, maxIndexBytes)
		if ferr == nil {
			body, finalURL = b, final
			lastErr = nil
			break
		}
		lastErr = ferr
		if !errors.Is(ferr, errHTTPNotFound) {
			break
		}
	}
	if lastErr != nil {
		return nil, false, fmt.Errorf("拉取 Kazumi 规则失败: %w", lastErr)
	}
	trimmed := bytes.TrimSpace(bytes.TrimPrefix(body, []byte("\xef\xbb\xbf")))
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	switch trimmed[0] {
	case '[':
		return fetchRepoFromIndex(ctx, client, rawURL, finalURL, trimmed)
	case '{':
		var rule Rule
		if err := json.Unmarshal(trimmed, &rule); err != nil {
			return nil, false, nil
		}
		if !rule.IsValid() {
			return nil, false, nil
		}
		return &Bundle{
			SourceURL: strings.TrimSpace(rawURL),
			IndexURL:  finalURL,
			BaseURL:   dirURL(finalURL),
			Rules:     []Rule{rule},
		}, true, nil
	default:
		return nil, false, nil
	}
}

func fetchRepoFromIndex(ctx context.Context, client *http.Client, sourceURL, indexURL string, indexBody []byte) (*Bundle, bool, error) {
	var items []IndexItem
	if err := json.Unmarshal(indexBody, &items); err != nil || len(items) == 0 {
		return nil, false, nil
	}
	if !looksLikeKazumiIndex(items) {
		return nil, false, nil
	}
	base := dirURL(indexURL)
	type fetchResult struct {
		rule    Rule
		ok      bool
		warning string
	}
	results := make([]fetchResult, len(items))
	sem := make(chan struct{}, maxFetchWorker)
	var wg sync.WaitGroup
	for i, item := range items {
		name := strings.TrimSpace(item.Name)
		if !safeRuleName(name) {
			results[i] = fetchResult{warning: fmt.Sprintf("跳过非法规则名: %q", item.Name)}
			continue
		}
		wg.Add(1)
		go func(idx int, ruleName string) {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			ruleURL := base + url.PathEscape(ruleName) + ".json"
			b, _, err := fetchBytes(ctx, client, ruleURL, maxRuleBytes)
			if err != nil {
				results[idx] = fetchResult{warning: fmt.Sprintf("规则 %s 拉取失败: %v", ruleName, err)}
				return
			}
			var rule Rule
			if err := json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), &rule); err != nil {
				results[idx] = fetchResult{warning: fmt.Sprintf("规则 %s JSON 解析失败", ruleName)}
				return
			}
			if strings.TrimSpace(rule.Name) == "" {
				rule.Name = ruleName
			}
			if !rule.IsValid() {
				results[idx] = fetchResult{warning: fmt.Sprintf("规则 %s 缺少搜索或剧集配置，已跳过", ruleName)}
				return
			}
			results[idx] = fetchResult{rule: rule, ok: true}
		}(i, name)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return nil, true, err
	}
	bundle := &Bundle{
		SourceURL: strings.TrimSpace(sourceURL),
		IndexURL:  indexURL,
		BaseURL:   base,
	}
	for _, res := range results {
		if res.ok {
			bundle.Rules = append(bundle.Rules, res.rule)
		} else if res.warning != "" {
			bundle.Warnings = append(bundle.Warnings, res.warning)
		}
	}
	if len(bundle.Rules) == 0 {
		return nil, true, errors.New("Kazumi 规则仓库中没有可用的规则")
	}
	return bundle, true, nil
}

func looksLikeKazumiIndex(items []IndexItem) bool {
	valid := 0
	for _, item := range items {
		if strings.TrimSpace(item.Name) == "" {
			continue
		}
		if item.UseNativePlayer != nil || item.LastUpdate > 0 || strings.TrimSpace(string(item.Version)) != "" {
			valid++
		}
	}
	return valid > 0
}

func safeRuleName(name string) bool {
	if name == "" || len(name) > 120 {
		return false
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\?#:`+"\x00") {
		return false
	}
	return true
}

func dirURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		if i := strings.LastIndex(raw, "/"); i >= 0 {
			return raw[:i+1]
		}
		return raw
	}
	u.RawQuery = ""
	u.Fragment = ""
	dir := path.Dir(u.Path)
	if dir == "." || dir == "/" {
		u.Path = "/"
	} else {
		u.Path = dir + "/"
	}
	return u.String()
}

func fetchBytes(ctx context.Context, client *http.Client, target string, maxBytes int64) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", defaultBrowserUA)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", errHTTPNotFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(body)) > maxBytes {
		return nil, "", errors.New("规则文件体积超过限制")
	}
	final := target
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return body, final, nil
}

// NormalizeEpisodeURL resolves relative hrefs against baseURL, unifies same-host
// HTTP(S) schemes to baseURL's scheme, and trims trailing path slashes.
// Mirrors Kazumi's lib/utils/episode_url.dart.
func NormalizeEpisodeURL(baseURL, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	rawURI, rawErr := url.Parse(trimmed)
	baseURI, baseErr := url.Parse(strings.TrimSpace(baseURL))
	hasValidBase := baseErr == nil && baseURI != nil && baseURI.Scheme != "" && baseURI.Host != ""

	var resolved *url.URL
	if rawErr == nil && rawURI != nil && rawURI.Scheme != "" && rawURI.Host != "" {
		clone := *rawURI
		resolved = &clone
	} else if hasValidBase && rawErr == nil && rawURI != nil {
		resolved = baseURI.ResolveReference(rawURI)
	}
	if resolved == nil || resolved.Host == "" {
		return trimmed
	}
	if hasValidBase &&
		isHTTPScheme(baseURI.Scheme) &&
		isHTTPScheme(resolved.Scheme) &&
		!strings.EqualFold(resolved.Scheme, baseURI.Scheme) &&
		strings.EqualFold(resolved.Hostname(), baseURI.Hostname()) &&
		resolved.Port() == baseURI.Port() {
		resolved.Scheme = baseURI.Scheme
	}
	p := resolved.Path
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = strings.TrimSuffix(p, "/")
	}
	resolved.Path = p
	resolved.RawPath = ""
	if resolved.RawQuery == "" {
		resolved.ForceQuery = false
	}
	return resolved.String()
}

func isHTTPScheme(s string) bool {
	return strings.EqualFold(s, "http") || strings.EqualFold(s, "https")
}

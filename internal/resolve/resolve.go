// Package resolve turns a play-page URL into a direct media address.
// It only uses the page markup and the subscription's parser list.
package resolve

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

const (
	maxDepth      = 4
	maxCandidates = 12
	maxBody       = 2 << 20
	parserBudget  = 7 * time.Second
	pageBudget    = 8 * time.Second
	maxParserErrs = 3
)

// Parser is one config entry that can turn a page URL into media.
type Parser struct {
	Name   string
	Type   int
	URL    string
	Flags  []string
	Header map[string]string
}

// Result is a direct media address and the headers needed to fetch it.
type Result struct {
	URL     string
	Headers map[string]string
}

// Options controls one resolution.
type Options struct {
	URL     string
	Headers map[string]string
	Flag    string
	Parsers []Parser
	Client  *http.Client
	// AllowLAN permits parser and page requests to private addresses.
	AllowLAN bool
	// Sniff loads a page in a hidden web view when static extraction fails.
	// Web mode leaves it nil.
	Sniff func(ctx context.Context, page string, headers map[string]string) (string, map[string]string, error)
}

// Resolve inspects a page, then tries matching type 0 and type 1 parsers.
// A media URL is returned as-is. An unchanged page URL means resolution failed.
func Resolve(ctx context.Context, opt Options) (Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	raw := strings.TrimSpace(opt.URL)
	if !isHTTP(raw) {
		return Result{}, errors.New("播放页不是 HTTP(S) 地址")
	}
	client := opt.Client
	if client == nil {
		jar, _ := cookiejar.New(nil)
		client = &http.Client{Jar: jar, Timeout: pageBudget}
	}
	seen := map[string]bool{}
	budget := maxCandidates
	if found, headers, ok := walk(ctx, client, raw, opt.Headers, opt.AllowLAN, 0, seen, &budget); ok {
		return Result{URL: found, Headers: headers}, nil
	}
	errs := 0
	for _, parser := range opt.Parsers {
		if errs >= maxParserErrs {
			break
		}
		if parser.Type != 0 && parser.Type != 1 {
			continue
		}
		if strings.TrimSpace(parser.URL) == "" || !flagMatches(parser.Flags, opt.Flag) {
			continue
		}
		pctx, cancel := context.WithTimeout(ctx, parserBudget)
		found, headers, ok, err := runParser(pctx, client, parser, raw, opt.Headers, opt.AllowLAN, seen, &budget, opt.Sniff)
		cancel()
		if err != nil {
			errs++
			continue
		}
		if ok {
			return Result{URL: found, Headers: headers}, nil
		}
	}
	return Result{}, errors.New("没有从播放页解析出媒体地址")
}

func walk(ctx context.Context, client *http.Client, raw string, headers map[string]string, allowLAN bool, depth int, seen map[string]bool, budget *int) (string, map[string]string, bool) {
	if depth > maxDepth || *budget <= 0 || !isHTTP(raw) || seen[raw] {
		return "", nil, false
	}
	if !allowLAN && isLAN(raw) {
		return "", nil, false
	}
	seen[raw] = true
	*budget--
	if mediaPath(raw) {
		return raw, withReferer(headers, ""), true
	}
	body, final, kind, respHeaders, err := fetch(ctx, client, raw, headers, allowLAN)
	if err != nil {
		return "", nil, false
	}
	if kind == "media" {
		return final, withReferer(mergeHeaders(headers, respHeaders), ""), true
	}
	base, _ := url.Parse(final)
	nextHeaders := withReferer(headers, final)
	candidates := pageCandidates(body, base)
	for _, candidate := range candidates {
		if mediaPath(candidate) {
			return candidate, withReferer(headers, final), true
		}
	}
	for _, candidate := range candidates {
		if mediaPath(candidate) || candidate == final {
			continue
		}
		if found, hdr, ok := walk(ctx, client, candidate, nextHeaders, allowLAN, depth+1, seen, budget); ok {
			return found, hdr, true
		}
	}
	return "", nil, false
}

func runParser(ctx context.Context, client *http.Client, parser Parser, play string, headers map[string]string, allowLAN bool, seen map[string]bool, budget *int, sniff func(context.Context, string, map[string]string) (string, map[string]string, error)) (string, map[string]string, bool, error) {
	target := parserTarget(parser.URL, play)
	if !isHTTP(target) {
		return "", nil, false, errors.New("解析接口地址无效")
	}
	merged := mergeHeaders(headers, parser.Header)
	if parser.Type == 1 {
		body, _, _, _, err := fetch(ctx, client, target, merged, allowLAN)
		if err != nil {
			return "", nil, false, err
		}
		media := jsonMedia(body)
		if media == "" || !isHTTP(media) {
			return "", nil, false, errors.New("解析接口没有返回地址")
		}
		if mediaPath(media) {
			return media, withReferer(merged, play), true, nil
		}
		found, hdr, ok := walk(ctx, client, media, withReferer(merged, play), allowLAN, 1, seen, budget)
		if !ok {
			return "", nil, false, errors.New("解析接口返回的页面里没有媒体")
		}
		return found, hdr, true, nil
	}
	found, hdr, ok := walk(ctx, client, target, merged, allowLAN, 1, seen, budget)
	if ok {
		return found, hdr, true, nil
	}
	if sniff != nil {
		if u, h, err := sniff(ctx, target, merged); err == nil && mediaPath(u) {
			return u, h, true, nil
		}
	}
	return "", nil, false, errors.New("网页解析没有得到媒体地址")
}

func parserTarget(template, play string) string {
	template = strings.TrimSpace(template)
	encoded := url.QueryEscape(play)
	if strings.Contains(template, "{url}") {
		return strings.ReplaceAll(template, "{url}", encoded)
	}
	if strings.HasSuffix(template, "=") {
		return template + encoded
	}
	if strings.Contains(template, "?") {
		if strings.Contains(strings.ToLower(template), "url=") {
			return template + encoded
		}
		return template + "&url=" + encoded
	}
	return template + "?url=" + encoded
}

func jsonMedia(body []byte) string {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return ""
	}
	return findURL(v, 0)
}

func findURL(v any, depth int) string {
	if depth > 4 {
		return ""
	}
	switch t := v.(type) {
	case string:
		if isHTTP(t) {
			return t
		}
	case map[string]any:
		for _, key := range []string{"url", "playUrl", "play_url"} {
			if s, ok := t[key].(string); ok && isHTTP(s) {
				return s
			}
		}
		for _, key := range []string{"data", "info"} {
			if nested, ok := t[key]; ok {
				if s := findURL(nested, depth+1); s != "" {
					return s
				}
			}
		}
	}
	return ""
}

func fetch(ctx context.Context, client *http.Client, raw string, headers map[string]string, allowLAN bool) ([]byte, string, string, map[string]string, error) {
	if !allowLAN && isLAN(raw) {
		return nil, "", "", nil, errors.New("播放页指向局域网")
	}
	cctx, cancel := context.WithTimeout(ctx, pageBudget)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", "", nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	for k, v := range headers {
		if strings.TrimSpace(k) != "" && !strings.ContainsAny(k+v, "\r\n") {
			req.Header.Set(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", "", nil, errors.New("播放页请求失败")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody+1))
	if err != nil {
		return nil, "", "", nil, err
	}
	if len(body) > maxBody {
		body = body[:maxBody]
	}
	final := raw
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	kind := "page"
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "mpegurl") || strings.Contains(ct, "video/") || strings.Contains(ct, "audio/") || playlistOrMedia(body) {
		kind = "media"
	}
	got := map[string]string{}
	if cookies := resp.Header.Values("Set-Cookie"); len(cookies) > 0 && client.Jar == nil {
		got["Cookie"] = strings.Join(cookies, "; ")
	}
	return body, final, kind, got, nil
}

func playlistOrMedia(body []byte) bool {
	b := bytes.TrimSpace(body)
	if bytes.HasPrefix(b, []byte("#EXTM3U")) {
		return true
	}
	if len(b) >= 12 && bytes.Contains(b[:12], []byte("ftyp")) {
		return true
	}
	return false
}

func pageCandidates(body []byte, base *url.URL) []string {
	var tags, players, literals, frames []string
	z := html.NewTokenizer(bytes.NewReader(body))
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			break
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			switch string(name) {
			case "video", "source", "audio":
				if src := tagAttr(z, "src"); src != "" {
					tags = append(tags, resolveRef(base, src))
				}
			case "iframe":
				if src := tagAttr(z, "src"); src != "" {
					resolved := resolveRef(base, src)
					if embedded := iframeQueryMedia(resolved); embedded != "" {
						tags = append(tags, embedded)
					}
					frames = append(frames, resolved)
				}
			}
		case html.TextToken:
			text := string(z.Text())
			players = append(players, macCMSURLs(text)...)
			literals = append(literals, mediaLiterals(text, base)...)
		}
	}
	literals = append(literals, mediaLiterals(string(body), base)...)
	all := append(append(append(tags, absolutize(base, players)...), literals...), frames...)
	filtered := make([]string, 0, len(all))
	for _, item := range all {
		if isLikelyAdMedia(item) {
			continue
		}
		filtered = append(filtered, item)
	}
	return unique(filtered)
}

func isLikelyAdMedia(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	for _, pat := range []string{"adposter", "preroll", "pre-roll", "googleads", "googlesyndication.com", "doubleclick.net", "adtrafficquality"} {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

func tagAttr(z *html.Tokenizer, key string) string {
	for {
		k, v, more := z.TagAttr()
		if strings.EqualFold(string(k), key) {
			return string(v)
		}
		if !more {
			return ""
		}
	}
}

var playerAssign = regexp.MustCompile(`player_[A-Za-z0-9_]*\s*=\s*`)
var mediaLiteral = regexp.MustCompile(`https?:\\?/\\?/[^\s"'<>]+?\.(?:m3u8|mp4|m4v|flv|mkv|webm|mpd)(?:\?[^\s"'<>]*)?`)

func macCMSURLs(text string) []string {
	var out []string
	rest := text
	for {
		loc := playerAssign.FindStringIndex(rest)
		if loc == nil {
			break
		}
		rest = rest[loc[1]:]
		obj, n := jsonObject(rest)
		if n == 0 {
			continue
		}
		rest = rest[n:]
		var m map[string]any
		if json.Unmarshal([]byte(obj), &m) != nil {
			continue
		}
		raw, _ := m["url"].(string)
		raw = decodePlayerURL(raw, intValue(m["encrypt"]))
		if raw != "" {
			out = append(out, raw)
		}
	}
	return out
}

func jsonObject(s string) (string, int) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	if i >= len(s) || s[i] != '{' {
		return "", 0
	}
	start := i
	depth, quoted, escaped := 0, false, false
	for ; i < len(s); i++ {
		c := s[i]
		if quoted {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		switch c {
		case '"':
			quoted = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return s[start : i+1], i + 1
			}
		}
	}
	return "", 0
}

func decodePlayerURL(raw string, encrypt int) string {
	raw = strings.TrimSpace(raw)
	switch encrypt {
	case 1:
		return unescape(raw)
	case 2:
		decoded, err := base64.StdEncoding.DecodeString(padBase64(raw))
		if err != nil {
			decoded, err = base64.URLEncoding.DecodeString(padBase64(raw))
		}
		if err != nil {
			return raw
		}
		return unescape(string(decoded))
	default:
		return raw
	}
}

func padBase64(s string) string {
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	return s
}

func unescape(s string) string {
	if out, err := url.PathUnescape(s); err == nil {
		return out
	}
	return s
}

func intValue(v any) int {
	switch t := v.(type) {
	case float64:
		return int(t)
	case string:
		if t == "1" || t == "2" {
			return int(t[0] - '0')
		}
	}
	return 0
}

func mediaLiterals(text string, base *url.URL) []string {
	text = strings.ReplaceAll(text, `\/`, "/")
	var out []string
	for _, match := range mediaLiteral.FindAllString(text, 20) {
		out = append(out, resolveRef(base, strings.ReplaceAll(match, `\`, "")))
	}
	return out
}

// iframeQueryMedia extracts a direct media URL embedded in an iframe's query
// parameters (e.g. ?url=https://.../index.m3u8), mirroring Kazumi's legacy parser.
func iframeQueryMedia(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.RawQuery == "" {
		return ""
	}
	for _, vals := range u.Query() {
		for _, v := range vals {
			v = strings.TrimSpace(v)
			if isHTTP(v) && mediaPath(v) {
				return v
			}
			if decoded, err := url.QueryUnescape(v); err == nil && decoded != v {
				decoded = strings.TrimSpace(decoded)
				if isHTTP(decoded) && mediaPath(decoded) {
					return decoded
				}
			}
		}
	}
	return ""
}

func absolutize(base *url.URL, refs []string) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		out = append(out, resolveRef(base, ref))
	}
	return out
}

func resolveRef(base *url.URL, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.String()
}

func unique(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, item := range in {
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
		if len(out) >= maxCandidates {
			break
		}
	}
	return out
}

func flagMatches(flags []string, flag string) bool {
	if len(flags) == 0 {
		return true
	}
	flag = strings.TrimSpace(flag)
	if flag == "" {
		return true
	}
	for _, item := range flags {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if item == flag || strings.Contains(flag, item) || strings.Contains(item, flag) {
			return true
		}
	}
	return false
}

func mediaPath(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	p := strings.ToLower(u.Path)
	for _, ext := range []string{".m3u8", ".mp4", ".m4v", ".flv", ".mkv", ".webm", ".mpd"} {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}

func isHTTP(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

func isLAN(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && !ip.IsLoopback() && (ip.IsPrivate() || ip.IsLinkLocalUnicast())
}

func mergeHeaders(base, extra map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func withReferer(headers map[string]string, page string) map[string]string {
	out := mergeHeaders(headers, nil)
	if page == "" {
		return out
	}
	if out == nil {
		out = map[string]string{}
	}
	if _, ok := out["Referer"]; !ok {
		out["Referer"] = page
	}
	return out
}

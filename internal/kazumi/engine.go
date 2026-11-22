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
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

const (
	defaultBrowserUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	maxResponseBytes = 4 << 20
)

// ErrCaptchaRequired indicates the target site returned an anti-crawler challenge.
var ErrCaptchaRequired = errors.New("站点触发人机验证（验证码），暂不支持自动通过，请切换其他规则")

var whitespaceRe = regexp.MustCompile(`\s+`)

// Engine executes Kazumi XPath and API rules over a provided HTTP client.
type Engine struct {
	Client *http.Client
	Log    func(string)
}

func (e *Engine) httpClient() *http.Client {
	if e != nil && e.Client != nil {
		return e.Client
	}
	return http.DefaultClient
}

// Search queries one Kazumi rule for keyword.
func (e *Engine) Search(ctx context.Context, rule Rule, keyword string) ([]SearchItem, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return nil, nil
	}
	if rule.NormalizedSearchMode() == "api" {
		return e.searchAPI(ctx, rule, keyword)
	}
	return e.searchXPath(ctx, rule, keyword)
}

// Chapters fetches the playback lines and episodes for one search item source.
func (e *Engine) Chapters(ctx context.Context, rule Rule, source string) ([]Road, PageMeta, error) {
	source = strings.TrimSpace(source)
	if source == "" {
		return nil, PageMeta{}, errors.New("缺少条目来源地址")
	}
	if rule.NormalizedChapterMode() == "api" {
		return e.chaptersAPI(ctx, rule, source)
	}
	return e.chaptersXPath(ctx, rule, source)
}

func (e *Engine) searchXPath(ctx context.Context, rule Rule, keyword string) ([]SearchItem, error) {
	queryURL := strings.ReplaceAll(strings.TrimSpace(rule.SearchURL), "@keyword", url.QueryEscape(keyword))
	u, err := url.Parse(queryURL)
	if err != nil || !isHTTPScheme(u.Scheme) || u.Host == "" {
		return nil, fmt.Errorf("搜索 URL 无效: %s", queryURL)
	}
	var raw string
	var status int
	var respHeader http.Header
	if rule.UsePost {
		formValues := u.Query()
		target := *u
		target.RawQuery = ""
		target.ForceQuery = false
		raw, status, respHeader, err = e.doRequest(ctx, rule, http.MethodPost, target.String(), nil, nil, "form", formValues)
	} else {
		raw, status, respHeader, err = e.doRequest(ctx, rule, http.MethodGet, queryURL, nil, nil, "none", nil)
	}
	if err != nil {
		if status == http.StatusForbidden || status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable {
			if isCaptchaChallenge(raw, respHeader, rule.AntiCrawlerConfig, nil) {
				return nil, ErrCaptchaRequired
			}
		}
		return nil, err
	}
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("搜索页 HTML 解析失败: %w", err)
	}
	if isCaptchaChallenge(raw, respHeader, rule.AntiCrawlerConfig, doc) {
		return nil, ErrCaptchaRequired
	}
	nodes, err := queryXPathAll(doc, rule.SearchList, false)
	if err != nil {
		return nil, err
	}
	items := make([]SearchItem, 0, len(nodes))
	for _, node := range nodes {
		nameNode, err := queryXPathOne(node, rule.SearchName, true)
		if err != nil {
			return nil, err
		}
		srcNode, err := queryXPathOne(node, rule.SearchResult, true)
		if err != nil {
			return nil, err
		}
		name := nodeText(nameNode)
		src := nodeHref(srcNode)
		if name == "" || src == "" {
			continue
		}
		items = append(items, SearchItem{
			Name: name,
			Src:  src,
			Pic:  extractSearchPic(node, rule.BaseURL),
		})
	}
	return items, nil
}

func (e *Engine) chaptersXPath(ctx context.Context, rule Rule, source string) ([]Road, PageMeta, error) {
	target := NormalizeEpisodeURL(rule.BaseURL, source)
	u, err := url.Parse(target)
	if err != nil || !isHTTPScheme(u.Scheme) || u.Host == "" {
		return nil, PageMeta{}, fmt.Errorf("章节 URL 无效: %s", target)
	}
	raw, _, _, err := e.doRequest(ctx, rule, http.MethodGet, target, nil, nil, "none", nil)
	if err != nil {
		return nil, PageMeta{}, err
	}
	doc, err := html.Parse(strings.NewReader(raw))
	if err != nil {
		return nil, PageMeta{}, fmt.Errorf("详情页 HTML 解析失败: %w", err)
	}
	roadNodes, err := queryXPathAll(doc, rule.ChapterRoads, false)
	if err != nil {
		return nil, PageMeta{}, err
	}
	roads := make([]Road, 0, len(roadNodes))
	for _, roadNode := range roadNodes {
		epNodes, err := queryXPathAll(roadNode, rule.ChapterResult, true)
		if err != nil {
			return nil, PageMeta{}, err
		}
		var eps []Episode
		for epIdx, epNode := range epNodes {
			href := nodeHref(epNode)
			if href == "" {
				continue
			}
			epURL := NormalizeEpisodeURL(rule.BaseURL, href)
			if epURL == "" {
				continue
			}
			name := whitespaceRe.ReplaceAllString(nodeText(epNode), "")
			if name == "" {
				name = fmt.Sprintf("第%d集", epIdx+1)
			}
			eps = append(eps, Episode{Name: name, URL: epURL})
		}
		if len(eps) == 0 {
			continue
		}
		roads = append(roads, Road{
			Name:     fmt.Sprintf("播放线路%d", len(roads)+1),
			Episodes: eps,
		})
	}
	return roads, extractPageMeta(doc, rule.BaseURL), nil
}

func (e *Engine) searchAPI(ctx context.Context, rule Rule, keyword string) ([]SearchItem, error) {
	cfg := rule.SearchAPIConfig
	if cfg == nil {
		return nil, errors.New("规则缺少 searchApiConfig")
	}
	vars := map[string]any{"keyword": keyword}
	raw, err := e.executeAPIRequest(ctx, rule, cfg.Request, vars)
	if err != nil {
		return nil, err
	}
	var doc any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("API 响应不是有效 JSON: %w", err)
	}
	nodes, err := ReadJSONPath(doc, cfg.ListPath)
	if err != nil {
		return nil, err
	}
	items := make([]SearchItem, 0, len(nodes))
	for _, node := range nodes {
		nameVal, err := ReadJSONPathFirst(node, cfg.NamePath)
		if err != nil {
			return nil, err
		}
		srcVal, err := ReadJSONPathFirst(node, cfg.SourcePath)
		if err != nil {
			return nil, err
		}
		name := toStringValue(nameVal)
		source := toStringValue(srcVal)
		if name == "" || source == "" {
			continue
		}
		src := source
		if cfg.SourceTemplate != "" {
			src = strings.ReplaceAll(cfg.SourceTemplate, "@source", uriEncodeComponent(source))
		}
		jm := extractJSONMeta(node, rule.BaseURL)
		items = append(items, SearchItem{
			Name:     name,
			Src:      src,
			Pic:      jm.Pic,
			Content:  jm.Content,
			Year:     jm.Year,
			Area:     jm.Area,
			TypeName: jm.TypeName,
			Actor:    jm.Actor,
			Director: jm.Director,
			Remarks:  jm.Remarks,
		})
	}
	return items, nil
}

func (e *Engine) chaptersAPI(ctx context.Context, rule Rule, source string) ([]Road, PageMeta, error) {
	cfg := rule.ChapterAPIConfig
	if cfg == nil {
		return nil, PageMeta{}, errors.New("规则缺少 chapterApiConfig")
	}
	vars := map[string]any{"source": source}
	raw, err := e.executeAPIRequest(ctx, rule, cfg.Request, vars)
	if err != nil {
		return nil, PageMeta{}, err
	}
	var doc any
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, PageMeta{}, fmt.Errorf("章节 API 响应不是有效 JSON: %w", err)
	}
	rootVars := map[string]any{"source": source}
	for k, jp := range cfg.Variables {
		val, err := ReadJSONPathFirst(doc, jp)
		if err != nil {
			return nil, PageMeta{}, err
		}
		if val == nil {
			return nil, PageMeta{}, fmt.Errorf("章节响应变量 %s 未匹配到值: %s", k, jp)
		}
		rootVars[k] = val
	}
	meta := extractJSONDocumentMeta(doc, rule.BaseURL)
	var roads []Road
	if strings.EqualFold(strings.TrimSpace(cfg.Format), "delimited") {
		roads, err = parseDelimitedChapters(doc, cfg, rootVars, rule.BaseURL)
	} else {
		roads, err = parseNestedChapters(doc, cfg, rootVars, rule.BaseURL)
	}
	return roads, meta, err
}

func parseDelimitedChapters(doc any, cfg *APIChapterConfig, rootVars map[string]any, baseURL string) ([]Road, error) {
	if cfg.RoadSeparator == "" || cfg.EpisodeSeparator == "" || cfg.FieldSeparator == "" {
		return nil, errors.New("章节分隔符不能为空")
	}
	namesVal, err := ReadJSONPathFirst(doc, cfg.RoadNamesPath)
	if err != nil {
		return nil, err
	}
	epsVal, err := ReadJSONPathFirst(doc, cfg.RoadEpisodesPath)
	if err != nil {
		return nil, err
	}
	epsStr := toStringValue(epsVal)
	if epsStr == "" {
		return nil, nil
	}
	roadNames := strings.Split(toStringValue(namesVal), cfg.RoadSeparator)
	roadGroups := strings.Split(epsStr, cfg.RoadSeparator)
	roads := make([]Road, 0, len(roadGroups))
	for roadIdx, group := range roadGroups {
		entries := strings.Split(group, cfg.EpisodeSeparator)
		var eps []Episode
		for epIdx, entry := range entries {
			entry = strings.TrimSpace(entry)
			if entry == "" {
				continue
			}
			sepIdx := strings.Index(entry, cfg.FieldSeparator)
			if sepIdx < 0 {
				continue
			}
			epName := strings.TrimSpace(entry[:sepIdx])
			rawURL := strings.TrimSpace(entry[sepIdx+len(cfg.FieldSeparator):])
			pageURL, err := resolveAPIEpisodeURL(cfg, rootVars, rawURL, roadIdx, epIdx, baseURL)
			if err != nil {
				return nil, err
			}
			if pageURL == "" {
				continue
			}
			if epName == "" {
				epName = fmt.Sprintf("第%d集", epIdx+1)
			}
			eps = append(eps, Episode{Name: epName, URL: pageURL})
		}
		if len(eps) == 0 {
			continue
		}
		roadName := ""
		if roadIdx < len(roadNames) {
			roadName = strings.TrimSpace(roadNames[roadIdx])
		}
		if roadName == "" {
			roadName = fmt.Sprintf("播放线路%d", len(roads)+1)
		}
		roads = append(roads, Road{Name: roadName, Episodes: eps})
	}
	return roads, nil
}

func parseNestedChapters(doc any, cfg *APIChapterConfig, rootVars map[string]any, baseURL string) ([]Road, error) {
	hasRoads := strings.TrimSpace(cfg.RoadsPath) != ""
	var roadNodes []any
	var err error
	if hasRoads {
		roadNodes, err = ReadJSONPath(doc, cfg.RoadsPath)
		if err != nil {
			return nil, err
		}
	} else {
		roadNodes = []any{doc}
	}
	roads := make([]Road, 0, len(roadNodes))
	for roadIdx, roadNode := range roadNodes {
		roadName := ""
		if hasRoads && strings.TrimSpace(cfg.RoadNamePath) != "" {
			val, err := ReadJSONPathFirst(roadNode, cfg.RoadNamePath)
			if err != nil {
				return nil, err
			}
			roadName = toStringValue(val)
		}
		epNodes, err := ReadJSONPath(roadNode, cfg.EpisodesPath)
		if err != nil {
			return nil, err
		}
		var eps []Episode
		for epIdx, epNode := range epNodes {
			nameVal, err := ReadJSONPathFirst(epNode, cfg.EpisodeNamePath)
			if err != nil {
				return nil, err
			}
			epName := toStringValue(nameVal)
			rawURL := ""
			if strings.TrimSpace(cfg.EpisodeUrlPath) != "" {
				urlVal, err := ReadJSONPathFirst(epNode, cfg.EpisodeUrlPath)
				if err != nil {
					return nil, err
				}
				rawURL = toStringValue(urlVal)
			}
			pageURL, err := resolveAPIEpisodeURL(cfg, rootVars, rawURL, roadIdx, epIdx, baseURL)
			if err != nil {
				return nil, err
			}
			if pageURL == "" {
				continue
			}
			if epName == "" {
				epName = fmt.Sprintf("第%d集", epIdx+1)
			}
			eps = append(eps, Episode{Name: epName, URL: pageURL})
		}
		if len(eps) == 0 {
			continue
		}
		if roadName == "" {
			roadName = fmt.Sprintf("播放线路%d", len(roads)+1)
		}
		roads = append(roads, Road{Name: roadName, Episodes: eps})
	}
	return roads, nil
}

func resolveAPIEpisodeURL(cfg *APIChapterConfig, rootVars map[string]any, rawURL string, roadIdx, epIdx int, baseURL string) (string, error) {
	if cfg.EpisodePage == nil {
		return NormalizeEpisodeURL(baseURL, rawURL), nil
	}
	if strings.TrimSpace(cfg.EpisodePage.URL) == "" {
		return "", errors.New("播放页地址模板不能为空")
	}
	vars := make(map[string]any, len(rootVars)+5)
	for k, v := range rootVars {
		vars[k] = v
	}
	vars["episodeUrl"] = rawURL
	vars["roadIndex"] = roadIdx
	vars["roadNumber"] = roadIdx + 1
	vars["episodeIndex"] = epIdx
	vars["episodeNumber"] = epIdx + 1

	renderedURL, err := renderTemplate(cfg.EpisodePage.URL, vars, true)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(renderedURL)
	if err != nil {
		return "", fmt.Errorf("剧集页面 URL 无效: %s", renderedURL)
	}
	if len(cfg.EpisodePage.Query) > 0 {
		renderedQuery, err := renderMap(cfg.EpisodePage.Query, vars)
		if err != nil {
			return "", err
		}
		q := u.Query()
		for k, v := range renderedQuery {
			q.Set(k, toStringValue(v))
		}
		u.RawQuery = q.Encode()
	}
	return NormalizeEpisodeURL(baseURL, u.String()), nil
}

func (e *Engine) executeAPIRequest(ctx context.Context, rule Rule, reqCfg APIRequestConfig, vars map[string]any) (string, error) {
	method := strings.ToUpper(strings.TrimSpace(reqCfg.Method))
	if method == "" {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodPost {
		return "", fmt.Errorf("API 仅支持 GET/POST，当前为 %s", method)
	}
	if strings.TrimSpace(reqCfg.URL) == "" {
		return "", errors.New("API 请求 URL 不能为空")
	}
	targetURL, err := renderTemplate(strings.TrimSpace(reqCfg.URL), vars, true)
	if err != nil {
		return "", err
	}
	u, err := url.Parse(targetURL)
	if err != nil || !isHTTPScheme(u.Scheme) || u.Host == "" {
		return "", fmt.Errorf("API 请求 URL 无效: %s", targetURL)
	}
	headers, err := renderMap(reqCfg.Headers, vars)
	if err != nil {
		return "", err
	}
	query, err := renderMap(reqCfg.Query, vars)
	if err != nil {
		return "", err
	}
	bodyType := strings.ToLower(strings.TrimSpace(reqCfg.BodyType))
	if bodyType == "" {
		bodyType = "none"
	}
	var body any
	if method == http.MethodPost && bodyType != "none" && reqCfg.Body != nil {
		body, err = renderValue(reqCfg.Body, vars)
		if err != nil {
			return "", err
		}
	}
	raw, _, _, err := e.doRequest(ctx, rule, method, targetURL, headers, query, bodyType, body)
	return raw, err
}

func (e *Engine) doRequest(ctx context.Context, rule Rule, method, targetURL string, headers, query map[string]any, bodyType string, body any) (string, int, http.Header, error) {
	u, err := url.Parse(targetURL)
	if err != nil {
		return "", 0, nil, err
	}
	if len(query) > 0 {
		q := u.Query()
		for k, v := range query {
			q.Set(k, toStringValue(v))
		}
		u.RawQuery = q.Encode()
	}
	var reqBody io.Reader
	contentType := ""
	if method == http.MethodPost {
		switch bodyType {
		case "json":
			b, err := json.Marshal(body)
			if err != nil {
				return "", 0, nil, err
			}
			reqBody = bytes.NewReader(b)
			contentType = "application/json"
		case "form":
			vals := url.Values{}
			switch typed := body.(type) {
			case url.Values:
				vals = typed
			case map[string]any:
				for k, v := range typed {
					vals.Set(k, toStringValue(v))
				}
			}
			reqBody = strings.NewReader(vals.Encode())
			contentType = "application/x-www-form-urlencoded"
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reqBody)
	if err != nil {
		return "", 0, nil, err
	}
	ua := strings.TrimSpace(rule.UserAgent)
	if ua == "" {
		ua = defaultBrowserUA
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	ref := strings.TrimSpace(rule.Referer)
	if ref == "" && strings.TrimSpace(rule.BaseURL) != "" {
		ref = strings.TrimRight(strings.TrimSpace(rule.BaseURL), "/") + "/"
	}
	if ref != "" && !strings.ContainsAny(ref, "\r\n") {
		req.Header.Set("Referer", ref)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for k, v := range headers {
		ks := strings.TrimSpace(k)
		vs := toStringValue(v)
		if ks != "" && !strings.ContainsAny(ks+vs, "\r\n") {
			req.Header.Set(ks, vs)
		}
	}
	resp, err := e.httpClient().Do(req)
	if err != nil {
		return "", 0, nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", resp.StatusCode, resp.Header, err
	}
	raw := string(b)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return raw, resp.StatusCode, resp.Header, fmt.Errorf("请求失败 (HTTP %d)", resp.StatusCode)
	}
	return raw, resp.StatusCode, resp.Header, nil
}

func isCaptchaChallenge(raw string, header http.Header, cfg *AntiCrawlerConfig, doc *html.Node) bool {
	if cfg == nil || !cfg.Enabled {
		return false
	}
	if header != nil && strings.EqualFold(strings.TrimSpace(header.Get("cf-mitigated")), "challenge") {
		return true
	}
	detectVal := strings.TrimSpace(cfg.CaptchaDetectValue)
	if detectVal != "" {
		switch cfg.CaptchaDetectType {
		case 2: // text
			return strings.Contains(raw, detectVal)
		case 3: // regex
			re, err := regexp.Compile("(?is)" + detectVal)
			return err == nil && re.MatchString(raw)
		default: // 1: xpath
			if doc == nil {
				doc, _ = html.Parse(strings.NewReader(raw))
			}
			node, err := queryXPathOne(doc, detectVal, false)
			return err == nil && node != nil
		}
	}
	if doc == nil && strings.TrimSpace(raw) != "" {
		doc, _ = html.Parse(strings.NewReader(raw))
	}
	for _, expr := range []string{cfg.CaptchaImage, cfg.CaptchaButton} {
		if strings.TrimSpace(expr) == "" {
			continue
		}
		if node, err := queryXPathOne(doc, expr, false); err == nil && node != nil {
			return true
		}
	}
	return false
}

func renderMap(in map[string]any, vars map[string]any) (map[string]any, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		rk, err := renderTemplate(k, vars, false)
		if err != nil {
			return nil, err
		}
		rv, err := renderValue(v, vars)
		if err != nil {
			return nil, err
		}
		out[rk] = rv
	}
	return out, nil
}

func renderValue(val any, vars map[string]any) (any, error) {
	switch typed := val.(type) {
	case string:
		if len(typed) > 1 && typed[0] == '@' && isValidVarName(typed[1:]) {
			v, ok := vars[typed[1:]]
			if !ok {
				return nil, fmt.Errorf("缺少模板变量 %s", typed)
			}
			return v, nil
		}
		return renderTemplate(typed, vars, false)
	case []any:
		out := make([]any, len(typed))
		for i, item := range typed {
			rv, err := renderValue(item, vars)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	case map[string]any:
		return renderMap(typed, vars)
	default:
		return val, nil
	}
}

func renderTemplate(tpl string, vars map[string]any, encode bool) (string, error) {
	if !strings.Contains(tpl, "@") {
		return tpl, nil
	}
	var b strings.Builder
	b.Grow(len(tpl))
	for i := 0; i < len(tpl); {
		if tpl[i] == '@' && (i == 0 || !isVarBodyChar(tpl[i-1])) && i+1 < len(tpl) && isVarStartChar(tpl[i+1]) {
			end := i + 2
			for end < len(tpl) && isVarBodyChar(tpl[end]) {
				end++
			}
			name := tpl[i+1 : end]
			val, ok := vars[name]
			if !ok {
				return "", fmt.Errorf("缺少模板变量 @%s", name)
			}
			str := toStringValue(val)
			if encode {
				str = uriEncodeComponent(str)
			}
			b.WriteString(str)
			i = end
			continue
		}
		b.WriteByte(tpl[i])
		i++
	}
	return b.String(), nil
}

func isValidVarName(s string) bool {
	if s == "" || !isVarStartChar(s[0]) {
		return false
	}
	for i := 1; i < len(s); i++ {
		if !isVarBodyChar(s[i]) {
			return false
		}
	}
	return true
}

func isVarStartChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '_'
}

func isVarBodyChar(c byte) bool {
	return isVarStartChar(c) || (c >= '0' && c <= '9')
}

// uriEncodeComponent matches Dart's Uri.encodeComponent / JS encodeURIComponent.
func uriEncodeComponent(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '!' || c == '~' || c == '*' || c == '\'' || c == '(' || c == ')' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func toStringValue(v any) string {
	if v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case float64:
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case bool:
		return strconv.FormatBool(t)
	case json.Number:
		return t.String()
	default:
		return strings.TrimSpace(fmt.Sprint(v))
	}
}

func extractJSONPic(node any, baseURL string) string {
	m, ok := node.(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"vod_pic", "pic", "picUrl", "pic_url", "cover", "coverUrl", "cover_url", "poster", "posterUrl", "poster_url", "image", "img", "imgUrl", "img_url", "thumb"} {
		raw := toStringValue(m[key])
		if raw == "" {
			continue
		}
		resolved := cleanPosterURL(NormalizeEpisodeURL(baseURL, raw))
		if strings.HasPrefix(resolved, "http://") || strings.HasPrefix(resolved, "https://") {
			return resolved
		}
	}
	return ""
}

func extractJSONDocumentMeta(doc any, baseURL string) PageMeta {
	if m, ok := doc.(map[string]any); ok {
		for _, wrap := range []string{"data", "vod", "detail", "info", "result", "subject"} {
			if sub, ok := m[wrap]; ok {
				if subMeta := extractJSONDocumentMeta(sub, baseURL); subMeta.Content != "" || subMeta.Title != "" || subMeta.Pic != "" {
					return subMeta
				}
			}
		}
		if list, ok := m["list"].([]any); ok && len(list) == 1 {
			if subMeta := extractJSONMeta(list[0], baseURL); subMeta.Content != "" || subMeta.Title != "" || subMeta.Pic != "" {
				return subMeta
			}
		}
	}
	return extractJSONMeta(doc, baseURL)
}

func extractJSONMeta(node any, baseURL string) PageMeta {
	m, ok := node.(map[string]any)
	if !ok {
		return PageMeta{}
	}
	firstField := func(keys ...string) string {
		for _, k := range keys {
			if v := toStringValue(m[k]); v != "" {
				return v
			}
		}
		return ""
	}
	content := cleanSynopsisText(firstField("vod_content", "vod_blurb", "description", "desc", "intro", "introduction", "summary", "content", "plot", "synopsis", "story"))
	year := firstField("vod_year", "year", "pubdate", "air_date", "release_year")
	if year != "" {
		if ym := yearPattern.FindString(year); ym != "" {
			year = ym
		}
	}
	return PageMeta{
		Title:    firstField("vod_name", "name_cn", "name", "title", "cnname"),
		Pic:      extractJSONPic(node, baseURL),
		Content:  content,
		Year:     year,
		Area:     firstField("vod_area", "area", "region", "country"),
		TypeName: firstField("type_name", "vod_class", "genre", "category"),
		Actor:    firstField("vod_actor", "actor", "actors", "cv", "cast", "voice_actor"),
		Director: firstField("vod_director", "director", "staff", "author"),
		Remarks:  firstField("vod_remarks", "remarks", "note", "status"),
	}
}

package tvbox

import (
	"encoding/json"
	"net/url"
	"strings"
)

// Rule is one simple playlist edit. Hosts match the media hostname by
// substring. Each Regex is removed from an HLS playlist body.
type Rule struct {
	Name  string   `json:"name"`
	Hosts []string `json:"hosts"`
	Regex []string `json:"regex"`
}

// LiveSource is one live playlist entry from the configuration.
type LiveSource struct {
	Name    string
	URL     string
	Headers map[string]string
}

// Source is one site after addresses are resolved and the runtime is chosen.
type Source struct {
	Key         string
	Name        string
	Type        int
	API         string
	Ext         json.RawMessage
	Kind        string // cms-xml, cms-json, drpy, live, python, jar, other
	Unsupported bool
	Reason      string
	Searchable  *int
	QuickSearch *int
	Filterable  *int
	Header      map[string]string
	PlayURL     string
	Jar         string
	LocalAPI    string
}

// Config is a parsed TVBox subscription.
type Config struct {
	Origin    string
	SpiderURL string
	Sites     []Source
	Lives     []LiveSource
	Hosts     map[string]string
	Rules     []Rule
	Wallpaper string
	Logo      string
}

type siteJSON struct {
	Key         string          `json:"key"`
	Name        string          `json:"name"`
	Type        *int            `json:"type"`
	API         string          `json:"api"`
	Ext         json.RawMessage `json:"ext"`
	Searchable  *int            `json:"searchable"`
	QuickSearch *int            `json:"quickSearch"`
	Filterable  *int            `json:"filterable"`
	Jar         string          `json:"jar"`
	Header      json.RawMessage `json:"header"`
	PlayURL     string          `json:"playUrl"`
	UA          string          `json:"ua"`
}

type liveJSON struct {
	Name    string          `json:"name"`
	URL     string          `json:"url"`
	UA      string          `json:"ua"`
	Header  json.RawMessage `json:"header"`
	Referer string          `json:"referer"`
}

// Parse decodes a subscription and resolves relative addresses against origin,
// which should be the final URL after redirects.
func Parse(data []byte, origin string) (*Config, error) {
	origin = strings.TrimSpace(origin)
	if _, err := url.Parse(origin); err != nil || origin == "" {
		return nil, errBadJSON
	}
	raw, err := Decode(data)
	if err != nil {
		if cfg, ok := playlistConfig(data, origin); ok {
			return cfg, nil
		}
		return nil, err
	}
	obj, err := jsonObject(raw)
	if err != nil {
		if cfg, ok := playlistConfig(data, origin); ok {
			return cfg, nil
		}
		return nil, err
	}
	if _, sitesOK := obj["sites"]; !sitesOK {
		if _, livesOK := obj["lives"]; !livesOK {
			if _, urls := obj["urls"]; urls {
				return nil, errDirectory
			}
			if _, store := obj["storeHouse"]; store {
				return nil, errDirectory
			}
		}
	}
	cfg := &Config{Origin: origin, Hosts: parseHosts(obj["hosts"]), Rules: parseRules(obj["rules"])}
	cfg.SpiderURL = parseSpider(origin, obj["spider"])
	cfg.Wallpaper = resolveOptional(origin, rawString(obj["wallpaper"]))
	cfg.Logo = resolveOptional(origin, rawString(obj["logo"]))
	seen := map[string]bool{}
	var sites []siteJSON
	if err := json.Unmarshal(obj["sites"], &sites); err != nil && len(obj["sites"]) > 0 {
		return nil, errBadJSON
	}
	for _, s := range sites {
		if strings.TrimSpace(s.Key) == "" || strings.TrimSpace(s.API) == "" || s.Type == nil || seen[s.Key] {
			continue
		}
		seen[s.Key] = true
		src := Source{
			Key: s.Key, Name: firstNonEmpty(s.Name, s.Key), Type: *s.Type,
			Searchable: s.Searchable, QuickSearch: s.QuickSearch, Filterable: s.Filterable,
			Header: parseHeader(s.Header), PlayURL: strings.TrimSpace(s.PlayURL),
		}
		if s.UA != "" {
			if src.Header == nil {
				src.Header = map[string]string{}
			}
			src.Header["User-Agent"] = s.UA
		}
		src.API = resolveAPI(origin, s.API)
		src.Jar = resolveAPI(origin, stripMD5(s.Jar))
		src.Ext = resolveExt(origin, s.Ext)
		classify(&src)
		cfg.Sites = append(cfg.Sites, src)
	}
	var lives []liveJSON
	_ = json.Unmarshal(obj["lives"], &lives)
	for _, l := range lives {
		addr, err := resolveRef(origin, l.URL)
		if err != nil {
			continue
		}
		h := parseHeader(l.Header)
		if l.UA != "" {
			if h == nil {
				h = map[string]string{}
			}
			h["User-Agent"] = l.UA
		}
		if l.Referer != "" {
			if h == nil {
				h = map[string]string{}
			}
			h["Referer"] = l.Referer
		}
		name := strings.TrimSpace(l.Name)
		if name == "" {
			name = "直播"
		}
		cfg.Lives = append(cfg.Lives, LiveSource{Name: name, URL: addr, Headers: h})
	}
	if len(cfg.Sites) == 0 && len(cfg.Lives) == 0 {
		if cfg, ok := playlistConfig(data, origin); ok {
			return cfg, nil
		}
		return nil, errNotConfig
	}
	for i := range cfg.Sites {
		cfg.Sites[i].LocalAPI = "/tvbox/s" + itoa(i)
	}
	return cfg, nil
}

func playlistConfig(data []byte, origin string) (*Config, bool) {
	if !isLiveText(data) {
		return nil, false
	}
	if _, err := ParsePlaylist(string(data), origin, nil); err != nil {
		return nil, false
	}
	return &Config{Origin: origin, Lives: []LiveSource{{Name: "直播", URL: origin}}}, true
}

func isLiveText(data []byte) bool {
	text := strings.TrimPrefix(string(data), "\uFEFF")
	var first string
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(strings.TrimRight(line, "\r"))
		if line != "" {
			first = line
			break
		}
	}
	if first == "" || strings.HasPrefix(first, "{") || strings.HasPrefix(first, "[") {
		return false
	}
	if strings.HasPrefix(first, "#EXTM3U") || strings.HasPrefix(first, "#EXTINF:") {
		return true
	}
	name, rest, ok := strings.Cut(first, ",")
	if !ok || strings.TrimSpace(name) == "" {
		return false
	}
	rest = strings.TrimSpace(rest)
	return rest == "#genre#" || strings.Contains(rest, "://")
}

func classify(s *Source) {
	switch s.Type {
	case 0:
		if isHTTP(s.API) {
			s.Kind = "cms-xml"
			return
		}
	case 1, 4:
		if isHTTP(s.API) {
			s.Kind = "cms-json"
			return
		}
	case 3:
		path := apiPath(s.API)
		switch {
		case strings.HasSuffix(path, ".js"):
			s.Kind = "drpy"
			return
		case strings.Contains(path, ".py") || (strings.HasPrefix(s.API, "py_") && extMentionsPy(s.Ext)):
			s.Kind = "python"
			s.Unsupported = true
			s.Reason = "Python 站点需要单独的 Python 运行时，当前版本没有内置，所以这个站点不能运行"
			return
		default:
			s.Kind = "jar"
			s.Unsupported = true
			s.Reason = "csp_ / JAR / DEX 站点依赖 Android 接口。桌面和网页模式没有 JVM、DEX 转码和 Android 兼容层，所以这个站点只能显示、不能打开"
			return
		}
	}
	s.Kind = "other"
	s.Unsupported = true
	if s.Type == 0 || s.Type == 1 || s.Type == 4 {
		s.Reason = "采集接口不是 http(s) 地址"
		return
	}
	s.Reason = "不支持的站点类型"
}

func extMentionsPy(raw json.RawMessage) bool {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return false
	}
	return strings.Contains(strings.ToLower(s), ".py")
}

func apiPath(api string) string {
	u, err := url.Parse(api)
	if err != nil {
		return strings.ToLower(strings.Split(api, "?")[0])
	}
	return strings.ToLower(u.Path)
}

func isHTTP(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://")
}

func parseSpider(origin string, raw json.RawMessage) string {
	return resolveAPI(origin, stripMD5(rawString(raw)))
}

func rawString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}
	return strings.TrimSpace(s)
}

func stripMD5(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(strings.ToLower(s), ";md5;"); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func resolveAPI(origin, ref string) string {
	ref = strings.TrimSpace(ref)
	if ref == "" || !shouldResolve(ref) {
		return ref
	}
	u, err := resolveRef(origin, ref)
	if err != nil {
		return ref
	}
	return u
}

func resolveOptional(origin, ref string) string {
	if strings.TrimSpace(ref) == "" {
		return ""
	}
	if !shouldResolve(ref) && !strings.HasPrefix(ref, "./") && !strings.HasPrefix(ref, "../") {
		return ref
	}
	u, err := resolveRef(origin, ref)
	if err != nil {
		return ref
	}
	return u
}

func shouldResolve(ref string) bool {
	r := strings.TrimSpace(ref)
	if strings.HasPrefix(r, "./") || strings.HasPrefix(r, "../") || strings.HasPrefix(r, "/") {
		return true
	}
	lower := strings.ToLower(r)
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") ||
		strings.HasPrefix(lower, "http:/") || strings.HasPrefix(lower, "https:/")
}

func resolveExt(origin string, raw json.RawMessage) json.RawMessage {
	if len(bytesTrim(raw)) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return raw
	}
	changed := false
	v = walkExt(v, origin, &changed)
	if !changed {
		return raw
	}
	b, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return b
}

func walkExt(v any, origin string, changed *bool) any {
	switch t := v.(type) {
	case string:
		if next, ok := resolveIfRelative(origin, t); ok {
			*changed = true
			return next
		}
		return t
	case map[string]any:
		for k, c := range t {
			t[k] = walkExt(c, origin, changed)
		}
		return t
	case []any:
		for i := range t {
			t[i] = walkExt(t[i], origin, changed)
		}
		return t
	default:
		return v
	}
}

func resolveIfRelative(origin, text string) (string, bool) {
	if !(strings.HasPrefix(text, "./") || strings.HasPrefix(text, "../") || strings.HasPrefix(text, "//")) {
		return "", false
	}
	u, err := resolveRef(origin, text)
	if err != nil {
		return "", false
	}
	return u, true
}

func resolveRef(origin, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errBadJSON
	}
	base, err := url.Parse(fixSchemeSlashes(origin))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", errBadJSON
	}
	u, err := url.Parse(fixSchemeSlashes(ref))
	if err != nil {
		return "", err
	}
	out := base.ResolveReference(u)
	if out.Scheme != "http" && out.Scheme != "https" {
		return "", errBadJSON
	}
	return out.String(), nil
}

func fixSchemeSlashes(s string) string {
	lower := strings.ToLower(s)
	for _, scheme := range []string{"https", "http"} {
		prefix := scheme + ":"
		if !strings.HasPrefix(lower, prefix) {
			continue
		}
		rest := strings.TrimLeft(s[len(prefix):], "/")
		return scheme + "://" + rest
	}
	return s
}

func parseHosts(raw json.RawMessage) map[string]string {
	if len(bytesTrim(raw)) == 0 {
		return nil
	}
	var lines []string
	if json.Unmarshal(raw, &lines) == nil {
		return hostLines(lines)
	}
	var one string
	if json.Unmarshal(raw, &one) == nil {
		return hostLines(strings.Split(one, "\n"))
	}
	var obj map[string]string
	if json.Unmarshal(raw, &obj) == nil {
		out := map[string]string{}
		for k, v := range obj {
			k, v = strings.TrimSpace(k), strings.TrimSpace(v)
			if k != "" && v != "" {
				out[strings.ToLower(k)] = v
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return nil
}

func hostLines(lines []string) map[string]string {
	out := map[string]string{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.ToLower(strings.TrimSpace(k)), strings.TrimSpace(v)
		if k == "" || v == "" {
			continue
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func parseRules(raw json.RawMessage) []Rule {
	var rules []Rule
	if json.Unmarshal(raw, &rules) != nil {
		return nil
	}
	var out []Rule
	for _, r := range rules {
		if len(r.Hosts) == 0 || len(r.Regex) == 0 {
			continue
		}
		out = append(out, r)
	}
	return out
}

func parseHeader(raw json.RawMessage) map[string]string {
	if len(bytesTrim(raw)) == 0 {
		return nil
	}
	var m map[string]string
	if json.Unmarshal(raw, &m) == nil {
		return cleanHeader(m)
	}
	var s string
	if json.Unmarshal(raw, &s) == nil && json.Unmarshal([]byte(s), &m) == nil {
		return cleanHeader(m)
	}
	return nil
}

func cleanHeader(m map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range m {
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k != "" && v != "" {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func bytesTrim(b []byte) []byte { return bytesTrimSpace(b) }

func bytesTrimSpace(b []byte) []byte {
	return bytesTrimLeft(bytesTrimRight(b))
}

func bytesTrimLeft(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\n' || b[0] == '\r' || b[0] == '\t') {
		b = b[1:]
	}
	return b
}

func bytesTrimRight(b []byte) []byte {
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	return b
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

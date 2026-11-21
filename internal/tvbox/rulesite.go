package tvbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/freelxs/jstv-player/internal/netx"
)

// crawl runs an ext-driven list template. xyq selects the XYQHiker field names;
// otherwise the public XBPQ && rules are used. Both share one page walker.
func (s *Server) crawl(ctx context.Context, src Source, action string, body map[string]any, xyq bool) (any, int, error) {
	if action == "init" {
		return map[string]any{}, http.StatusOK, nil
	}
	cfg, err := s.ruleConfig(ctx, src)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	if xyq {
		cfg = xyqAsXBPQ(cfg)
	}
	site := &ruleSite{srv: s, cfg: cfg, header: src.Header}
	switch action {
	case "home":
		return site.home(ctx)
	case "category":
		return site.category(ctx, scalar(body["id"]), positive(body["page"]))
	case "detail":
		return site.detail(ctx, scalar(body["id"]))
	case "search":
		return site.search(ctx, scalar(body["wd"]), positive(body["page"]))
	case "play":
		return site.play(scalar(body["id"])), http.StatusOK, nil
	default:
		return nil, http.StatusNotFound, errors.New("未知操作")
	}
}

type ruleSite struct {
	srv    *Server
	cfg    map[string]string
	header map[string]string
}

type namedID struct{ name, id string }

func (r *ruleSite) home(ctx context.Context) (any, int, error) {
	cats := r.categories()
	class := make([]any, 0, len(cats))
	for _, c := range cats {
		class = append(class, map[string]any{"type_id": c.id, "type_name": c.name})
	}
	var list []any
	var err error
	if len(cats) > 0 {
		list, err = r.page(ctx, cats[0].id, 1)
	}
	if len(class) == 0 && len(list) == 0 {
		if err != nil {
			return nil, http.StatusBadGateway, err
		}
		return nil, http.StatusBadGateway, errors.New("规则没有分类")
	}
	return map[string]any{"class": class, "list": list}, http.StatusOK, nil
}

func (r *ruleSite) category(ctx context.Context, tid string, page int) (any, int, error) {
	list, err := r.page(ctx, tid, page)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	pages := 1
	if len(list) > 0 {
		pages = page + 1
	}
	return map[string]any{"page": page, "pagecount": pages, "limit": len(list), "total": len(list), "list": list}, http.StatusOK, nil
}

func (r *ruleSite) search(ctx context.Context, wd string, page int) (any, int, error) {
	tpl := strings.TrimSpace(r.cfg["搜索url"])
	if tpl == "" || strings.TrimSpace(wd) == "" {
		return map[string]any{"page": page, "pagecount": 1, "list": []any{}}, http.StatusOK, nil
	}
	addr := applyPlaceholders(tpl, map[string]string{"wd": wd, "pg": strconv.Itoa(page), "searchPg": strconv.Itoa(page), "catePg": strconv.Itoa(page)})
	body, pageURL, err := r.srv.getText(ctx, addr, r.requestHeaders())
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	list := r.items(body, pageURL)
	pages := 1
	if len(list) > 0 {
		pages = page + 1
	}
	return map[string]any{"page": page, "pagecount": pages, "list": list}, http.StatusOK, nil
}

func (r *ruleSite) detail(ctx context.Context, id string) (any, int, error) {
	body, pageURL, err := r.srv.getText(ctx, id, r.requestHeaders())
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	from, play := r.playLines(body, pageURL)
	name := plainText(r.pick(body, r.cfg["详情标题"]))
	if name == "" {
		name = "详情"
	}
	vod := map[string]any{
		"vod_id": id, "vod_name": name,
		"vod_pic":       r.absolute(pageURL, r.pick(body, r.cfg["图片"])),
		"vod_content":   plainText(r.pick(body, r.cfg["简介"])),
		"vod_director":  plainText(r.pick(body, r.cfg["导演"])),
		"vod_actor":     plainText(r.pick(body, r.cfg["主演"])),
		"vod_play_from": from, "vod_play_url": play,
	}
	return map[string]any{"list": []any{vod}}, http.StatusOK, nil
}

func (r *ruleSite) play(id string) map[string]any {
	parse := 1
	if directMedia(id) {
		parse = 0
	}
	out := map[string]any{"parse": parse, "url": id}
	h := map[string]any{}
	for k, v := range headerSpec(r.cfg["播放请求头"]) {
		h[k] = v
	}
	for k, v := range r.header {
		if _, ok := h[k]; !ok {
			h[k] = v
		}
	}
	if len(h) > 0 {
		out["header"] = h
	}
	return out
}

func (r *ruleSite) page(ctx context.Context, tid string, pg int) ([]any, error) {
	tpl := strings.TrimSpace(firstNonEmpty(r.cfg["分类url"], r.cfg["分类链接"]))
	if tpl == "" {
		return nil, errors.New("规则没有分类地址")
	}
	addr := applyPlaceholders(tpl, map[string]string{
		"cateId": tid, "catePg": strconv.Itoa(pg), "pg": strconv.Itoa(pg),
		"area": "", "year": "", "by": "", "class": "", "lang": "", "wd": "",
	})
	body, pageURL, err := r.srv.getText(ctx, addr, r.requestHeaders())
	if err != nil {
		return nil, err
	}
	return r.items(body, pageURL), nil
}

func (r *ruleSite) categories() []namedID {
	namesRaw := firstNonEmpty(r.cfg["分类"], r.cfg["分类名称"])
	idsRaw := firstNonEmpty(r.cfg["分类值"], r.cfg["分类名称替换词"])
	if strings.Contains(namesRaw, "$") {
		var out []namedID
		for _, part := range strings.Split(namesRaw, "#") {
			name, id, ok := strings.Cut(part, "$")
			name, id = strings.TrimSpace(name), strings.TrimSpace(id)
			if ok && name != "" && id != "" {
				out = append(out, namedID{name, id})
			}
		}
		return out
	}
	names, ids := splitChoices(namesRaw), splitChoices(idsRaw)
	if len(ids) == 0 {
		ids = names
	}
	n := len(names)
	if len(ids) < n {
		n = len(ids)
	}
	var out []namedID
	for i := 0; i < n; i++ {
		if names[i] == "" || ids[i] == "" {
			continue
		}
		out = append(out, namedID{names[i], ids[i]})
	}
	return out
}

func (r *ruleSite) items(doc, page string) []any {
	if r.useJsoup() {
		return r.jsoupItems(doc, page)
	}
	rule := orDefault(r.cfg["数组"], `module-item-pic&&</a>`)
	var list []any
	for _, block := range cutBlocks(doc, rule) {
		name := plainText(cutBetween(block, orDefault(r.cfg["标题"], `title="&&"`)))
		href := r.absolute(page, r.withPrefix(cutBetween(block, orDefault(r.cfg["链接"], `href="&&"`))))
		pic := r.absolute(page, firstNonEmpty(
			cutBetween(block, orDefault(r.cfg["图片"], `data-src="&&"`)),
			cutBetween(block, `data-original="&&"`),
			cutBetween(block, `src="&&"`),
		))
		note := plainText(cutBetween(block, r.cfg["副标题"]))
		if name == "" || href == "" {
			continue
		}
		list = append(list, map[string]any{"vod_id": href, "vod_name": name, "vod_pic": pic, "vod_remarks": note})
		if len(list) >= 80 {
			break
		}
	}
	return list
}

func (r *ruleSite) jsoupItems(docText, page string) []any {
	doc, err := parseHTMLDocument(docText)
	if err != nil {
		return nil
	}
	sel := strings.TrimSpace(r.cfg["数组"])
	if sel == "" {
		sel = ".module-item"
	}
	var list []any
	doc.Find(sel).Each(func(_ int, item *goquery.Selection) {
		if len(list) >= 80 {
			return
		}
		name := plainText(jsoupValue(item, orDefault(r.cfg["标题"], "a&&Text")))
		href := r.absolute(page, r.withPrefix(jsoupValue(item, orDefault(r.cfg["链接"], "a&&href"))))
		pic := r.absolute(page, firstNonEmpty(
			jsoupValue(item, orDefault(r.cfg["图片"], "img&&data-src")),
			jsoupValue(item, "img&&data-original"),
			jsoupValue(item, "img&&src"),
		))
		note := plainText(jsoupValue(item, r.cfg["副标题"]))
		if name == "" || href == "" {
			return
		}
		list = append(list, map[string]any{"vod_id": href, "vod_name": name, "vod_pic": pic, "vod_remarks": note})
	})
	return list
}

func (r *ruleSite) playLines(doc, page string) (string, string) {
	lineBlocks := cutBlocks(doc, orDefault(r.cfg["线路数组"], "module-tab-item&&</div>"))
	playBlocks := cutBlocks(doc, orDefault(r.cfg["播放数组"], "module-play-list&&</div>"))
	if len(playBlocks) == 0 {
		playBlocks = []string{doc}
	}
	titleRule := orDefault(r.cfg["线路标题"], "<span>&&</span>")
	var flags, groups []string
	for i, block := range playBlocks {
		flag := "播放"
		if i < len(lineBlocks) {
			flag = plainText(cutBetween(lineBlocks[i], titleRule))
		}
		if flag == "" {
			flag = fmt.Sprintf("线路%d", i+1)
		}
		eps := r.episodes(block, page)
		if len(eps) == 0 {
			continue
		}
		flags = append(flags, flag)
		groups = append(groups, strings.Join(eps, "#"))
	}
	return strings.Join(flags, "$$$"), strings.Join(groups, "$$$")
}

var anchorPattern = regexp.MustCompile(`(?is)<a\b[^>]*href\s*=\s*["']([^"']+)["'][^>]*>(.*?)</a>`)
var tagPattern = regexp.MustCompile(`(?s)<[^>]*>`)

func (r *ruleSite) episodes(block, page string) []string {
	var out []string
	for i, m := range anchorPattern.FindAllStringSubmatch(block, 400) {
		href := r.absolute(page, html.UnescapeString(m[1]))
		if href == "" {
			continue
		}
		name := plainText(m[2])
		if name == "" {
			name = fmt.Sprintf("第%d集", i+1)
		}
		out = append(out, name+"$"+href)
	}
	return out
}

func (r *ruleSite) pick(doc, rule string) string {
	if strings.TrimSpace(rule) == "" {
		return ""
	}
	if r.useJsoup() && !strings.Contains(rule, "<") {
		parsed, err := goquery.NewDocumentFromReader(strings.NewReader(doc))
		if err != nil {
			return ""
		}
		return jsoupValue(parsed.Selection, rule)
	}
	return cutBetween(doc, rule)
}

func (r *ruleSite) useJsoup() bool {
	if r.cfg["jsoup"] == "1" || r.cfg["分类片单是否Jsoup写法"] == "1" {
		return true
	}
	arr := r.cfg["数组"]
	return arr != "" && !strings.Contains(arr, "&&") && !strings.Contains(arr, "<")
}

func (r *ruleSite) withPrefix(ref string) string {
	ref = strings.TrimSpace(ref)
	prefix := strings.TrimSpace(r.cfg["链接前缀"])
	if ref == "" || prefix == "" || strings.Contains(ref, "://") {
		return ref
	}
	return prefix + ref
}

func (r *ruleSite) absolute(base, ref string) string {
	ref = strings.TrimSpace(html.UnescapeString(ref))
	if ref == "" || strings.HasPrefix(strings.ToLower(ref), "javascript:") {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil {
		return ""
	}
	if b, err := url.Parse(base); err == nil {
		u = b.ResolveReference(u)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	return u.String()
}

func (r *ruleSite) requestHeaders() map[string]string {
	out := map[string]string{}
	for k, v := range r.header {
		out[k] = v
	}
	for k, v := range headerSpec(firstNonEmpty(r.cfg["请求头"], r.cfg["播放请求头"])) {
		out[k] = v
	}
	return out
}

func (s *Server) getText(ctx context.Context, raw string, header map[string]string) (string, string, error) {
	if ascii, err := netx.ASCIIURL(raw); err == nil && ascii != "" {
		raw = ascii
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	for k, v := range header {
		req.Header.Set(k, v)
	}
	client := s.client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	final := raw
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return string(maybeGBK(b)), final, nil
}

func (s *Server) ruleConfig(ctx context.Context, src Source) (map[string]string, error) {
	raw := bytesTrim(src.Ext)
	if len(raw) == 0 || string(raw) == "null" {
		return nil, errors.New("这个站点没有 ext 规则")
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		asString = strings.TrimSpace(asString)
		switch {
		case strings.HasPrefix(asString, "http://"), strings.HasPrefix(asString, "https://"):
			body, _, err := s.getText(ctx, asString, nil)
			if err != nil || !jsonLike(body) {
				// A bare API root, not a rule document.
				return map[string]string{"url": asString}, nil
			}
			raw = []byte(body)
		case strings.HasPrefix(asString, "{"), strings.HasPrefix(asString, "["):
			raw = []byte(asString)
		default:
			if asString == "" {
				return nil, errors.New("这个站点没有 ext 规则")
			}
			return map[string]string{"url": asString}, nil
		}
	}
	if decoded, err := Decode(raw); err == nil {
		raw = decoded
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) != nil {
		return nil, errors.New("ext 规则不是 JSON")
	}
	out := map[string]string{}
	for k, v := range obj {
		switch t := v.(type) {
		case string:
			out[k] = t
		case float64:
			out[k] = strconv.FormatFloat(t, 'f', -1, 64)
		case bool:
			if t {
				out[k] = "1"
			} else {
				out[k] = "0"
			}
		default:
			b, err := json.Marshal(t)
			if err == nil {
				out[k] = string(b)
			}
		}
	}
	return out, nil
}

func xyqAsXBPQ(cfg map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range cfg {
		out[k] = v
	}
	copyKey := func(dst, a, b string) {
		if v := firstNonEmpty(cfg[a], cfg[b]); v != "" {
			out[dst] = v
		}
	}
	copyKey("分类", "分类名称", "分类")
	copyKey("分类值", "分类名称替换词", "分类值")
	copyKey("分类url", "分类链接", "分类url")
	copyKey("数组", "分类列表数组规则", "数组")
	copyKey("标题", "分类片单标题", "标题")
	copyKey("图片", "分类片单图片", "图片")
	copyKey("链接", "分类片单链接", "链接")
	copyKey("副标题", "分类片单副标题", "副标题")
	copyKey("搜索url", "搜索链接", "搜索url")
	copyKey("线路数组", "线路列表数组规则", "线路数组")
	copyKey("播放数组", "播放列表数组规则", "播放数组")
	copyKey("播放链接", "选集链接", "播放链接")
	copyKey("播放请求头", "请求头", "播放请求头")
	if cfg["分类片单是否Jsoup写法"] == "1" || cfg["分类截取模式"] == "0" {
		out["jsoup"] = "1"
	}
	return out
}

func applyPlaceholders(tpl string, vars map[string]string) string {
	for k, v := range vars {
		tpl = strings.ReplaceAll(tpl, "{"+k+"}", url.PathEscape(v))
	}
	return tpl
}

func splitChoices(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	sep := "&"
	if strings.Contains(s, "#") && !strings.Contains(s, "&") {
		sep = "#"
	}
	var out []string
	for _, part := range strings.Split(s, sep) {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitBan(rule string) (string, []string) {
	if i := strings.LastIndex(rule, "[不包含:"); i >= 0 && strings.HasSuffix(rule, "]") {
		inner := rule[i+len("[不包含:") : len(rule)-1]
		return rule[:i], strings.Split(inner, "|")
	}
	return rule, nil
}

func cutBlocks(doc, rule string) []string {
	body, ban := splitBan(rule)
	left, right, ok := strings.Cut(body, "&&")
	if !ok || left == "" || right == "" {
		return nil
	}
	var out []string
	rest := doc
	for len(out) < 120 {
		i := strings.Index(rest, left)
		if i < 0 {
			break
		}
		rest = rest[i+len(left):]
		j := strings.Index(rest, right)
		if j < 0 {
			break
		}
		piece := rest[:j]
		rest = rest[j+len(right):]
		if containsAny(piece, ban) {
			continue
		}
		out = append(out, piece)
	}
	return out
}

func cutBetween(doc, rule string) string {
	body, ban := splitBan(rule)
	if strings.TrimSpace(body) == "" {
		return ""
	}
	left, right, ok := strings.Cut(body, "&&")
	if !ok {
		return ""
	}
	i := 0
	if left != "" {
		p := strings.Index(doc, left)
		if p < 0 {
			return ""
		}
		i = p + len(left)
	}
	rest := doc[i:]
	if right != "" {
		j := strings.Index(rest, right)
		if j < 0 {
			return ""
		}
		rest = rest[:j]
	}
	if containsAny(rest, ban) {
		return ""
	}
	return strings.TrimSpace(rest)
}

func containsAny(s string, parts []string) bool {
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" && strings.Contains(s, part) {
			return true
		}
	}
	return false
}

func jsoupValue(root *goquery.Selection, rule string) string {
	rule = strings.TrimSpace(rule)
	if rule == "" || root == nil || root.Length() == 0 {
		return ""
	}
	parts := strings.Split(rule, "&&")
	node := root
	attr := ""
	chain := parts
	if jsoupAttr(parts[len(parts)-1]) {
		attr = parts[len(parts)-1]
		chain = parts[:len(parts)-1]
	}
	for _, part := range chain {
		part = strings.TrimSpace(part)
		if part == "" || strings.EqualFold(part, "body") {
			continue
		}
		node = node.Find(part)
	}
	node = node.First()
	if attr == "" || strings.EqualFold(attr, "text") {
		return strings.TrimSpace(node.Text())
	}
	if v, ok := node.Attr(attr); ok {
		return v
	}
	return ""
}

func jsoupAttr(s string) bool {
	switch strings.ToLower(s) {
	case "text", "href", "src", "data-src", "data-original", "title", "alt", "content":
		return true
	default:
		return false
	}
}

func headerSpec(spec string) map[string]string {
	out := map[string]string{}
	for _, part := range strings.Split(spec, "#") {
		k, v, ok := strings.Cut(part, "$")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if k == "" {
			continue
		}
		if strings.EqualFold(v, "MOBILE_UA") || strings.EqualFold(v, "PC_UA") {
			v = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
		}
		out[k] = v
	}
	return out
}

func plainText(s string) string {
	s = tagPattern.ReplaceAllString(s, "")
	return strings.TrimSpace(html.UnescapeString(s))
}

func jsonLike(s string) bool {
	s = strings.TrimSpace(s)
	return strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")
}

func orDefault(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

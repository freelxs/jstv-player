package kazumi

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/antchfx/htmlquery"
	"golang.org/x/net/html"
)

// NormalizeRelativeXPath adapts Kazumi/Dart xpath_selector relative sub-queries
// to standard XPath 1.0 evaluated on a context node.
func NormalizeRelativeXPath(expr string) string {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return ""
	}
	parts := splitTopLevelUnion(expr)
	for i, part := range parts {
		p := strings.TrimSpace(part)
		if strings.HasPrefix(p, "//") {
			p = "." + p
		} else if strings.HasPrefix(p, "/") {
			p = "." + p
		}
		parts[i] = p
	}
	return strings.Join(parts, " | ")
}

func splitTopLevelUnion(expr string) []string {
	var out []string
	depth := 0
	var quote byte
	start := 0
	for i := 0; i < len(expr); i++ {
		c := expr[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '[', '(':
			depth++
		case ']', ')':
			if depth > 0 {
				depth--
			}
		case '|':
			if depth == 0 {
				out = append(out, expr[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, expr[start:])
	return out
}

func queryXPathAll(node *html.Node, expr string, relative bool) ([]*html.Node, error) {
	if node == nil {
		return nil, nil
	}
	q := strings.TrimSpace(expr)
	if q == "" {
		return nil, fmt.Errorf("XPath 不能为空")
	}
	if relative {
		q = NormalizeRelativeXPath(q)
	}
	nodes, err := htmlquery.QueryAll(node, q)
	if err != nil {
		return nil, fmt.Errorf("XPath 无效 (%s): %w", expr, err)
	}
	return nodes, nil
}

func queryXPathOne(node *html.Node, expr string, relative bool) (*html.Node, error) {
	nodes, err := queryXPathAll(node, expr, relative)
	if err != nil || len(nodes) == 0 {
		return nil, err
	}
	return nodes[0], nil
}

func nodeText(n *html.Node) string {
	if n == nil {
		return ""
	}
	return strings.TrimSpace(htmlquery.InnerText(n))
}

func nodeHref(n *html.Node) string {
	if n == nil {
		return ""
	}
	if n.Type == html.ElementNode {
		if v := strings.TrimSpace(htmlquery.SelectAttr(n, "href")); v != "" {
			return v
		}
		return ""
	}
	return strings.TrimSpace(htmlquery.InnerText(n))
}

func extractSearchPic(node *html.Node, baseURL string) string {
	if node == nil {
		return ""
	}
	if img, err := htmlquery.Query(node, ".//img"); err == nil && img != nil {
		for _, attr := range []string{"data-original", "data-src", "data-lazy-src", "data-echo", "data-url", "src"} {
			if resolved := resolvePosterCandidate(baseURL, htmlquery.SelectAttr(img, attr)); resolved != "" {
				return resolved
			}
		}
	}
	if els, err := htmlquery.QueryAll(node, ".//*[@data-original or @data-src or @data-lazy-src or @style]"); err == nil {
		for _, el := range els {
			for _, attr := range []string{"data-original", "data-src", "data-lazy-src"} {
				if resolved := resolvePosterCandidate(baseURL, htmlquery.SelectAttr(el, attr)); resolved != "" {
					return resolved
				}
			}
			if style := htmlquery.SelectAttr(el, "style"); style != "" {
				for _, raw := range extractStyleURLs(style) {
					if resolved := resolvePosterCandidate(baseURL, raw); resolved != "" {
						return resolved
					}
				}
			}
		}
	}
	return ""
}

var cssURLRe = regexp.MustCompile(`(?i)url\(\s*['"]?([^'")\s]+)['"]?\s*\)`)

func extractStyleURLs(style string) []string {
	matches := cssURLRe.FindAllStringSubmatch(style, -1)
	if len(matches) == 0 {
		return nil
	}
	out := make([]string, 0, len(matches))
	for _, m := range matches {
		if len(m) >= 2 && m[1] != "" {
			out = append(out, m[1])
		}
	}
	return out
}

func resolvePosterCandidate(baseURL, raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(strings.ToLower(raw), "data:") {
		return ""
	}
	resolved := cleanPosterURL(NormalizeEpisodeURL(baseURL, raw))
	if strings.HasPrefix(resolved, "http://") || strings.HasPrefix(resolved, "https://") {
		return resolved
	}
	return ""
}

func cleanPosterURL(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || !isHTTPScheme(u.Scheme) || u.Host == "" {
		return raw
	}
	host := strings.ToLower(u.Hostname())
	// Baidu's legacy gimg proxy (e.g. gimg0.baidu.com/gimg/app=2001&...&src=https://...)
	// now returns a 52-byte 1x1 transparent GIF. Unwrap the real origin cover URL.
	if strings.HasSuffix(host, ".baidu.com") && strings.Contains(u.Path, "/gimg/") {
		full := raw
		if idx := strings.Index(full, "src="); idx >= 0 {
			inner := full[idx+4:]
			if amp := strings.IndexByte(inner, '&'); amp >= 0 && !strings.Contains(inner[:amp], "?") {
				inner = inner[:amp]
			}
			if decoded, err := url.QueryUnescape(inner); err == nil && (strings.HasPrefix(decoded, "http://") || strings.HasPrefix(decoded, "https://")) {
				inner = decoded
			}
			if strings.HasPrefix(inner, "http://") || strings.HasPrefix(inner, "https://") {
				return inner
			}
		}
	}
	return raw
}

func extractPageMeta(doc *html.Node, baseURL string) PageMeta {
	var out PageMeta
	if doc == nil {
		return out
	}
	if meta, err := htmlquery.Query(doc, "//meta[@property='og:title']"); err == nil && meta != nil {
		out.Title = strings.TrimSpace(htmlquery.SelectAttr(meta, "content"))
	}
	if out.Title == "" {
		if h, err := htmlquery.Query(doc, "//*[contains(@class,'detail_imform_name')] | //h1"); err == nil && h != nil {
			out.Title = strings.TrimSpace(htmlquery.InnerText(h))
		}
	}
	if out.Title == "" {
		if t, err := htmlquery.Query(doc, "//title"); err == nil && t != nil {
			out.Title = strings.TrimSpace(htmlquery.InnerText(t))
		}
	}
	if meta, err := htmlquery.Query(doc, "//meta[@property='og:image']"); err == nil && meta != nil {
		out.Pic = resolvePosterCandidate(baseURL, htmlquery.SelectAttr(meta, "content"))
	}
	if out.Pic == "" {
		if node, err := htmlquery.Query(doc, "//*[contains(@class,'detail') or contains(@class,'module-item-pic') or contains(@class,'stui-content__thumb') or contains(@class,'myui-content__thumb') or contains(@class,'poster')]"); err == nil && node != nil {
			out.Pic = extractSearchPic(node, baseURL)
		}
	}
	out.Content = extractHTMLDescription(doc)
	extractHTMLDetailKV(doc, &out)
	return out
}

var synopsisQueries = []string{
	"//*[contains(@class,'detail_imform_desc_pre')]",
	"//*[contains(@class,'module-info-introduction-content')]",
	"//*[contains(@class,'stui-content__desc')]",
	"//*[contains(@class,'myui-content__detail')]//*[contains(@class,'sketch')]",
	"//*[contains(@class,'data') and contains(@class,'sketch')]",
	"//*[contains(@class,'sketch') and contains(@class,'content')]",
	"//*[@id='vod_content']",
	"//*[contains(@class,'vod_content')]",
	"//*[contains(@class,'vod-content')]",
	"//*[contains(@class,'video-info-content')]",
	"//*[contains(@class,'hl-content-text')]",
	"//*[contains(@class,'show-desc')]",
	"//*[contains(@class,'detail-desc')]",
	"//*[contains(@class,'detail_desc')]",
	"//*[contains(@class,'detail-content')]",
	"//*[contains(@class,'detail_content')]",
	"//*[contains(@class,'anthology-content')]",
	"//*[@itemprop='description']",
}

func extractHTMLDescription(doc *html.Node) string {
	for _, q := range synopsisQueries {
		nodes, err := htmlquery.QueryAll(doc, q)
		if err != nil {
			continue
		}
		for _, n := range nodes {
			if n == nil || (n.Type == html.ElementNode && strings.EqualFold(n.Data, "meta")) {
				continue
			}
			if text := cleanSynopsisText(htmlquery.InnerText(n)); len([]rune(text)) >= 4 {
				return text
			}
		}
	}
	for _, q := range []string{
		"//meta[@property='og:description']",
		"//meta[@name='description']",
		"//meta[@name='twitter:description']",
		"//meta[@itemprop='description']",
	} {
		if meta, err := htmlquery.Query(doc, q); err == nil && meta != nil {
			text := cleanSynopsisText(htmlquery.SelectAttr(meta, "content"))
			if len([]rune(text)) >= 4 && !isSEOBoilerplateDescription(text) {
				return text
			}
		}
	}
	return ""
}

var (
	synopsisLeadRe  = regexp.MustCompile(`^(?:剧情简介|内容简介|番剧简介|动漫简介|节目简介|故事简介|简介|详情|介绍)\s*[：:]\s*`)
	synopsisTrailRe = regexp.MustCompile(`\s*(?:\[?\s*(?:展开全部|收起全部|展开|收起|详情)\s*\]?|(?:\.\.\.|…)?\s*展开|>>)+\s*$`)
	htmlTagStripRe  = regexp.MustCompile(`<[^>]+>`)
)

func cleanSynopsisText(raw string) string {
	s := strings.TrimSpace(htmlTagStripRe.ReplaceAllString(raw, " "))
	if s == "" {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		line = synopsisLeadRe.ReplaceAllString(line, "")
		line = synopsisTrailRe.ReplaceAllString(line, "")
		line = strings.TrimSpace(line)
		if line != "" {
			kept = append(kept, line)
		}
	}
	out := strings.Join(kept, "\n")
	out = synopsisLeadRe.ReplaceAllString(out, "")
	out = synopsisTrailRe.ReplaceAllString(out, "")
	return strings.TrimSpace(out)
}

func isSEOBoilerplateDescription(s string) bool {
	for _, bad := range []string{"在线观看", "免费观看", "高清完整版", "全集免费", "手机免费", "无删减版"} {
		if strings.Contains(s, bad) && len([]rune(s)) < 120 {
			return true
		}
	}
	return false
}

func extractHTMLDetailKV(doc *html.Node, out *PageMeta) {
	if doc == nil || out == nil {
		return
	}
	nodes, err := htmlquery.QueryAll(doc, "//*[contains(@class,'detail_imform_kv')] | //*[contains(@class,'module-info-item')] | //*[contains(@class,'video-info-items')] | //*[contains(@class,'stui-content__detail')]//p | //*[contains(@class,'myui-content__detail')]//p")
	if err != nil {
		return
	}
	for _, n := range nodes {
		line := strings.Join(strings.Fields(htmlquery.InnerText(n)), " ")
		if line == "" {
			continue
		}
		applyDetailKVLine(line, out)
	}
}

func applyDetailKVLine(line string, out *PageMeta) {
	sepIdx := strings.IndexAny(line, "：:")
	if sepIdx <= 0 {
		return
	}
	_, size := int(0), 1
	if strings.HasPrefix(line[sepIdx:], "：") {
		size = len("：")
	}
	_ = size
	label := strings.TrimSpace(line[:sepIdx])
	val := strings.TrimSpace(line[sepIdx+size:])
	if val == "" || len([]rune(val)) > 120 {
		return
	}
	switch label {
	case "地区", "国家", "制片国家/地区":
		if out.Area == "" {
			out.Area = val
		}
	case "年份", "年代", "首播时间", "上映", "出品年份", "首播":
		if out.Year == "" {
			if m := yearPattern.FindString(val); m != "" {
				out.Year = m
			} else {
				out.Year = val
			}
		}
	case "类型", "剧情类型", "分类", "题材":
		if out.TypeName == "" {
			out.TypeName = val
		}
	case "导演", "监督", "总导演", "原作", "制作公司", "动画制作":
		if out.Director == "" {
			out.Director = val
		}
	case "主演", "声优", "配音", "CV", "演员":
		if out.Actor == "" {
			out.Actor = val
		}
	case "状态", "更新", "集数", "备注", "连载状态":
		if out.Remarks == "" {
			out.Remarks = val
		}
	}
}

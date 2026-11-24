package tvbox

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/andybalholm/cascadia"
	"golang.org/x/net/html"
)

const maxHTMLParseBytes = 1 << 20

var htmlTagNames = map[string]bool{
	"html": true, "head": true, "body": true, "div": true, "span": true,
	"a": true, "img": true, "p": true, "ul": true, "li": true, "ol": true,
	"dl": true, "dt": true, "dd": true, "video": true, "audio": true,
	"source": true, "script": true, "style": true, "table": true, "thead": true,
	"tbody": true, "tfoot": true, "tr": true, "td": true, "th": true,
	"h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"input": true, "meta": true, "link": true, "iframe": true, "section": true,
	"article": true, "header": true, "footer": true, "nav": true, "main": true,
	"aside": true, "em": true, "strong": true, "b": true, "i": true, "u": true,
	"s": true, "small": true, "big": true, "font": true, "form": true,
	"button": true, "label": true, "select": true, "option": true, "figure": true,
	"figcaption": true, "picture": true, "canvas": true, "svg": true, "br": true,
	"hr": true, "pre": true, "code": true, "blockquote": true, "sup": true, "sub": true,
}

var urlSchemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

func pdfh(doc, expr string) string {
	return evalPDFH(doc, expr, "text")
}

func pdfa(doc, expr string) string {
	expr = strings.TrimSpace(expr)
	if doc == "" || expr == "" {
		return "[]"
	}
	q, err := parseHTMLDocument(doc)
	if err != nil {
		return "[]"
	}
	for _, branch := range splitTopLevel(expr, "||") {
		branch = strings.TrimSpace(branch)
		if branch == "" {
			continue
		}
		parts := splitAndParts(branch)
		if len(parts) == 0 {
			continue
		}
		cur := q.Selection
		for _, part := range parts {
			cur = evalSelectorPart(cur, part)
			if cur.Length() == 0 {
				break
			}
		}
		if cur.Length() == 0 {
			continue
		}
		out := make([]string, 0, cur.Length())
		cur.Each(func(_ int, node *goquery.Selection) {
			if raw, err := goquery.OuterHtml(node); err == nil && raw != "" {
				out = append(out, raw)
			}
		})
		if len(out) > 0 {
			b, _ := json.Marshal(out)
			return string(b)
		}
	}
	return "[]"
}

func pd(doc, expr string) string {
	return pdWithBase(doc, expr, "")
}

func pdWithBase(doc, expr, base string) string {
	val := strings.TrimSpace(evalPDFH(doc, expr, "href||src"))
	if val == "" {
		return ""
	}
	if urlSchemeRe.MatchString(val) {
		return val
	}
	base = strings.TrimSpace(base)
	if base == "" {
		return val
	}
	return joinURL(base, val)
}

func evalPDFH(doc, expr, fallbackMode string) string {
	expr = strings.TrimSpace(expr)
	if doc == "" || expr == "" {
		return ""
	}
	q, err := parseHTMLDocument(doc)
	if err != nil {
		return ""
	}
	if shouldSplitOrRule(expr) {
		for _, branch := range splitTopLevel(expr, "||") {
			if val := evalSinglePDFH(q, branch, fallbackMode); val != "" {
				return val
			}
		}
		return ""
	}
	return evalSinglePDFH(q, expr, fallbackMode)
}

func shouldSplitOrRule(expr string) bool {
	branches := splitTopLevel(expr, "||")
	if len(branches) <= 1 {
		return false
	}
	withAnd := 0
	for _, b := range branches {
		if strings.Contains(b, "&&") {
			withAnd++
		}
	}
	return withAnd == 0 || withAnd == len(branches)
}

func evalSinglePDFH(q *goquery.Document, expr, fallbackMode string) string {
	parts := splitAndParts(expr)
	if len(parts) == 0 {
		return ""
	}
	modes := splitTopLevel(fallbackMode, "||")
	chain := parts
	if last := parts[len(parts)-1]; isModePart(last) {
		modes = splitTopLevel(last, "||")
		chain = parts[:len(parts)-1]
	}
	cur := q.Selection
	var excludes []string
	for idx, part := range chain {
		basePart, partExcludes := splitExclusions(part)
		if idx == len(chain)-1 && len(partExcludes) > 0 {
			excludes = partExcludes
		}
		if basePart != "" {
			cur = evalSelectorPart(cur, basePart)
		}
		if cur.Length() == 0 {
			return ""
		}
	}
	node := cur.First()
	if node.Length() == 0 {
		return ""
	}
	for _, mode := range modes {
		mode = strings.TrimSpace(mode)
		if mode == "" {
			continue
		}
		if val := extractNodeValue(node, mode, excludes); val != "" {
			return val
		}
	}
	return ""
}

func isModePart(part string) bool {
	alts := splitTopLevel(part, "||")
	if len(alts) == 0 {
		return false
	}
	for _, alt := range alts {
		if !isSingleModeToken(strings.TrimSpace(alt)) {
			return false
		}
	}
	return true
}

func isSingleModeToken(token string) bool {
	if token == "" {
		return false
	}
	lower := strings.ToLower(token)
	if lower == "text" || lower == "html" {
		return true
	}
	if strings.ContainsAny(token, ".#[]>:+~*,() \t\r\n") || strings.Contains(token, "--") {
		return false
	}
	return !htmlTagNames[lower]
}

func splitAndParts(expr string) []string {
	raw := strings.Split(expr, "&&")
	out := make([]string, 0, len(raw))
	for _, part := range raw {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func splitTopLevel(s, sep string) []string {
	var out []string
	depthParen := 0
	depthBracket := 0
	var quote byte
	start := 0
	for i := 0; i < len(s); i++ {
		ch := s[i]
		if quote != 0 {
			if ch == '\\' && i+1 < len(s) {
				i++
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
		case '(':
			depthParen++
		case ')':
			if depthParen > 0 {
				depthParen--
			}
		case '[':
			depthBracket++
		case ']':
			if depthBracket > 0 {
				depthBracket--
			}
		}
		if depthParen == 0 && depthBracket == 0 && strings.HasPrefix(s[i:], sep) {
			out = append(out, s[start:i])
			i += len(sep) - 1
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func splitExclusions(part string) (string, []string) {
	if !strings.Contains(part, "--") {
		return part, nil
	}
	pieces := splitTopLevel(part, "--")
	if len(pieces) <= 1 {
		return part, nil
	}
	base := strings.TrimSpace(pieces[0])
	var excludes []string
	for _, piece := range pieces[1:] {
		candidate := strings.TrimSpace(piece)
		if candidate == "" {
			continue
		}
		lower := strings.ToLower(candidate)
		if htmlTagNames[lower] || strings.HasPrefix(candidate, ".") || strings.HasPrefix(candidate, "#") || strings.ContainsAny(candidate, " .#[:") {
			excludes = append(excludes, candidate)
		} else {
			// Likely a BEM class modifier like .btn--primary; do not treat as exclusion.
			return part, nil
		}
	}
	if len(excludes) == 0 {
		return part, nil
	}
	return base, excludes
}

func evalSelectorPart(cur *goquery.Selection, part string) *goquery.Selection {
	alts := splitTopLevel(part, "||")
	for _, alt := range alts {
		alt = strings.TrimSpace(alt)
		if alt == "" {
			continue
		}
		res := evalCommaSelector(cur, alt)
		if res.Length() > 0 {
			return res
		}
	}
	return emptySelection(cur)
}

func evalCommaSelector(cur *goquery.Selection, sel string) *goquery.Selection {
	branches := splitTopLevel(sel, ",")
	if len(branches) == 1 {
		return evalSingleSelector(cur, strings.TrimSpace(branches[0]))
	}
	var acc *goquery.Selection
	for _, branch := range branches {
		branch = strings.TrimSpace(branch)
		if branch == "" {
			continue
		}
		matched := evalSingleSelector(cur, branch)
		if acc == nil {
			acc = matched
		} else if matched.Length() > 0 {
			acc = acc.AddSelection(matched)
		}
	}
	if acc == nil {
		return emptySelection(cur)
	}
	return acc
}

type pseudoFilter struct {
	kind string // eq, gt, lt, first, last, even, odd, contains
	num  int
	text string
}

type selectorChunk struct {
	combinator string // "", " ", ">"
	css        string
	filters    []pseudoFilter
}

func evalSingleSelector(cur *goquery.Selection, sel string) *goquery.Selection {
	if sel == "" {
		return cur
	}
	chunks := parseSelectorChunks(sel)
	out := cur
	for _, chunk := range chunks {
		if chunk.css != "" {
			out = matchCSS(out, chunk.css, chunk.combinator == ">")
			if out.Length() == 0 {
				return out
			}
		}
		for _, f := range chunk.filters {
			out = applyPseudoFilter(out, f)
			if out.Length() == 0 {
				return out
			}
		}
	}
	return out
}

func matchCSS(cur *goquery.Selection, css string, directChild bool) *goquery.Selection {
	matcher, err := cascadia.Compile(css)
	if err != nil {
		return emptySelection(cur)
	}
	if directChild {
		return cur.ChildrenMatcher(matcher)
	}
	found := cur.FindMatcher(matcher)
	if found.Length() > 0 {
		return found
	}
	return cur.FilterMatcher(matcher)
}

func parseSelectorChunks(sel string) []selectorChunk {
	tokens := tokenizeSelector(sel)
	var chunks []selectorChunk
	var cssBuf strings.Builder
	pendingComb := ""

	flush := func(filters []pseudoFilter) {
		css := strings.TrimSpace(cssBuf.String())
		cssBuf.Reset()
		if css == "" && len(filters) == 0 {
			return
		}
		chunks = append(chunks, selectorChunk{
			combinator: pendingComb,
			css:        css,
			filters:    filters,
		})
		pendingComb = ""
	}

	for _, tok := range tokens {
		if tok == ">" {
			if cssBuf.Len() == 0 && len(chunks) > 0 {
				pendingComb = ">"
			} else {
				if cssBuf.Len() > 0 {
					cssBuf.WriteString(" > ")
				} else {
					pendingComb = ">"
				}
			}
			continue
		}
		if tok == "+" || tok == "~" {
			if cssBuf.Len() > 0 {
				cssBuf.WriteString(" " + tok + " ")
			}
			continue
		}
		baseCSS, filters := extractJQueryPseudos(tok)
		if len(filters) == 0 {
			if cssBuf.Len() > 0 && !strings.HasSuffix(cssBuf.String(), " ") {
				cssBuf.WriteByte(' ')
			}
			cssBuf.WriteString(tok)
			continue
		}
		if baseCSS != "" {
			if cssBuf.Len() > 0 && !strings.HasSuffix(cssBuf.String(), " ") {
				cssBuf.WriteByte(' ')
			}
			cssBuf.WriteString(baseCSS)
		}
		flush(filters)
	}
	flush(nil)
	return chunks
}

func tokenizeSelector(sel string) []string {
	var tokens []string
	var buf strings.Builder
	depthParen := 0
	depthBracket := 0
	var quote byte

	push := func() {
		if s := strings.TrimSpace(buf.String()); s != "" {
			tokens = append(tokens, s)
		}
		buf.Reset()
	}

	for i := 0; i < len(sel); i++ {
		ch := sel[i]
		if quote != 0 {
			buf.WriteByte(ch)
			if ch == '\\' && i+1 < len(sel) {
				i++
				buf.WriteByte(sel[i])
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
			buf.WriteByte(ch)
		case '(':
			depthParen++
			buf.WriteByte(ch)
		case ')':
			if depthParen > 0 {
				depthParen--
			}
			buf.WriteByte(ch)
		case '[':
			depthBracket++
			buf.WriteByte(ch)
		case ']':
			if depthBracket > 0 {
				depthBracket--
			}
			buf.WriteByte(ch)
		case ' ', '\t', '\r', '\n':
			if depthParen == 0 && depthBracket == 0 {
				push()
			} else {
				buf.WriteByte(ch)
			}
		case '>', '+', '~':
			if depthParen == 0 && depthBracket == 0 {
				push()
				tokens = append(tokens, string(ch))
			} else {
				buf.WriteByte(ch)
			}
		default:
			buf.WriteByte(ch)
		}
	}
	push()
	return tokens
}

func extractJQueryPseudos(tok string) (string, []pseudoFilter) {
	var css strings.Builder
	var filters []pseudoFilter
	depthBracket := 0
	var quote byte

	for i := 0; i < len(tok); {
		ch := tok[i]
		if quote != 0 {
			css.WriteByte(ch)
			if ch == '\\' && i+1 < len(tok) {
				i++
				css.WriteByte(tok[i])
			} else if ch == quote {
				quote = 0
			}
			i++
			continue
		}
		if ch == '\'' || ch == '"' {
			quote = ch
			css.WriteByte(ch)
			i++
			continue
		}
		if ch == '[' {
			depthBracket++
			css.WriteByte(ch)
			i++
			continue
		}
		if ch == ']' {
			if depthBracket > 0 {
				depthBracket--
			}
			css.WriteByte(ch)
			i++
			continue
		}
		if ch == ':' && depthBracket == 0 {
			if f, nextIdx, ok := parsePseudoAt(tok, i); ok {
				filters = append(filters, f)
				i = nextIdx
				continue
			}
		}
		css.WriteByte(ch)
		i++
	}
	return css.String(), filters
}

func parsePseudoAt(tok string, idx int) (pseudoFilter, int, bool) {
	rest := tok[idx+1:]
	lower := strings.ToLower(rest)

	for _, kw := range []string{"first", "last", "even", "odd"} {
		if strings.HasPrefix(lower, kw) {
			after := idx + 1 + len(kw)
			if after == len(tok) || !isIdentChar(tok[after]) {
				return pseudoFilter{kind: kw}, after, true
			}
		}
	}
	for _, kw := range []string{"eq(", "gt(", "lt(", "contains("} {
		if strings.HasPrefix(lower, kw) {
			openIdx := idx + 1 + len(kw) - 1
			closeIdx := findMatchingParen(tok, openIdx)
			if closeIdx < 0 {
				return pseudoFilter{}, 0, false
			}
			arg := strings.TrimSpace(tok[openIdx+1 : closeIdx])
			kind := strings.TrimSuffix(kw, "(")
			if kind == "contains" {
				arg = strings.Trim(arg, `"'`)
				return pseudoFilter{kind: kind, text: arg}, closeIdx + 1, true
			}
			n, err := strconv.Atoi(arg)
			if err != nil {
				return pseudoFilter{}, 0, false
			}
			return pseudoFilter{kind: kind, num: n}, closeIdx + 1, true
		}
	}
	return pseudoFilter{}, 0, false
}

func isIdentChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '-' || ch == '_'
}

func findMatchingParen(s string, openIdx int) int {
	depth := 0
	var quote byte
	for i := openIdx; i < len(s); i++ {
		ch := s[i]
		if quote != 0 {
			if ch == '\\' && i+1 < len(s) {
				i++
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"':
			quote = ch
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func applyPseudoFilter(sel *goquery.Selection, f pseudoFilter) *goquery.Selection {
	n := sel.Length()
	if n == 0 {
		return sel
	}
	switch f.kind {
	case "first":
		return sel.Eq(0)
	case "last":
		return sel.Eq(n - 1)
	case "eq":
		idx := f.num
		if idx < 0 {
			idx += n
		}
		if idx < 0 || idx >= n {
			return emptySelection(sel)
		}
		return sel.Eq(idx)
	case "gt":
		idx := f.num
		if idx < 0 {
			idx += n
		}
		start := idx + 1
		if start < 0 {
			start = 0
		}
		if start >= n {
			return emptySelection(sel)
		}
		return sel.Slice(start, n)
	case "lt":
		idx := f.num
		if idx < 0 {
			idx += n
		}
		if idx <= 0 {
			return emptySelection(sel)
		}
		if idx > n {
			idx = n
		}
		return sel.Slice(0, idx)
	case "even":
		return sel.FilterFunction(func(i int, _ *goquery.Selection) bool { return i%2 == 0 })
	case "odd":
		return sel.FilterFunction(func(i int, _ *goquery.Selection) bool { return i%2 == 1 })
	case "contains":
		return sel.FilterFunction(func(_ int, s *goquery.Selection) bool { return strings.Contains(s.Text(), f.text) })
	default:
		return sel
	}
}

func emptySelection(ref *goquery.Selection) *goquery.Selection {
	if ref == nil {
		return &goquery.Selection{}
	}
	return ref.Slice(0, 0)
}

func extractNodeValue(node *goquery.Selection, mode string, excludes []string) string {
	if node == nil || node.Length() == 0 {
		return ""
	}
	target := node
	if len(excludes) > 0 && (strings.EqualFold(mode, "text") || strings.EqualFold(mode, "html") || mode == "") {
		clone := node.Clone()
		for _, ex := range excludes {
			if matcher, err := cascadia.Compile(ex); err == nil {
				clone.FindMatcher(matcher).Remove()
			}
		}
		target = clone
	}
	switch strings.ToLower(mode) {
	case "", "text":
		return strings.TrimSpace(target.Text())
	case "html":
		raw, _ := goquery.OuterHtml(target)
		return raw
	default:
		if v, ok := target.Attr(mode); ok && v != "" {
			return v
		}
		lower := strings.ToLower(mode)
		if lower != mode {
			if v, ok := target.Attr(lower); ok && v != "" {
				return v
			}
		}
		if len(target.Nodes) > 0 {
			if v := attrIgnoreCase(target.Nodes[0], lower); v != "" {
				return v
			}
		}
		nested := target.Find("[" + lower + "]").First()
		if nested.Length() > 0 {
			if v, ok := nested.Attr(lower); ok && v != "" {
				return v
			}
		}
		return ""
	}
}

func attrIgnoreCase(node *html.Node, lowerName string) string {
	if node == nil {
		return ""
	}
	for _, attr := range node.Attr {
		if strings.EqualFold(attr.Key, lowerName) {
			return attr.Val
		}
	}
	return ""
}

func parseHTMLDocument(doc string) (*goquery.Document, error) {
	if len(doc) > maxHTMLParseBytes {
		doc = doc[:maxHTMLParseBytes]
	}
	trimmed := strings.TrimSpace(doc)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "<tr"):
		doc = "<table><tbody>" + doc + "</tbody></table>"
	case strings.HasPrefix(lower, "<td"), strings.HasPrefix(lower, "<th"):
		doc = "<table><tbody><tr>" + doc + "</tr></tbody></table>"
	case strings.HasPrefix(lower, "<tbody"), strings.HasPrefix(lower, "<thead"), strings.HasPrefix(lower, "<tfoot"):
		doc = "<table>" + doc + "</table>"
	case strings.HasPrefix(lower, "<option"):
		doc = "<select>" + doc + "</select>"
	}
	return goquery.NewDocumentFromReader(strings.NewReader(doc))
}

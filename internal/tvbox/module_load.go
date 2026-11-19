package tvbox

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"modernc.org/quickjs"
)

type moduleImport struct {
	Spec  string
	Names string
}

// cutImport removes one leading import statement. names is the clause before
// from, including braces. spec is the module specifier.
func cutImport(src string) (spec, names, rest string, ok bool) {
	trimmed := strings.TrimLeft(src, " \t\r\n")
	if !strings.HasPrefix(trimmed, "import") {
		return "", "", src, false
	}
	body := trimmed[len("import"):]
	q1 := strings.IndexAny(body, `"'`)
	if q1 < 0 {
		return "", "", src, false
	}
	quote := body[q1]
	q2 := strings.IndexByte(body[q1+1:], quote)
	if q2 < 0 {
		return "", "", src, false
	}
	spec = body[q1+1 : q1+1+q2]
	names = strings.TrimSpace(body[:q1])
	names = strings.TrimSpace(strings.TrimSuffix(names, "from"))
	after := body[q1+1+q2+1:]
	after = strings.TrimLeft(after, " \t")
	if strings.HasPrefix(after, ";") {
		after = after[1:]
	}
	return spec, names, after, true
}

func absolutizeExtJSON(api string, raw json.RawMessage) string {
	if len(bytesTrim(raw)) == 0 {
		return "null"
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return string(raw)
	}
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "./") || strings.HasPrefix(text, "../") {
		if abs, err := resolveAsset(api, text); err == nil {
			b, _ := json.Marshal(abs)
			return string(b)
		}
	}
	return string(raw)
}

func resolveAsset(base, spec string) (string, error) {
	spec = strings.TrimSpace(spec)
	if strings.HasPrefix(spec, "http://") || strings.HasPrefix(spec, "https://") {
		return spec, nil
	}
	if strings.HasPrefix(spec, "assets://js/lib/") {
		spec = "./" + strings.TrimPrefix(spec, "assets://js/lib/")
	}
	bu, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	ru, err := url.Parse(spec)
	if err != nil {
		return "", err
	}
	return bu.ResolveReference(ru).String(), nil
}

func (e *qjsEngine) prepareSpider(ctx context.Context, baseURL, src string) (string, error) {
	var prelude strings.Builder
	rest := src
	seen := map[string]bool{}
	for {
		spec, names, next, ok := cutImport(rest)
		if !ok {
			break
		}
		global, err := e.evalDependency(ctx, baseURL, spec, seen, 0)
		if err != nil {
			return "", err
		}
		if bind := importBinding(names, global); bind != "" {
			prelude.WriteString(bind)
			prelude.WriteByte('\n')
		}
		rest = next
	}
	rest = strings.Replace(rest, "export default", "globalThis.__drpy =", 1)
	return prelude.String() + rest, nil
}

func importBinding(names, global string) string {
	names = strings.TrimSpace(names)
	if names == "" || global == "" {
		return ""
	}
	if strings.HasPrefix(names, "{") {
		return "const " + names + " = globalThis." + global + ";"
	}
	if strings.HasPrefix(names, "*") {
		alias := strings.TrimSpace(strings.TrimPrefix(names, "*"))
		alias = strings.TrimSpace(strings.TrimPrefix(alias, "as"))
		if alias == "" {
			return ""
		}
		return "const " + alias + " = globalThis." + global + ";"
	}
	return "const " + names + " = globalThis." + global + ".default;"
}

func (e *qjsEngine) evalDependency(ctx context.Context, baseURL, spec string, seen map[string]bool, depth int) (string, error) {
	if depth > 4 {
		return "", errors.New("脚本依赖嵌套过深")
	}
	abs, err := resolveAsset(baseURL, spec)
	if err != nil {
		return "", err
	}
	global := moduleGlobal(spec)
	if seen[abs] {
		return global, nil
	}
	seen[abs] = true
	body, err := fetchURLHeaders(ctx, e.client, abs, nil)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path.Base(spec), err)
	}
	text := string(body)
	var nested strings.Builder
	rest := text
	for {
		nspec, names, next, ok := cutImport(rest)
		if !ok {
			break
		}
		ng, err := e.evalDependency(ctx, abs, nspec, seen, depth+1)
		if err != nil {
			return "", err
		}
		if bind := importBinding(names, ng); bind != "" {
			nested.WriteString(bind)
			nested.WriteByte('\n')
		}
		rest = next
	}
	rest = exportAssignment(rest, global)
	if _, err := e.vm.Eval("(function(){\n"+nested.String()+rest+"\n})();\n0;", quickjs.EvalGlobal); err != nil {
		return "", fmt.Errorf("%s: %w", path.Base(spec), err)
	}
	return global, nil
}

var (
	exportFn    = regexp.MustCompile(`export\s+(async\s+)?function\s+(\w+)`)
	exportClass = regexp.MustCompile(`export\s+class\s+(\w+)`)
	exportDecl  = regexp.MustCompile(`export\s+(?:const|let|var)\s+(\w+)`)
)

func exportAssignment(src, global string) string {
	src, names := stripNamedExports(src)
	if i := strings.LastIndex(src, "export{"); i >= 0 {
		semi := strings.Index(src[i:], ";")
		if semi > 0 {
			clause := src[i : i+semi]
			if assign, ok := namedExportAssign(clause, global); ok {
				src = src[:i] + assign + src[i+semi+1:]
			}
		}
	}
	if i := strings.Index(src, "export default"); i >= 0 {
		// Default imports read module.default. export { x as default } already
		// builds that object; a bare `export default` has to do it too.
		src = src[:i] + "globalThis." + global + "=globalThis." + global + "||{};globalThis." + global + ".default=" + src[i+len("export default"):]
	}
	if len(names) == 0 {
		if !strings.Contains(src, "globalThis."+global) {
			src += "\nglobalThis." + global + " = globalThis." + global + " || {};"
		}
		return src
	}
	var b strings.Builder
	b.WriteString(src)
	b.WriteString("\nglobalThis.")
	b.WriteString(global)
	b.WriteString("=globalThis.")
	b.WriteString(global)
	b.WriteString("||{};")
	for _, name := range names {
		b.WriteString("globalThis.")
		b.WriteString(global)
		b.WriteByte('.')
		b.WriteString(name)
		b.WriteByte('=')
		b.WriteString(name)
		b.WriteByte(';')
	}
	return b.String()
}

func stripNamedExports(src string) (string, []string) {
	var names []string
	src = exportFn.ReplaceAllStringFunc(src, func(s string) string {
		m := exportFn.FindStringSubmatch(s)
		names = append(names, m[2])
		return strings.Replace(s, "export ", "", 1)
	})
	src = exportClass.ReplaceAllStringFunc(src, func(s string) string {
		m := exportClass.FindStringSubmatch(s)
		names = append(names, m[1])
		return strings.Replace(s, "export ", "", 1)
	})
	src = exportDecl.ReplaceAllStringFunc(src, func(s string) string {
		m := exportDecl.FindStringSubmatch(s)
		names = append(names, m[1])
		return strings.Replace(s, "export ", "", 1)
	})
	return src, names
}

func namedExportAssign(clause, global string) (string, bool) {
	start := strings.Index(clause, "{")
	end := strings.LastIndex(clause, "}")
	if start < 0 || end <= start {
		return "", false
	}
	var b strings.Builder
	b.WriteString("globalThis.")
	b.WriteString(global)
	b.WriteString("={")
	first := true
	for _, part := range strings.Split(clause[start+1:end], ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		local, name, ok := strings.Cut(part, " as ")
		if !ok {
			local, name = part, part
		}
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteString(strings.TrimSpace(name))
		b.WriteByte(':')
		b.WriteString(strings.TrimSpace(local))
	}
	b.WriteString("};")
	return b.String(), true
}

func moduleGlobal(spec string) string {
	base := path.Base(strings.Split(spec, "?")[0])
	base = strings.TrimSuffix(base, path.Ext(base))
	var b strings.Builder
	b.WriteString("__mod_")
	for _, r := range base {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == len("__mod_") {
		sum := sha256.Sum256([]byte(spec))
		b.WriteString(hex.EncodeToString(sum[:4]))
	}
	return b.String()
}

func localizeImports(ctx context.Context, client *http.Client, dir, baseURL, src string) (string, error) {
	return rewriteLocalImports(ctx, client, dir, baseURL, src, map[string]string{}, 0)
}

func rewriteLocalImports(ctx context.Context, client *http.Client, dir, baseURL, src string, seen map[string]string, depth int) (string, error) {
	if depth > 4 {
		return src, nil
	}
	var b strings.Builder
	rest := src
	for {
		spec, names, next, ok := cutImport(rest)
		if !ok {
			break
		}
		local, err := cacheImport(ctx, client, dir, baseURL, spec, seen, depth)
		if err != nil {
			return "", err
		}
		b.WriteString(rewrittenImport(names, "./"+local))
		b.WriteByte('\n')
		rest = next
	}
	b.WriteString(rest)
	return b.String(), nil
}

func rewrittenImport(names, spec string) string {
	names = strings.TrimSpace(names)
	if names == "" {
		return "import \"" + spec + "\";"
	}
	if strings.HasPrefix(names, "{") || strings.HasPrefix(names, "*") {
		return "import " + names + " from \"" + spec + "\";"
	}
	return "import " + names + " from \"" + spec + "\";"
}

func cacheImport(ctx context.Context, client *http.Client, dir, baseURL, spec string, seen map[string]string, depth int) (string, error) {
	abs, err := resolveAsset(baseURL, spec)
	if err != nil {
		return "", err
	}
	if local, ok := seen[abs]; ok {
		return local, nil
	}
	body, err := fetchURL(ctx, client, abs)
	if err != nil {
		return "", err
	}
	name := safeModuleName(spec)
	seen[abs] = name
	text, err := rewriteLocalImports(ctx, client, dir, abs, string(body), seen, depth+1)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		return "", err
	}
	return name, nil
}

func safeModuleName(spec string) string {
	base := path.Base(strings.Split(spec, "?")[0])
	if base == "." || base == "/" || base == "" {
		sum := sha256.Sum256([]byte(spec))
		return hex.EncodeToString(sum[:6]) + ".js"
	}
	base = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, base)
	return base
}

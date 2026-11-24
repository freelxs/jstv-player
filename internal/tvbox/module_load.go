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
	"sort"
	"strings"
	"sync"

	"modernc.org/quickjs"
)

type moduleImport struct {
	Spec  string
	Names string
}

// cutImport removes one leading import statement. names is the clause before
// from, including braces. spec is the module specifier.
func cutImport(src string) (spec, names, rest string, ok bool) {
	trimmed := skipLeadingTrivia(src)
	if !strings.HasPrefix(trimmed, "import") {
		return "", "", src, false
	}
	if len(trimmed) > len("import") {
		nextCh := trimmed[len("import")]
		if nextCh != ' ' && nextCh != '\t' && nextCh != '\r' && nextCh != '\n' && nextCh != '{' && nextCh != '*' && nextCh != '"' && nextCh != '\'' {
			return "", "", src, false
		}
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

func skipLeadingTrivia(src string) string {
	s := strings.TrimPrefix(src, "\uFEFF")
	for {
		s = strings.TrimLeft(s, " \t\r\n")
		if strings.HasPrefix(s, "//") {
			if nl := strings.IndexByte(s, '\n'); nl >= 0 {
				s = s[nl+1:]
				continue
			}
			return ""
		}
		if strings.HasPrefix(s, "/*") {
			if end := strings.Index(s[2:], "*/"); end >= 0 {
				s = s[2+end+2:]
				continue
			}
			return ""
		}
		return s
	}
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

// PrefetchScripts downloads all executable JavaScript assets (main scripts,
// imported modules, and remote ext rule URLs) referenced by drpy sites in cfg
// and populates cfg.Scripts and cfg.ScriptSHA256 prior to trust confirmation.
func PrefetchScripts(ctx context.Context, client *http.Client, cfg *Config) error {
	if cfg == nil {
		return nil
	}
	var (
		mu       sync.Mutex
		scripts  = map[string][]byte{}
		inflight = map[string]*prefetchCall{}
	)
	fetchOnce := func(rawURL string, headers map[string]string) ([]byte, error) {
		mu.Lock()
		if b, ok := scripts[rawURL]; ok && len(b) > 0 {
			mu.Unlock()
			return b, nil
		}
		if c, ok := inflight[rawURL]; ok {
			mu.Unlock()
			<-c.done
			return c.body, c.err
		}
		c := &prefetchCall{done: make(chan struct{})}
		inflight[rawURL] = c
		mu.Unlock()

		body, err := fetchURLHeaders(ctx, client, rawURL, headers)

		mu.Lock()
		c.body, c.err = body, err
		if err == nil && len(body) > 0 {
			scripts[rawURL] = body
		}
		delete(inflight, rawURL)
		close(c.done)
		mu.Unlock()
		return body, err
	}

	var prefetchDeps func(baseURL, src string, depth int) error
	prefetchDeps = func(baseURL, src string, depth int) error {
		if depth > 4 {
			return errors.New("脚本依赖嵌套过深")
		}
		rest := src
		for {
			spec, _, next, ok := cutImport(rest)
			if !ok {
				break
			}
			if _, builtin := builtinModuleSpec(spec); builtin {
				rest = next
				continue
			}
			abs, err := resolveAsset(baseURL, spec)
			if err != nil {
				return err
			}
			body, err := fetchOnce(abs, nil)
			if err != nil {
				return fmt.Errorf("%s: %w", path.Base(spec), err)
			}
			if err := prefetchDeps(abs, string(body), depth+1); err != nil {
				return err
			}
			rest = next
		}
		return nil
	}

	sem := make(chan struct{}, 8)
	var wg sync.WaitGroup
	for i := range cfg.Sites {
		site := &cfg.Sites[i]
		if site.Kind != "drpy" || site.Unsupported {
			continue
		}
		wg.Add(1)
		go func(site *Source) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				site.Unsupported = true
				site.Reason = fmt.Sprintf("下载 JavaScript 站点脚本失败: %v", ctx.Err())
				return
			}
			body, err := fetchOnce(site.API, site.Header)
			if err != nil {
				site.Unsupported = true
				site.Reason = fmt.Sprintf("下载 JavaScript 站点脚本失败: %v", err)
				return
			}
			if err := prefetchDeps(site.API, string(body), 0); err != nil {
				site.Unsupported = true
				site.Reason = fmt.Sprintf("下载 JavaScript 站点依赖失败: %v", err)
				return
			}
			var extStr string
			if json.Unmarshal([]byte(absolutizeExtJSON(site.API, site.Ext)), &extStr) == nil {
				extStr = strings.TrimSpace(extStr)
				if shouldPrefetchExt(site.API, extStr) {
					_, err := fetchOnce(extStr, site.Header)
					if err != nil {
						site.Unsupported = true
						site.Reason = fmt.Sprintf("下载 JavaScript 扩展规则失败: %v", err)
						return
					}
				}
			}
		}(site)
	}
	wg.Wait()
	if len(scripts) == 0 {
		cfg.Scripts = nil
		cfg.ScriptSHA256 = ""
		return nil
	}
	cfg.Scripts = scripts
	cfg.ScriptSHA256 = ComputeScriptSHA256(scripts)
	return nil
}

type prefetchCall struct {
	done chan struct{}
	body []byte
	err  error
}

// VerifyCachedScripts checks that all drpy sites and their dependencies in cfg
// are present in cfg.Scripts and recomputes cfg.ScriptSHA256.
func VerifyCachedScripts(cfg *Config) error {
	if cfg == nil {
		return nil
	}
	need := false
	for _, site := range cfg.Sites {
		if site.Kind != "drpy" || site.Unsupported {
			continue
		}
		need = true
		body, ok := cfg.Scripts[site.API]
		if !ok || len(body) == 0 {
			return errors.New("缓存缺少已确认的 JavaScript 站点脚本，请刷新订阅")
		}
		if err := verifyCachedDeps(site.API, string(body), cfg.Scripts, 0); err != nil {
			return err
		}
		var extStr string
		if json.Unmarshal([]byte(absolutizeExtJSON(site.API, site.Ext)), &extStr) == nil {
			extStr = strings.TrimSpace(extStr)
			if shouldPrefetchExt(site.API, extStr) {
				if b, ok := cfg.Scripts[extStr]; !ok || len(b) == 0 {
					return errors.New("缓存缺少已确认的 JavaScript 扩展规则，请刷新订阅")
				}
			}
		}
	}
	if !need {
		cfg.ScriptSHA256 = ""
		return nil
	}
	cfg.ScriptSHA256 = ComputeScriptSHA256(cfg.Scripts)
	return nil
}

func verifyCachedDeps(baseURL, src string, scripts map[string][]byte, depth int) error {
	if depth > 4 {
		return errors.New("脚本依赖嵌套过深")
	}
	rest := src
	for {
		spec, _, next, ok := cutImport(rest)
		if !ok {
			break
		}
		if _, builtin := builtinModuleSpec(spec); builtin {
			rest = next
			continue
		}
		abs, err := resolveAsset(baseURL, spec)
		if err != nil {
			return err
		}
		body, ok := scripts[abs]
		if !ok || len(body) == 0 {
			return errors.New("缓存缺少已确认的 JavaScript 依赖模块，请刷新订阅")
		}
		if err := verifyCachedDeps(abs, string(body), scripts, depth+1); err != nil {
			return err
		}
		rest = next
	}
	return nil
}

// ComputeScriptSHA256 returns a deterministic SHA-256 digest over all URL->bytes
// entries in scripts.
func ComputeScriptSHA256(scripts map[string][]byte) string {
	if len(scripts) == 0 {
		return ""
	}
	urls := make([]string, 0, len(scripts))
	for u := range scripts {
		urls = append(urls, u)
	}
	sort.Strings(urls)
	h := sha256.New()
	_, _ = h.Write([]byte("jstv-tvbox-scripts-v1\n"))
	for _, u := range urls {
		sum := sha256.Sum256(scripts[u])
		_, _ = h.Write([]byte(u + ":" + hex.EncodeToString(sum[:]) + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

func shouldPrefetchExt(api, extStr string) bool {
	extStr = strings.TrimSpace(extStr)
	if !strings.HasPrefix(extStr, "http://") && !strings.HasPrefix(extStr, "https://") {
		return false
	}
	lower := strings.ToLower(extStr)
	if strings.HasSuffix(lower, ".js") || strings.HasSuffix(lower, ".json") ||
		strings.HasSuffix(lower, ".txt") || strings.HasSuffix(lower, ".mjs") ||
		strings.HasSuffix(lower, ".cjs") {
		return true
	}
	apiLower := strings.ToLower(api)
	if strings.Contains(apiLower, "drpy") && !strings.HasSuffix(extStr, "/") &&
		!strings.Contains(lower, "/api.php") && !strings.Contains(lower, "/provide/vod") {
		return true
	}
	return false
}

func fetchScriptAsset(ctx context.Context, opt Options, client *http.Client, raw string, headers map[string]string) ([]byte, error) {
	if opt.PinnedScripts != nil {
		if b, ok := opt.PinnedScripts[raw]; ok && len(b) > 0 {
			return b, nil
		}
	}
	if opt.LockScripts {
		return nil, errors.New("未获信任确认的远程脚本资源已被拒绝")
	}
	return fetchURLHeaders(ctx, client, raw, headers)
}

func sanitizeScript(src string) string {
	src = strings.ReplaceAll(src, "let d = [];", "var d = [];")
	src = strings.ReplaceAll(src, "let d=[];", "var d=[];")
	src = strings.ReplaceAll(src, "let d = []", "var d = []")
	src = strings.ReplaceAll(src, "const TYPE =", "var TYPE =")
	src = strings.ReplaceAll(src, "const MY_PAGE =", "var MY_PAGE =")
	src = strings.ReplaceAll(src, "const MY_CATE =", "var MY_CATE =")
	return src
}

func (e *qjsEngine) prepareSpider(ctx context.Context, baseURL, src string) (string, error) {
	src = sanitizeScript(src)
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
	rest, _ = stripNamedExports(rest)
	return prelude.String() + rest, nil
}

func importBinding(names, global string) string {
	names = strings.TrimSpace(names)
	if names == "" || global == "" {
		return ""
	}
	if strings.HasPrefix(names, "{") {
		return "const " + convertImportDestructuring(names) + " = globalThis." + global + ";"
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

func convertImportDestructuring(clause string) string {
	start := strings.IndexByte(clause, '{')
	end := strings.LastIndexByte(clause, '}')
	if start < 0 || end <= start {
		return clause
	}
	var parts []string
	for _, item := range strings.Split(clause[start+1:end], ",") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if orig, alias, ok := strings.Cut(item, " as "); ok {
			parts = append(parts, strings.TrimSpace(orig)+": "+strings.TrimSpace(alias))
		} else {
			parts = append(parts, item)
		}
	}
	return "{ " + strings.Join(parts, ", ") + " }"
}

func (e *qjsEngine) evalDependency(ctx context.Context, baseURL, spec string, seen map[string]bool, depth int) (string, error) {
	if depth > 4 {
		return "", errors.New("脚本依赖嵌套过深")
	}
	if global, ok := builtinModuleSpec(spec); ok {
		if !seen[global] {
			seen[global] = true
			if _, err := e.vm.Eval(qjsCatShim+"\n;0", quickjs.EvalGlobal); err != nil {
				return "", fmt.Errorf("%s: %w", path.Base(spec), err)
			}
		}
		return global, nil
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
	body, err := fetchScriptAsset(ctx, e.opt, e.client, abs, nil)
	if err != nil {
		return "", fmt.Errorf("%s: %w", path.Base(spec), err)
	}
	text := sanitizeScript(string(body))
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

func localizeImports(ctx context.Context, opt Options, client *http.Client, dir, baseURL, src string) (string, error) {
	return rewriteLocalImports(ctx, opt, client, dir, baseURL, src, map[string]string{}, 0)
}

func rewriteLocalImports(ctx context.Context, opt Options, client *http.Client, dir, baseURL, src string, seen map[string]string, depth int) (string, error) {
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
		local, err := cacheImport(ctx, opt, client, dir, baseURL, spec, seen, depth)
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

func cacheImport(ctx context.Context, opt Options, client *http.Client, dir, baseURL, spec string, seen map[string]string, depth int) (string, error) {
	abs, err := resolveAsset(baseURL, spec)
	if err != nil {
		return "", err
	}
	if local, ok := seen[abs]; ok {
		return local, nil
	}
	body, err := fetchScriptAsset(ctx, opt, client, abs, nil)
	if err != nil {
		return "", err
	}
	name := safeModuleName(spec)
	seen[abs] = name
	text, err := rewriteLocalImports(ctx, opt, client, dir, abs, string(body), seen, depth+1)
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

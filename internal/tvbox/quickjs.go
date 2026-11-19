package tvbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
	"modernc.org/quickjs"
)

// qjsEngine evaluates a user-supplied spider inside pure-Go QuickJS.
// The spider file is loaded from the subscription. drpy2 itself is not
// shipped with the app. A script QuickJS cannot parse falls back to Node.
type qjsEngine struct {
	vm     *quickjs.VM
	client *http.Client
	dir    string
	mu     sync.Mutex
	store  map[string]string
}

func startQuickJS(ctx context.Context, opt Options, client *http.Client, src Source) (*qjsEngine, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		client = clientFor(opt)
	}
	body, err := fetchURL(ctx, client, src.API)
	if err != nil {
		return nil, err
	}
	dir := opt.Dir
	if dir == "" {
		dir, err = os.MkdirTemp("", "jstv-qjs-")
		if err != nil {
			return nil, err
		}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	vm, err := quickjs.NewVM()
	if err != nil {
		return nil, err
	}
	eng := &qjsEngine{vm: vm, client: client, dir: dir, store: map[string]string{}}
	if raw, err := os.ReadFile(eng.storePath()); err == nil {
		_ = json.Unmarshal(raw, &eng.store)
	}
	if err := eng.installHost(); err != nil {
		vm.Close()
		return nil, err
	}
	if _, err := vm.Eval(qjsPrelude+adaptSpider(string(body)), quickjs.EvalGlobal); err != nil {
		vm.Close()
		return nil, err
	}
	kind, err := vm.Eval(qjsBind, quickjs.EvalGlobal)
	if err != nil || kind != "ok" {
		vm.Close()
		if err == nil {
			err = errors.New("脚本没有导出 init/home/detail/play")
		}
		return nil, err
	}
	ext := "{}"
	if len(bytes.TrimSpace(src.Ext)) > 0 {
		ext = string(src.Ext)
	}
	if _, err := vm.Call("__init", ext); err != nil {
		vm.Close()
		return nil, err
	}
	return eng, nil
}

func (e *qjsEngine) installHost() error {
	if err := e.vm.RegisterFunc("__req", e.req, false); err != nil {
		return err
	}
	if err := e.vm.RegisterFunc("__joinUrl", joinURL, false); err != nil {
		return err
	}
	if err := e.vm.RegisterFunc("__pdfh", pdfh, false); err != nil {
		return err
	}
	if err := e.vm.RegisterFunc("__pdfa", pdfa, false); err != nil {
		return err
	}
	if err := e.vm.RegisterFunc("__pd", pd, false); err != nil {
		return err
	}
	if err := e.vm.RegisterFunc("__localGet", e.localGet, false); err != nil {
		return err
	}
	if err := e.vm.RegisterFunc("__localSet", e.localSet, false); err != nil {
		return err
	}
	return e.vm.RegisterFunc("__localDelete", e.localDelete, false)
}

func (e *qjsEngine) call(ctx context.Context, fn string, args map[string]any) (any, error) {
	if e == nil || e.vm == nil {
		return nil, errors.New("站点未启动")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			e.vm.Interrupt()
		case <-done:
		}
	}()
	defer close(done)
	payload, _ := json.Marshal(args)
	raw, err := e.vm.Call("__call", fn, string(payload))
	if err != nil {
		return nil, err
	}
	text, _ := raw.(string)
	return interpretDrpy(json.RawMessage(strconvQuote(text))), nil
}

func strconvQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (e *qjsEngine) stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.vm != nil {
		e.vm.Close()
		e.vm = nil
	}
}

func (e *qjsEngine) req(raw, spec string) string {
	var opt struct {
		Method  string            `json:"method"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
		Data    string            `json:"data"`
		Timeout int               `json:"timeout"`
	}
	_ = json.Unmarshal([]byte(spec), &opt)
	method := strings.ToUpper(strings.TrimSpace(opt.Method))
	if method == "" {
		method = http.MethodGet
	}
	body := opt.Body
	if body == "" {
		body = opt.Data
	}
	var reader io.Reader
	if body != "" && method != http.MethodGet {
		reader = strings.NewReader(body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(reqTimeout(opt.Timeout))*time.Millisecond)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, method, raw, reader)
	if err != nil {
		return reqResult(0, err.Error(), nil)
	}
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 JSTVPlayer")
	for k, v := range opt.Headers {
		if strings.TrimSpace(k) != "" && !strings.ContainsAny(k+v, "\r\n") {
			httpReq.Header.Set(k, v)
		}
	}
	resp, err := e.client.Do(httpReq)
	if err != nil {
		return reqResult(0, err.Error(), nil)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	headers := map[string]string{}
	for k, v := range resp.Header {
		if len(v) > 0 {
			headers[k] = v[0]
		}
	}
	return reqResult(resp.StatusCode, string(b), headers)
}

func reqTimeout(v int) int {
	if v <= 0 {
		return 20000
	}
	if v < 1000 {
		return v * 1000
	}
	return v
}

func reqResult(code int, content string, headers map[string]string) string {
	b, _ := json.Marshal(map[string]any{"code": code, "content": content, "headers": headers})
	return string(b)
}

func (e *qjsEngine) storePath() string { return filepath.Join(e.dir, "local.json") }

func (e *qjsEngine) localGet(key, fallback string) string {
	if v, ok := e.store[key]; ok {
		return v
	}
	return fallback
}

func (e *qjsEngine) localSet(key, value string) string {
	if e.store == nil {
		e.store = map[string]string{}
	}
	e.store[key] = value
	if b, err := json.Marshal(e.store); err == nil {
		_ = os.WriteFile(e.storePath(), b, 0o600)
	}
	return "ok"
}

func (e *qjsEngine) localDelete(key string) string {
	delete(e.store, key)
	if b, err := json.Marshal(e.store); err == nil {
		_ = os.WriteFile(e.storePath(), b, 0o600)
	}
	return "ok"
}

func joinURL(base, rel string) string {
	b, err := url.Parse(strings.TrimSpace(base))
	if err != nil {
		return rel
	}
	r, err := url.Parse(strings.TrimSpace(rel))
	if err != nil {
		return rel
	}
	return b.ResolveReference(r).String()
}

func pdfh(doc, expr string) string { return pickHTML(doc, expr, "text", true) }
func pdfa(doc, expr string) string {
	sel, mode := splitSelector(expr)
	if mode == "" {
		mode = "html"
	}
	return pickAll(doc, sel, mode)
}
func pd(doc, expr string) string {
	sel, mode := splitSelector(expr)
	if mode == "" || mode == "text" {
		mode = "href"
	}
	return firstMatch(doc, sel, mode)
}

func pickHTML(doc, expr, fallback string, first bool) string {
	sel, mode := splitSelector(expr)
	if mode == "" {
		mode = fallback
	}
	if first {
		return firstMatch(doc, sel, mode)
	}
	return pickAll(doc, sel, mode)
}

func splitSelector(expr string) (string, string) {
	parts := strings.Split(expr, "&&")
	if len(parts) == 1 {
		return strings.TrimSpace(expr), ""
	}
	last := strings.TrimSpace(parts[len(parts)-1])
	head := parts[:len(parts)-1]
	mode := ""
	switch strings.ToLower(last) {
	case "text", "html":
		mode = strings.ToLower(last)
	default:
		if last != "" && !strings.ContainsAny(last, ".#[]>: ") {
			mode = last
		} else {
			head = parts
		}
	}
	var css []string
	for _, part := range head {
		part = strings.TrimSpace(part)
		if part != "" {
			css = append(css, part)
		}
	}
	return strings.Join(css, " "), mode
}

func firstMatch(doc, selector, mode string) string {
	q, err := goquery.NewDocumentFromReader(strings.NewReader(doc))
	if err != nil {
		return ""
	}
	node := q.Find(selector).First()
	return nodeValue(node, mode)
}

func pickAll(doc, selector, mode string) string {
	q, err := goquery.NewDocumentFromReader(strings.NewReader(doc))
	if err != nil {
		return "[]"
	}
	out := []string{}
	q.Find(selector).Each(func(_ int, node *goquery.Selection) {
		if v := nodeValue(node, mode); v != "" {
			out = append(out, v)
		}
	})
	b, _ := json.Marshal(out)
	return string(b)
}

func nodeValue(node *goquery.Selection, mode string) string {
	if node == nil || node.Length() == 0 {
		return ""
	}
	switch strings.ToLower(mode) {
	case "", "text":
		return strings.TrimSpace(node.Text())
	case "html":
		html, _ := node.Html()
		return html
	default:
		v, _ := node.Attr(mode)
		if v == "" {
			v, _ = node.Attr(strings.ToLower(mode))
		}
		return v
	}
}

func adaptSpider(src string) string {
	var b strings.Builder
	for _, line := range strings.Split(src, "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "import ") {
			continue
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	out := b.String()
	out = strings.Replace(out, "export default", "globalThis.__drpy =", 1)
	return out
}

const qjsPrelude = `
function setTimeout(fn) { try { if (typeof fn === 'function') fn(); } catch (e) {} return 0; }
function setInterval() { return 0; }
function clearTimeout() {}
function clearInterval() {}
function req(url, obj) { return JSON.parse(__req(String(url), JSON.stringify(obj || {}))); }
function joinUrl(base, rel) { return __joinUrl(String(base || ''), String(rel || '')); }
function pdfh(html, expr) { return __pdfh(String(html || ''), String(expr || '')); }
function pdfa(html, expr) { return JSON.parse(__pdfa(String(html || ''), String(expr || '')) || '[]'); }
function pd(html, expr) { return __pd(String(html || ''), String(expr || '')); }
const local = {
  get(key, fallback) { return __localGet(String(key), fallback == null ? '' : String(fallback)); },
  set(key, value) { return __localSet(String(key), value == null ? '' : String(value)); },
  delete(key) { return __localDelete(String(key)); }
};
function __init(raw) {
  let ext = raw;
  try { ext = JSON.parse(raw); } catch (e) {}
  if (globalThis.__drpy && typeof globalThis.__drpy.init === 'function') globalThis.__drpy.init(ext);
}
function __call(fn, payload) {
  const body = JSON.parse(payload || '{}');
  const eng = globalThis.__drpy;
  let result;
  switch (fn) {
    case 'home': result = eng.home(!!body.filter); break;
    case 'homeVod': result = typeof eng.homeVod === 'function' ? eng.homeVod() : ''; break;
    case 'category': result = eng.category(String(body.tid ?? ''), body.pg, body.filter, body.extend || {}); break;
    case 'detail': result = eng.detail(String(body.id ?? '')); break;
    case 'search': result = eng.search(String(body.wd ?? ''), !!body.quick, body.pg || 1); break;
    case 'play': result = eng.play(String(body.flag ?? ''), String(body.id ?? ''), body.flags || []); break;
    default: throw new Error('未知调用 ' + fn);
  }
  if (typeof result === 'string') return result;
  return JSON.stringify(result === undefined ? null : result);
}
`

const qjsBind = `
(() => {
  if (globalThis.__drpy && typeof globalThis.__drpy.init === 'function') return 'ok';
  if (typeof init === 'function' && typeof home === 'function') {
    globalThis.__drpy = { init, home, homeVod, category, detail, search, play };
    return 'ok';
  }
  return 'missing';
})()
`

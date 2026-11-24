package tvbox

import (
	"bytes"
	"context"
	"encoding/base64"
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

	"github.com/freelxs/jstv-player/internal/catvod"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/transform"
	"modernc.org/quickjs"
)

// qjsEngine evaluates a user-supplied spider inside pure-Go QuickJS.
// The spider file is loaded from the subscription. drpy2 itself is not
// shipped with the app. A script QuickJS cannot parse falls back to Node.
type qjsEngine struct {
	vm              *quickjs.VM
	client          *http.Client
	reqClient       *http.Client
	opt             Options
	allowLAN        bool
	allowedLoopback string
	dir             string
	mu              sync.Mutex
	store           map[string]string
}

func startQuickJS(ctx context.Context, opt Options, client *http.Client, src Source) (*qjsEngine, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if client == nil {
		client = clientFor(opt)
	}
	body, err := fetchScriptAsset(ctx, opt, client, src.API, src.Header)
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
	allowedLoopback := opt.AllowedLoopbackAddr
	if !opt.RestrictLAN && allowedLoopback == "" {
		if u, err := url.Parse(src.API); err == nil {
			allowedLoopback = catvod.AllowedLoopbackAddr(u)
		}
	}
	reqClient := RestrictedHTTPClient(opt.Hosts, 20*time.Second, opt.AllowLAN, allowedLoopback)
	eng := &qjsEngine{
		vm:              vm,
		client:          client,
		reqClient:       reqClient,
		opt:             opt,
		allowLAN:        opt.AllowLAN,
		allowedLoopback: allowedLoopback,
		dir:             dir,
		store:           map[string]string{},
	}
	if raw, err := os.ReadFile(eng.storePath()); err == nil {
		_ = json.Unmarshal(raw, &eng.store)
	}
	if err := eng.installHost(); err != nil {
		vm.Close()
		return nil, err
	}
	if _, err := vm.Eval(qjsPrelude, quickjs.EvalGlobal); err != nil {
		vm.Close()
		return nil, err
	}
	prepared, err := eng.prepareSpider(ctx, src.API, string(body))
	if err != nil {
		vm.Close()
		return nil, err
	}
	if _, err := vm.Eval(prepared+"\n;0", quickjs.EvalGlobal); err != nil {
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
	ext := absolutizeExtJSON(src.API, src.Ext)
	if _, err := eng.callJS(ctx, "__init", ext, src.Key, src.Type); err != nil {
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
	if err := e.vm.RegisterFunc("__pd", pdWithBase, false); err != nil {
		return err
	}
	if err := e.vm.RegisterFunc("__localGet", e.localGet, false); err != nil {
		return err
	}
	if err := e.vm.RegisterFunc("__localSet", e.localSet, false); err != nil {
		return err
	}
	if err := e.vm.RegisterFunc("__cryptoHash", cryptoHashHost, false); err != nil {
		return err
	}
	if err := e.vm.RegisterFunc("__cryptoAES", cryptoAESHost, false); err != nil {
		return err
	}
	if err := e.vm.RegisterFunc("__b64EncodeHex", b64EncodeHexHost, false); err != nil {
		return err
	}
	if err := e.vm.RegisterFunc("__b64DecodeHex", b64DecodeHexHost, false); err != nil {
		return err
	}
	return e.vm.RegisterFunc("__localDelete", e.localDelete, false)
}

func (e *qjsEngine) callJS(ctx context.Context, fn string, args ...any) (string, error) {
	vm := e.vm
	if ctx == nil {
		ctx = context.Background()
	}
	done := make(chan struct{})
	go func() {
		defer func() { _ = recover() }()
		select {
		case <-ctx.Done():
			select {
			case <-done:
			default:
				vm.Interrupt()
			}
		case <-done:
		}
	}()
	defer close(done)
	raw, err := vm.Call(fn, args...)
	if err != nil {
		return "", err
	}
	text, _ := raw.(string)
	if text != "__PROMISE__" {
		return text, nil
	}
	for {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		n, err := vm.ExecutePendingJobs()
		if err != nil {
			return "", err
		}
		if n == 0 {
			break
		}
	}
	finRaw, err := vm.Call("__asyncFinish")
	if err != nil {
		return "", err
	}
	var state struct {
		Done  bool   `json:"done"`
		OK    bool   `json:"ok"`
		Value string `json:"value"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(scalar(finRaw)), &state); err != nil {
		return "", err
	}
	if !state.Done {
		return "", errors.New("异步调用未完成")
	}
	if !state.OK {
		if state.Error == "" {
			state.Error = "站点异步调用失败"
		}
		return "", errors.New(state.Error)
	}
	return state.Value, nil
}

func (e *qjsEngine) call(ctx context.Context, fn string, args map[string]any) (any, error) {
	if e == nil || e.vm == nil {
		return nil, errors.New("站点未启动")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	payload, _ := json.Marshal(args)
	text, err := e.callJS(ctx, "__call", fn, string(payload))
	if err != nil {
		return nil, err
	}
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
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return reqResult(0, "", nil)
	}
	if !e.allowLAN && catvod.AllowedLoopbackAddr(u) != e.allowedLoopback && catvod.IsLocalNetworkURL(u.String()) {
		return reqResult(0, "", nil)
	}
	var opt struct {
		Method   string          `json:"method"`
		Headers  map[string]any  `json:"headers"`
		Body     json.RawMessage `json:"body"`
		Data     json.RawMessage `json:"data"`
		Timeout  flexInt         `json:"timeout"`
		Redirect any             `json:"redirect"`
		Buffer   flexInt         `json:"buffer"`
		PostType string          `json:"postType"`
	}
	_ = json.Unmarshal([]byte(spec), &opt)
	method := strings.ToUpper(strings.TrimSpace(opt.Method))
	if method == "" {
		method = http.MethodGet
	}
	if method == http.MethodGet && e.opt.PinnedScripts != nil {
		if cached, ok := e.opt.PinnedScripts[raw]; ok && len(cached) > 0 {
			return reqResult(http.StatusOK, string(cached), map[string]string{"content-type": "application/javascript"})
		}
		if unescaped, err := url.PathUnescape(raw); err == nil && unescaped != raw {
			if cached, ok := e.opt.PinnedScripts[unescaped]; ok && len(cached) > 0 {
				return reqResult(http.StatusOK, string(cached), map[string]string{"content-type": "application/javascript"})
			}
		}
	}
	reqHeaders := map[string]string{}
	reqCharset := ""
	contentType := ""
	for k, v := range opt.Headers {
		val := scalar(v)
		if strings.TrimSpace(k) == "" || strings.ContainsAny(k+val, "\r\n") {
			continue
		}
		reqHeaders[k] = val
		if strings.EqualFold(k, "Content-Type") {
			contentType = val
			reqCharset = extractCharset(val)
		}
	}
	body, inferredCT := encodeReqBody(opt.Body, opt.Data, contentType, opt.PostType)
	if inferredCT != "" && contentType == "" {
		reqHeaders["Content-Type"] = inferredCT
	}
	var reader io.Reader
	if body != "" && method != http.MethodGet && method != http.MethodHead {
		reader = strings.NewReader(body)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(reqTimeout(opt.Timeout.N))*time.Millisecond)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, method, raw, reader)
	if err != nil {
		return reqResult(0, "", nil)
	}
	httpReq.Header.Set("User-Agent", "Mozilla/5.0 JSTVPlayer")
	for k, v := range reqHeaders {
		if method == http.MethodGet && strings.EqualFold(k, "Content-Type") && strings.HasPrefix(strings.ToLower(v), "text/html") {
			continue
		}
		httpReq.Header.Set(k, v)
	}
	httpClient := e.reqClient
	if httpClient == nil {
		httpClient = RestrictedHTTPClient(e.opt.Hosts, 20*time.Second, e.allowLAN, e.allowedLoopback)
	}
	if isRedirectDisabled(opt.Redirect) {
		cloned := *httpClient
		cloned.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
		httpClient = &cloned
	}
	resp, err := httpClient.Do(httpReq)
	if err != nil {
		return reqResult(0, "", nil)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	headers := map[string]string{}
	for k, v := range resp.Header {
		if len(v) > 0 {
			lowerKey := strings.ToLower(k)
			if lowerKey == "set-cookie" && len(v) > 1 {
				headers[lowerKey] = strings.Join(v, "; ")
			} else {
				headers[lowerKey] = v[0]
			}
		}
	}
	if opt.Buffer.N == 2 {
		return reqResult(resp.StatusCode, base64.StdEncoding.EncodeToString(b), headers)
	}
	respCharset := extractCharset(headers["content-type"])
	if respCharset == "" {
		respCharset = reqCharset
	}
	if respCharset == "" {
		respCharset = sniffHTMLCharset(b)
	}
	return reqResult(resp.StatusCode, decodeBodyCharset(b, respCharset), headers)
}

func encodeReqBody(rawBody, rawData json.RawMessage, contentType, postType string) (string, string) {
	raw := bytes.TrimSpace(rawBody)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		raw = bytes.TrimSpace(rawData)
	}
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", ""
	}
	var str string
	if json.Unmarshal(raw, &str) == nil {
		return str, ""
	}
	if strings.Contains(strings.ToLower(contentType), "application/json") || strings.EqualFold(postType, "json") {
		return string(raw), "application/json"
	}
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		vals := url.Values{}
		for k, v := range obj {
			vals.Set(k, scalar(v))
		}
		return vals.Encode(), "application/x-www-form-urlencoded"
	}
	return string(raw), ""
}

func isRedirectDisabled(v any) bool {
	switch t := v.(type) {
	case bool:
		return !t
	case float64:
		return t == 0
	case int:
		return t == 0
	case string:
		s := strings.TrimSpace(strings.ToLower(t))
		return s == "0" || s == "false"
	default:
		return false
	}
}

func extractCharset(ct string) string {
	lower := strings.ToLower(ct)
	idx := strings.Index(lower, "charset=")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(lower[idx+len("charset="):])
	if semi := strings.IndexAny(rest, "; \t\r\n"); semi >= 0 {
		rest = rest[:semi]
	}
	return strings.Trim(rest, `"'`)
}

func sniffHTMLCharset(b []byte) string {
	head := b
	if len(head) > 2048 {
		head = head[:2048]
	}
	lower := strings.ToLower(string(head))
	idx := strings.Index(lower, "charset=")
	if idx < 0 {
		return ""
	}
	rest := strings.TrimSpace(lower[idx+len("charset="):])
	if end := strings.IndexAny(rest, `"' ;/>`); end >= 0 {
		rest = rest[:end]
	}
	return strings.Trim(rest, `"'`)
}

func decodeBodyCharset(b []byte, charset string) string {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "gbk", "gb2312", "gb18030", "gb_2312", "gb_2312-80":
		if decoded, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), b); err == nil {
			return string(decoded)
		}
	case "big5":
		if decoded, _, err := transform.Bytes(traditionalchinese.Big5.NewDecoder(), b); err == nil {
			return string(decoded)
		}
	}
	return string(b)
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
	if headers == nil {
		headers = map[string]string{}
	}
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
function print() {}
var console = globalThis.console || { log: print, error: print, info: print, warn: print, debug: print };
function req(url, obj) { return JSON.parse(__req(String(url), JSON.stringify(obj || {}))); }
function joinUrl(base, rel) { return __joinUrl(String(base || ''), String(rel || '')); }
function pdfh(html, expr) { return __pdfh(String(html || ''), String(expr || '')); }
function pdfa(html, expr) { return JSON.parse(__pdfa(String(html || ''), String(expr || '')) || '[]'); }
function pd(html, expr, base) { return __pd(String(html || ''), String(expr || ''), String(base || '')); }
const local = {
  get(scope, key) {
    if (arguments.length >= 2) {
      const scoped = __localGet(String(scope) + '\0' + String(key), '\0__MISS__');
      if (scoped !== '\0__MISS__') return scoped;
      const plain = __localGet(String(scope), '\0__MISS__');
      if (plain !== '\0__MISS__') return plain;
      return '';
    }
    return __localGet(String(scope), '');
  },
  set(scope, key, value) {
    if (arguments.length >= 3) {
      return __localSet(String(scope) + '\0' + String(key), value == null ? '' : String(value));
    }
    return __localSet(String(scope), key == null ? '' : String(key));
  },
  delete(scope, key) {
    if (arguments.length >= 2) {
      return __localDelete(String(scope) + '\0' + String(key));
    }
    return __localDelete(String(scope));
  }
};
globalThis.__asyncState = { done: false, ok: false, value: '', error: '' };
function __wrapResult(result) {
  if (result && typeof result.then === 'function') {
    globalThis.__asyncState = { done: false, ok: false, value: '', error: '' };
    result.then(
      (v) => { globalThis.__asyncState = { done: true, ok: true, value: typeof v === 'string' ? v : JSON.stringify(v === undefined ? null : v), error: '' }; },
      (e) => { globalThis.__asyncState = { done: true, ok: false, value: '', error: String(e && e.message || e) }; }
    );
    return '__PROMISE__';
  }
  if (typeof result === 'string') return result;
  return JSON.stringify(result === undefined ? null : result);
}
function __asyncFinish() { return JSON.stringify(globalThis.__asyncState); }
function __init(raw, skey, stype) {
  let ext = raw;
  try { ext = JSON.parse(raw); } catch (e) {}
  if (!globalThis.__drpy || typeof globalThis.__drpy.init !== 'function') return '';
  const arg = globalThis.__catvodInit ? { skey: skey || '', stype: stype || 3, ext: ext == null ? '' : ext } : ext;
  return __wrapResult(globalThis.__drpy.init(arg));
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
  return __wrapResult(result);
}
`

const qjsBind = `
(() => {
  if (globalThis.__drpy && typeof globalThis.__drpy.init === 'function') return 'ok';
  if (typeof __jsEvalReturn === 'function') {
    const exp = __jsEvalReturn();
    if (exp && typeof exp === 'object') {
      globalThis.__drpy = exp;
      globalThis.__catvodInit = true;
      return 'ok';
    }
  }
  if (typeof init === 'function' && typeof home === 'function') {
    globalThis.__drpy = { init, home, homeVod, category, detail, search, play };
    return 'ok';
  }
  return 'missing';
})()
`

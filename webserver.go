package main

import (
	"bufio"
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"reflect"
	"strings"
	"time"
)

//go:embed webassets
var webAssets embed.FS

func webModeEnabled() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("JSTV_MODE")), "web") {
		return true
	}
	for _, arg := range os.Args[1:] {
		if arg == "--web" || arg == "-web" {
			return true
		}
	}
	return false
}

func runWeb() error {
	dir := os.Getenv("JSTV_DATA_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir = filepathJoin(home, ".local", "share", "jstv-player")
	}
	user := strings.TrimSpace(os.Getenv("JSTV_WEB_USER"))
	password := os.Getenv("JSTV_WEB_PASSWORD")
	reset := envOn("JSTV_WEB_RESET_PASSWORD")
	secure := envOn("JSTV_WEB_SECURE")
	auth, err := openAuth(dir, user, password, reset, secure)
	if err != nil {
		return err
	}
	if reset {
		log.Printf("web: replaced saved login for %q and revoked sessions", auth.file.Username)
	} else {
		log.Printf("web: login user %q", auth.file.Username)
	}
	p := newPlayer(dir)
	p.webMode.Store(true)
	addr := strings.TrimSpace(os.Getenv("JSTV_WEB_ADDR"))
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           newWebHandler(p, auth),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("JSTV Player web listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	select {
	case err := <-errCh:
		p.StopSource()
		return err
	case <-shutdownNotify():
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		p.StopSource()
		return nil
	}
}

func envOn(name string) bool {
	v := strings.TrimSpace(os.Getenv(name))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

func filepathJoin(elem ...string) string {
	return strings.Join(elem, string(os.PathSeparator))
}

type webServer struct {
	player  *Player
	auth    *authStore
	media   *http.Client
	posters *http.Client
	methods map[string]reflect.Method
}

func newWebHandler(p *Player, auth *authStore) http.Handler {
	s := &webServer{
		player:  p,
		auth:    auth,
		media:   newMediaClient(),
		posters: &http.Client{Timeout: 20 * time.Second},
		methods: playerMethods(),
	}
	s.posters.Transport = s.posterTransport()
	s.posters.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return errors.New("bad scheme")
		}
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /login", s.serveLogin)
	mux.HandleFunc("GET /login.js", s.serveAsset("webassets/login.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /login.css", s.serveAsset("webassets/login.css", "text/css; charset=utf-8"))
	mux.HandleFunc("POST /api/login", s.handleLogin)
	protected := http.NewServeMux()
	protected.HandleFunc("POST /api/logout", s.handleLogout)
	protected.HandleFunc("POST /api/call", s.handleCall)
	protected.HandleFunc("POST /api/stream", s.handleStream)
	protected.HandleFunc("GET /poster", s.servePoster)
	protected.Handle("GET /media/", http.HandlerFunc(s.serveMedia))
	protected.Handle("HEAD /media/", http.HandlerFunc(s.serveMedia))
	protected.HandleFunc("GET /bridge.js", s.serveAsset("webassets/bridge.js", "text/javascript; charset=utf-8"))
	protected.HandleFunc("GET /web-player.js", s.serveAsset("webassets/web-player.js", "text/javascript; charset=utf-8"))
	protected.HandleFunc("GET /vendor/hls.min.js", s.serveAsset("webassets/hls.min.js", "text/javascript; charset=utf-8"))
	protected.Handle("GET /", http.HandlerFunc(s.serveFrontend))
	mux.Handle("/", s.requireAuth(protected))
	return securityHeaders(mux)
}

func newMediaClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConns:          32,
		},
		// The loopback proxy follows upstream redirects itself.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; media-src 'self' blob:; connect-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; worker-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func (s *webServer) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.auth.sessionUser(r); !ok {
			if r.Method == http.MethodGet && wantsHTML(r) {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未登录"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func (s *webServer) serveLogin(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.auth.sessionUser(r); ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(loginPage)
}

func (s *webServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !jsonMutation(r) {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "需要 JSON 请求"})
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, 8<<10, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求无效"})
		return
	}
	token, err := s.auth.login(clientIP(r), body.Username, body.Password)
	if err != nil {
		status := http.StatusUnauthorized
		if err.Error() == "尝试次数过多，请稍后再试" {
			status = http.StatusTooManyRequests
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	s.auth.setCookie(w, token)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *webServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !jsonMutation(r) {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "需要 JSON 请求"})
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.auth.logout(c.Value)
	}
	s.auth.clearCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func jsonMutation(r *http.Request) bool {
	if r.Header.Get("X-JSTV-Request") != "1" {
		return false
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	return strings.HasPrefix(ct, "application/json")
}

type rpcRequest struct {
	Method string            `json:"method"`
	Args   []json.RawMessage `json:"args"`
}

func (s *webServer) handleCall(w http.ResponseWriter, r *http.Request) {
	if !jsonMutation(r) {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "需要 JSON 请求"})
		return
	}
	var req rpcRequest
	if err := decodeJSON(w, r, 4<<20, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求无效"})
		return
	}
	name := strings.TrimPrefix(req.Method, "Player.")
	if name == "SearchAll" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "搜索全部站点请使用流式接口"})
		return
	}
	result, err := s.invoke(r.Context(), name, req.Args)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"result": result})
}

func (s *webServer) handleStream(w http.ResponseWriter, r *http.Request) {
	if !jsonMutation(r) {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "需要 JSON 请求"})
		return
	}
	var req rpcRequest
	if err := decodeJSON(w, r, 64<<10, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求无效"})
		return
	}
	if strings.TrimPrefix(req.Method, "Player.") != "SearchAll" || len(req.Args) < 1 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "未知的流式调用"})
		return
	}
	var wd string
	if err := json.Unmarshal(req.Args[0], &wd); err != nil || strings.TrimSpace(wd) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请输入搜索词"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "服务器不支持流式输出"})
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	enc := json.NewEncoder(w)
	err := s.player.streamSearch(r.Context(), wd, func(result SiteResults) error {
		if err := enc.Encode(map[string]any{"type": "item", "data": result}); err != nil {
			return err
		}
		flusher.Flush()
		return nil
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		_ = enc.Encode(map[string]any{"type": "error", "error": err.Error()})
		flusher.Flush()
		return
	}
	_ = enc.Encode(map[string]any{"type": "done"})
	flusher.Flush()
}

func (s *webServer) invoke(ctx context.Context, name string, args []json.RawMessage) (any, error) {
	m, ok := s.methods[name]
	if !ok {
		return nil, errors.New("未知接口")
	}
	mt := m.Type
	in := make([]reflect.Value, 0, mt.NumIn())
	in = append(in, reflect.ValueOf(s.player))
	argi := 0
	for i := 1; i < mt.NumIn(); i++ {
		pt := mt.In(i)
		if pt == reflect.TypeOf((*context.Context)(nil)).Elem() {
			if ctx == nil {
				ctx = context.Background()
			}
			in = append(in, reflect.ValueOf(ctx))
			continue
		}
		if argi >= len(args) {
			return nil, errors.New("参数不足")
		}
		v := reflect.New(pt)
		if err := json.Unmarshal(args[argi], v.Interface()); err != nil {
			return nil, errors.New("参数无效")
		}
		in = append(in, v.Elem())
		argi++
	}
	if argi != len(args) {
		return nil, errors.New("参数过多")
	}
	out := m.Func.Call(in)
	var result any
	for _, v := range out {
		if v.Type().Implements(reflect.TypeOf((*error)(nil)).Elem()) {
			if !v.IsNil() {
				return nil, v.Interface().(error)
			}
			continue
		}
		if result != nil {
			return nil, errors.New("接口返回值无法编码")
		}
		if v.Kind() == reflect.Pointer && v.IsNil() {
			result = nil
			continue
		}
		result = v.Interface()
	}
	return result, nil
}

func playerMethods() map[string]reflect.Method {
	allow := map[string]bool{
		"Logs": true, "ClearLogs": true, "GetSettings": true, "SaveSettings": true,
		"AddSubscription": true, "UpdateSubscription": true, "RemoveSubscription": true,
		"SwitchSubscription": true, "LoadSource": true, "ApproveSource": true, "StopSource": true,
		"ProbeNodePermissions": true, "Home": true, "Category": true, "Search": true,
		"Detail": true, "GetHistory": true, "Open": true, "PlayEpisodes": true,
		"PickCustomPlayer": true, "WebPlay": true, "WebAdvance": true,
	}
	out := map[string]reflect.Method{}
	typ := reflect.TypeOf(&Player{})
	for i := 0; i < typ.NumMethod(); i++ {
		m := typ.Method(i)
		if allow[m.Name] {
			out[m.Name] = m
		}
	}
	return out
}

func (s *webServer) serveMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if s.player.proxy == nil {
		http.Error(w, "播放代理未启动", http.StatusBadGateway)
		return
	}
	base := strings.TrimRight(s.player.proxy.BaseURL(), "/")
	rest := strings.TrimPrefix(r.URL.Path, "/media")
	if base == "" || !strings.HasPrefix(rest, "/p/") || strings.Contains(rest, "..") {
		http.NotFound(w, r)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, base+rest, nil)
	if err != nil {
		http.Error(w, "播放地址无效", http.StatusBadGateway)
		return
	}
	if rg := r.Header.Get("Range"); rg != "" {
		req.Header.Set("Range", rg)
	}
	resp, err := s.media.Do(req)
	if err != nil {
		http.Error(w, "播放代理不可用", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
		http.Error(w, "播放地址已失效，请重新点播", resp.StatusCode)
		return
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "mpegurl") && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent) {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if err != nil {
			http.Error(w, "播放列表读取失败", http.StatusBadGateway)
			return
		}
		body = rewriteMediaBody(base, body)
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "private, no-store")
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
		return
	}
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, resp.Body)
	}
}

func rewriteMediaBody(proxyBase string, body []byte) []byte {
	proxyBase = strings.TrimRight(proxyBase, "/")
	if proxyBase == "" {
		return body
	}
	return bytes.ReplaceAll(body, []byte(proxyBase), []byte("/media"))
}

func (s *webServer) servePoster(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("u")
	if err := validatePosterURL(raw, s.player.pluginPosterHost()); err != nil {
		http.Error(w, "拒绝加载该图片", http.StatusForbidden)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, raw, nil)
	if err != nil {
		http.Error(w, "拒绝加载该图片", http.StatusForbidden)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
	resp, err := s.posters.Do(req)
	if err != nil {
		http.Error(w, "图片不可用", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		http.Error(w, "图片不可用", http.StatusBadGateway)
		return
	}
	buf := bufio.NewReader(io.LimitReader(resp.Body, 8<<20))
	sniff, _ := buf.Peek(32)
	ct := imageContentType(resp.Header.Get("Content-Type"), sniff)
	if ct == "" {
		http.Error(w, "不是图片", http.StatusUnsupportedMediaType)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, buf)
}

func validatePosterURL(raw, pluginHost string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return errors.New("bad url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("bad scheme")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		if u.Host != pluginHost {
			return errors.New("blocked")
		}
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && blockedPosterIP(ip) && u.Host != pluginHost {
		return errors.New("blocked")
	}
	return nil
}

func (s *webServer) posterTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if allow := s.player.pluginPosterHost(); allow != "" && net.JoinHostPort(host, port) == allow {
				return dialer.DialContext(ctx, network, addr)
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil || len(ips) == 0 {
				if err == nil {
					err = errors.New("no address")
				}
				return nil, err
			}
			for _, ip := range ips {
				if blockedPosterIP(ip.IP) {
					return nil, errors.New("blocked address")
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
		ResponseHeaderTimeout: 15 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          8,
	}
}

func blockedPosterIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast()
}

func imageContentType(header string, sniff []byte) string {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(header, ";")[0]))
	switch {
	case bytes.HasPrefix(sniff, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	case bytes.HasPrefix(sniff, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}):
		return "image/png"
	case bytes.HasPrefix(sniff, []byte("GIF87a")) || bytes.HasPrefix(sniff, []byte("GIF89a")):
		return "image/gif"
	case len(sniff) >= 12 && bytes.HasPrefix(sniff, []byte("RIFF")) && bytes.Equal(sniff[8:12], []byte("WEBP")):
		return "image/webp"
	case ct == "image/jpeg" || ct == "image/png" || ct == "image/gif" || ct == "image/webp":
		return ct
	default:
		return ""
	}
}

func (s *webServer) serveFrontend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	clean := path.Clean("/" + r.URL.Path)
	if clean == "/" || clean == "/index.html" {
		s.serveIndex(w, r)
		return
	}
	if strings.Contains(clean, "..") || strings.HasPrefix(path.Base(clean), ".") {
		http.NotFound(w, r)
		return
	}
	ext := strings.ToLower(path.Ext(clean))
	switch ext {
	case ".html", ".js", ".css", ".png", ".svg", ".ico", ".webp":
	default:
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(clean, ".test.cjs") || strings.HasSuffix(clean, ".md") {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(clean, "/")
	b, err := fs.ReadFile(webFS, path.Join("web", name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType(ext))
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method != http.MethodHead {
		_, _ = w.Write(b)
	}
}

func (s *webServer) serveIndex(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(webFS, "web/index.html")
	if err != nil {
		http.Error(w, "界面缺失", http.StatusInternalServerError)
		return
	}
	b = injectWebScripts(b)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodHead {
		_, _ = w.Write(b)
	}
}

func injectWebScripts(html []byte) []byte {
	html = bytes.Replace(html,
		[]byte(`<script src="request-token.js"></script>`),
		[]byte("<script src=\"/bridge.js\"></script>\n<script src=\"request-token.js\"></script>"),
		1)
	html = bytes.Replace(html,
		[]byte(`<script src="app.js"></script>`),
		[]byte("<script src=\"app.js\"></script>\n<script src=\"/web-player.js\"></script>"),
		1)
	return html
}

func (s *webServer) serveAsset(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(webAssets, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	}
}

func contentType(ext string) string {
	switch ext {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	default:
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
		return "application/octet-stream"
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, dest any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

var loginPage = []byte(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>登录 · JSTV Player</title>
<link rel="stylesheet" href="/login.css">
</head>
<body>
<main class="login">
  <div class="mark" aria-hidden="true"></div>
  <h1>JSTV Player</h1>
  <p class="lead">网页模式需要登录后才能浏览和播放。</p>
  <form id="login-form">
    <label>用户名<input name="username" autocomplete="username" required maxlength="64"></label>
    <label>密码<input name="password" type="password" autocomplete="current-password" required maxlength="72"></label>
    <button type="submit">登录</button>
    <p id="login-error" class="error" role="alert"></p>
  </form>
</main>
<script src="/login.js"></script>
</body>
</html>
`)

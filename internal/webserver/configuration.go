package webserver

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"
)

type configurationTicket struct {
	sessionID  string
	generation uint64
	expires    time.Time
}

type configurationBridge struct {
	mu      sync.Mutex
	tickets map[string]configurationTicket
}

func (s *webServer) serveConfiguration(w http.ResponseWriter, r *http.Request) {
	base, generation, _ := s.runtime.ConfigurationTarget()
	if base == "" {
		http.Error(w, "请先加载 JS 插件", 409)
		return
	}
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		http.Error(w, "未登录", 401)
		return
	}
	token, err := newSessionToken()
	if err != nil {
		http.Error(w, "无法创建配置会话", 500)
		return
	}
	s.configuration.mu.Lock()
	if s.configuration.tickets == nil {
		s.configuration.tickets = map[string]configurationTicket{}
	}
	for key, ticket := range s.configuration.tickets {
		if time.Now().After(ticket.expires) {
			delete(s.configuration.tickets, key)
		}
	}
	if len(s.configuration.tickets) >= 128 {
		s.configuration.mu.Unlock()
		http.Error(w, "配置会话过多，请稍后重试", 429)
		return
	}
	s.configuration.tickets[token] = configurationTicket{sessionID: hashToken(cookie.Value), generation: generation, expires: time.Now().Add(30 * time.Minute)}
	s.configuration.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-src 'self'; style-src 'unsafe-inline'; frame-ancestors 'none'")
	_, _ = fmt.Fprintf(w, `<!doctype html><html lang="zh"><meta charset="utf-8"><title>插件配置</title><style>html,body{margin:0;height:100%%;font:14px system-ui;background:#18181b;color:#fafafa}header{height:40px;display:flex;align-items:center;padding:0 12px;gap:20px}a{color:#a5c9ff}iframe{width:100%%;height:calc(100%% - 40px);border:0;background:white}</style><header>插件配置 · 当前订阅<a href="/">返回播放器并刷新站点</a><span>配置会话 30 分钟后过期</span></header><iframe title="插件配置" sandbox="allow-scripts allow-forms allow-downloads allow-popups" referrerpolicy="no-referrer" src="%s"></iframe></html>`, html.EscapeString("/plugin-configuration/"+token+"/website/index.html"))
}

func (s *webServer) configurationAuthorized(ticket configurationTicket) bool {
	s.auth.mu.Lock()
	defer s.auth.mu.Unlock()
	session := s.auth.sessions[ticket.sessionID]
	now := time.Now()
	return session != nil && session.User == s.auth.file.Username && now.Before(session.Expires) && now.Before(session.Created.Add(sessionAbsolute)) && now.Before(ticket.expires)
}

func (s *webServer) servePluginConfiguration(w http.ResponseWriter, r *http.Request) {
	parts := strings.SplitN(strings.TrimPrefix(r.URL.Path, "/plugin-configuration/"), "/", 2)
	if len(parts) != 2 {
		http.NotFound(w, r)
		return
	}
	s.configuration.mu.Lock()
	ticket, ok := s.configuration.tickets[parts[0]]
	s.configuration.mu.Unlock()
	base, generation, sourceCtx := s.runtime.ConfigurationTarget()
	if !ok || base == "" || generation != ticket.generation || !s.configurationAuthorized(ticket) {
		http.Error(w, "配置会话已过期，请从播放器重新打开", 401)
		return
	}
	upstreamPath := "/" + parts[1]
	if strings.ContainsAny(upstreamPath, "\\\x00") || path.Clean(upstreamPath) != strings.TrimSuffix(upstreamPath, "/") && upstreamPath != "/website/" {
		http.NotFound(w, r)
		return
	}
	method := r.Method
	if method == http.MethodOptions {
		method = r.Header.Get("Access-Control-Request-Method")
	}
	if !configurationRoute(method, upstreamPath) {
		http.Error(w, "此配置功能未开放给网页模式", 403)
		return
	}
	// Opaque-origin sandbox requests carry a scoped bearer capability, never
	// the app's session cookie. A normal external Origin cannot use this bridge.
	origin := r.Header.Get("Origin")
	if origin != "" && origin != "null" {
		http.Error(w, "配置来源无效", 403)
		return
	}
	w.Header().Set("Access-Control-Allow-Origin", "null")
	w.Header().Set("Vary", "Origin")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Requested-With")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Del("X-Frame-Options")
	w.Header().Set("Content-Security-Policy", "sandbox allow-scripts allow-forms allow-downloads allow-popups; default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' https: data: blob:; connect-src 'self'; font-src 'self' data:; frame-src 'none'; frame-ancestors 'self'; base-uri 'self'; form-action 'self'")
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}
	if method == http.MethodPost && !strings.HasPrefix(strings.ToLower(r.Header.Get("Content-Type")), "application/json") {
		http.Error(w, "配置保存需要 JSON", 415)
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stop := context.AfterFunc(sourceCtx, cancel)
	defer stop()
	target, err := url.Parse(base)
	if err != nil {
		http.Error(w, "插件地址无效", 502)
		return
	}
	target.Path = upstreamPath
	target.RawQuery = r.URL.RawQuery
	req, err := http.NewRequestWithContext(ctx, method, target.String(), http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil {
		http.Error(w, "配置请求无效", 400)
		return
	}
	for _, name := range []string{"Accept", "Content-Type"} {
		req.Header.Set(name, r.Header.Get(name))
	}
	// Authentication cookies and client Authorization never reach the plugin.
	resp, err := s.media.Do(req)
	if err != nil {
		http.Error(w, "配置请求失败，请重试", 502)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 && resp.StatusCode < 400 {
		http.Error(w, "配置重定向已阻止", 502)
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil || len(body) > 8<<20 {
		http.Error(w, "配置响应无效或过大", 502)
		return
	}
	if _, current, _ := s.runtime.ConfigurationTarget(); current != generation {
		http.Error(w, "订阅已切换", 409)
		return
	}
	if method == http.MethodPost && resp.StatusCode >= 200 && resp.StatusCode < 300 {
		s.runtime.InvalidateConfiguration(generation)
	}
	prefix := "/plugin-configuration/" + parts[0]
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/html") || strings.Contains(contentType, "javascript") || strings.Contains(contentType, "text/css") {
		body = bytes.ReplaceAll(body, []byte("/website"), []byte(prefix+"/website"))
		if strings.Contains(contentType, "text/html") {
			body = injectConfigurationBridge(body, prefix)
		}
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(resp.StatusCode)
	if method != http.MethodHead {
		_, _ = w.Write(body)
	}
}

// Only data/configuration routes inspected in the reference bundle are exposed.
// No playground, script upload, filesystem, database or arbitrary proxy routes.
func configurationRoute(method, path string) bool {
	if method == http.MethodHead {
		method = http.MethodGet
	}
	if method != http.MethodGet && method != http.MethodPost {
		return false
	}
	if method == http.MethodGet {
		for _, p := range []string{"/website/", "/website", "/website/index.html", "/website/assets/theme.css", "/website/assets/common.css", "/website/assets/common.js", "/website/assets/qrcode.min.js"} {
			if path == p {
				return true
			}
		}
		for _, name := range []string{"sources", "live", "youtube", "webdav", "playback", "proxy"} {
			if path == "/website/"+name || path == "/website/"+name+".html" {
				return true
			}
		}
	}
	if path == "/website/sources" || path == "/website/live" {
		return true
	}
	if method == http.MethodPost && (path == "/website/sources/reset" || path == "/website/proxy/config" || path == "/website/proxy/test") {
		return true
	}
	if strings.HasPrefix(path, "/website/config/") {
		return strings.Count(path, "/") == 3 && len(path) < 180
	}
	if method == http.MethodGet && strings.HasPrefix(path, "/website/code/") {
		return strings.Count(path, "/") == 3 && len(path) < 180
	}
	get := []string{"youtube/api/start", "youtube/api/poll", "youtube/api/status", "youtube/api/config", "youtube/api/channels", "youtube/api/channel/search", "youtube/api/potcheck", "webdav/api/list", "playback/login/providers", "playback/m3u8-settings", "playback/magnet", "playback/magnet/folders", "playback/magnet/quota", "playback/pan", "playback/relay-exempt", "proxy"}
	post := []string{"youtube/api/channels", "youtube/api/poturl", "youtube/api/priority", "youtube/api/sabr", "youtube/api/watcher", "webdav/api/save", "webdav/api/delete", "webdav/api/test", "playback/apply", "playback/login/start", "playback/login/cancel", "playback/login/poll", "playback/login/pikpak-password", "playback/login/tianyi-password", "playback/m3u8-settings", "playback/magnet", "playback/magnet/clear", "playback/pan", "playback/relay-exempt"}
	allowed := get
	if method == http.MethodPost {
		allowed = post
	}
	for _, route := range allowed {
		if path == "/website/"+route {
			return true
		}
	}
	return false
}

func injectConfigurationBridge(body []byte, prefix string) []byte {
	// A small in-memory Storage substitute avoids opaque-origin SecurityErrors.
	// Configuration values remain persisted by the plugin's HTTP APIs.
	script := fmt.Sprintf(`<script>(()=>{const prefix=%q;const mapURL=(u)=>{const s=String(u);return s.startsWith('/website')?prefix+s:s};const nativeFetch=window.fetch.bind(window);window.fetch=(input,init)=>nativeFetch(typeof input==='string'?mapURL(input):input,{...init,credentials:'omit'});const open=XMLHttpRequest.prototype.open;XMLHttpRequest.prototype.open=function(method,u,...rest){return open.call(this,method,mapURL(u),...rest)};for(const key of ['localStorage','sessionStorage']){const data=new Map();try{Object.defineProperty(window,key,{value:{getItem:k=>data.has(String(k))?data.get(String(k)):null,setItem:(k,v)=>data.set(String(k),String(v)),removeItem:k=>data.delete(String(k)),clear:()=>data.clear(),key:i=>Array.from(data.keys())[i]||null,get length(){return data.size}}})}catch(e){}}})();</script>`, prefix)
	if index := bytes.Index(bytes.ToLower(body), []byte("<head>")); index >= 0 {
		return append(append(append([]byte{}, body[:index+6]...), []byte(script)...), body[index+6:]...)
	}
	return append([]byte(script), body...)
}

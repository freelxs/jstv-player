package tvbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/freelxs/jstv-player/internal/catvod"
)

// Server speaks the CatPawOpen site protocol for one TVBox subscription.
type Server struct {
	cfg    *Config
	sites  []Source
	client *http.Client
	opt    Options

	mu      sync.Mutex
	ln      net.Listener
	engines map[string]*drpyEngine
	lives   map[int][]Channel
	srv     *http.Server
}

// Options configures the local adapter.
type Options struct {
	Node       string
	Dir        string
	Log        func(string)
	InheritEnv bool
	HTTP       *http.Client
}

// NewServer prepares the adapter. JavaScript sites stay disabled when Node
// is missing so the rest of the subscription can still load.
func NewServer(cfg *Config, opt Options) *Server {
	sites := append([]Source(nil), cfg.Sites...)
	if strings.TrimSpace(opt.Node) == "" {
		for i := range sites {
			if sites[i].Kind == "drpy" {
				sites[i].Unsupported = true
				sites[i].Reason = "这个站点是 JavaScript（drpy），但没有找到 Node.js 18 或更新版本"
			}
		}
	}
	if len(cfg.Lives) > 0 {
		zero := 0
		sites = append(sites, Source{
			Key: "live", Name: "直播", Type: 1, Kind: "live", LocalAPI: "/tvbox/live",
			Searchable: &zero,
		})
	}
	client := opt.HTTP
	if client == nil {
		client = HTTPClient(cfg.Hosts, 25*time.Second)
	}
	return &Server{cfg: cfg, sites: sites, client: client, opt: opt, engines: map[string]*drpyEngine{}, lives: map[int][]Channel{}}
}

// Sites is the catalog exposed to the player, including unsupported entries.
func (s *Server) Sites() []catvod.Site {
	out := make([]catvod.Site, 0, len(s.sites))
	for _, src := range s.sites {
		out = append(out, catvod.Site{
			Key: src.Key, Name: src.Name, Type: src.Type, API: src.LocalAPI,
			Searchable: src.Searchable, QuickSearch: src.QuickSearch, Filterable: src.Filterable,
			Unsupported: src.Unsupported, Reason: src.Reason,
		})
	}
	return out
}

// Start listens on 127.0.0.1 and a free port.
func (s *Server) Start() error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.ln = ln
	s.srv = &http.Server{Handler: http.HandlerFunc(s.route)}
	s.mu.Unlock()
	go func() { _ = s.srv.Serve(ln) }()
	return nil
}

// Base is the loopback origin, or "" after Stop.
func (s *Server) Base() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return ""
	}
	return "http://" + s.ln.Addr().String()
}

// Stop closes the listener and any drpy processes.
func (s *Server) Stop() {
	s.mu.Lock()
	ln := s.ln
	srv := s.srv
	s.ln = nil
	engines := s.engines
	s.engines = map[string]*drpyEngine{}
	s.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}
	if ln != nil {
		_ = ln.Close()
	}
	for _, e := range engines {
		e.stop()
	}
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/config" && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"video": map[string]any{"sites": s.Sites()}})
		return
	}
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/tvbox/") {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/tvbox/")
	id, action, ok := strings.Cut(rest, "/")
	if !ok || action == "" {
		http.NotFound(w, r)
		return
	}
	var body map[string]any
	if r.Body != nil {
		b, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if len(b) > 0 {
			_ = json.Unmarshal(b, &body)
		}
	}
	if id == "live" {
		out, code, err := s.live(r.Context(), action, body)
		if err != nil {
			writeJSON(w, code, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(id, "s"))
	if err != nil || idx < 0 || idx >= len(s.cfg.Sites) {
		http.NotFound(w, r)
		return
	}
	src := s.sites[idx]
	if src.Unsupported {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": src.Reason})
		return
	}
	out, code, err := s.dispatch(r.Context(), src, action, body)
	if err != nil {
		writeJSON(w, code, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) dispatch(ctx context.Context, src Source, action string, body map[string]any) (any, int, error) {
	switch src.Kind {
	case "cms-xml", "cms-json":
		return s.cms(ctx, src, action, body)
	case "drpy":
		return s.drpy(ctx, src, action, body)
	default:
		return nil, http.StatusNotImplemented, errors.New(src.Reason)
	}
}

func (s *Server) cms(ctx context.Context, src Source, action string, body map[string]any) (any, int, error) {
	c := newCMS(src, s.client)
	switch action {
	case "init":
		return map[string]any{}, http.StatusOK, nil
	case "home":
		out, err := c.home(ctx)
		return out, http.StatusBadGateway, err
	case "category":
		filters := stringMap(body["filters"])
		out, err := c.category(ctx, scalar(body["id"]), positive(body["page"]), filters)
		return out, http.StatusBadGateway, err
	case "detail":
		out, err := c.detail(ctx, scalar(body["id"]))
		return out, http.StatusBadGateway, err
	case "search":
		out, err := c.search(ctx, scalar(body["wd"]), positive(body["page"]))
		return out, http.StatusBadGateway, err
	case "play":
		return c.play(scalar(body["id"])), http.StatusOK, nil
	default:
		return nil, http.StatusNotFound, errors.New("未知操作")
	}
}

func (s *Server) drpy(ctx context.Context, src Source, action string, body map[string]any) (any, int, error) {
	if action == "init" {
		return map[string]any{}, http.StatusOK, nil
	}
	eng, err := s.engine(ctx, src)
	if err != nil {
		return nil, http.StatusBadGateway, err
	}
	switch action {
	case "home":
		home, err := eng.call(ctx, "home", map[string]any{"filter": true})
		if err != nil {
			return nil, http.StatusBadGateway, err
		}
		obj := asMap(home)
		if vod, err := eng.call(ctx, "homeVod", map[string]any{}); err == nil {
			if list, ok := asMap(vod)["list"].([]any); ok && len(list) > 0 {
				if cur, ok := obj["list"].([]any); !ok || len(cur) == 0 {
					obj["list"] = list
				}
			}
		}
		return obj, http.StatusOK, nil
	case "category":
		out, err := eng.call(ctx, "category", map[string]any{
			"tid": scalar(body["id"]), "pg": positive(body["page"]), "filter": true, "extend": stringMap(body["filters"]),
		})
		return out, http.StatusBadGateway, err
	case "detail":
		out, err := eng.call(ctx, "detail", map[string]any{"id": scalar(body["id"])})
		return out, http.StatusBadGateway, err
	case "search":
		out, err := eng.call(ctx, "search", map[string]any{"wd": scalar(body["wd"]), "quick": false, "pg": positive(body["page"])})
		return out, http.StatusBadGateway, err
	case "play":
		out, err := eng.call(ctx, "play", map[string]any{"flag": scalar(body["flag"]), "id": scalar(body["id"]), "flags": []any{}})
		if err != nil {
			return nil, http.StatusBadGateway, err
		}
		return coercePlay(asMap(out), src.Header), http.StatusOK, nil
	default:
		return nil, http.StatusNotFound, errors.New("未知操作")
	}
}

func (s *Server) engine(ctx context.Context, src Source) (*drpyEngine, error) {
	s.mu.Lock()
	if e := s.engines[src.Key]; e != nil {
		s.mu.Unlock()
		return e, nil
	}
	s.mu.Unlock()
	e, err := startDrpy(ctx, s.opt, s.cfg.Hosts, src)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		e.stop()
		return nil, errors.New("订阅已停止")
	}
	if cur := s.engines[src.Key]; cur != nil {
		e.stop()
		return cur, nil
	}
	s.engines[src.Key] = e
	return e, nil
}

func (s *Server) live(ctx context.Context, action string, body map[string]any) (any, int, error) {
	switch action {
	case "init":
		return map[string]any{}, http.StatusOK, nil
	case "home":
		var class []any
		for i, l := range s.cfg.Lives {
			class = append(class, map[string]any{"type_id": strconv.Itoa(i), "type_name": l.Name})
		}
		return map[string]any{"class": class}, http.StatusOK, nil
	case "category":
		idx, err := strconv.Atoi(scalar(body["id"]))
		if err != nil || idx < 0 || idx >= len(s.cfg.Lives) {
			return nil, http.StatusBadRequest, errors.New("没有这个直播列表")
		}
		chs, err := s.channels(ctx, idx)
		if err != nil {
			return nil, http.StatusBadGateway, err
		}
		var list []any
		for i, ch := range chs {
			list = append(list, map[string]any{
				"vod_id": "ch:" + strconv.Itoa(idx) + ":" + strconv.Itoa(i), "vod_name": ch.Name,
				"vod_pic": ch.Logo, "vod_remarks": ch.Group,
			})
		}
		return map[string]any{"page": 1, "pagecount": 1, "limit": len(list), "total": len(list), "list": list}, http.StatusOK, nil
	case "detail":
		idx, n, err := parseChannelID(scalar(body["id"]))
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		chs, err := s.channels(ctx, idx)
		if err != nil {
			return nil, http.StatusBadGateway, err
		}
		if n < 0 || n >= len(chs) {
			return nil, http.StatusBadRequest, errors.New("频道不存在")
		}
		ch := chs[n]
		id := scalar(body["id"])
		return map[string]any{"list": []any{map[string]any{
			"vod_id": id, "vod_name": ch.Name, "vod_pic": ch.Logo, "vod_remarks": ch.Group,
			"vod_play_from": "直播", "vod_play_url": "播放$" + id,
		}}}, http.StatusOK, nil
	case "play":
		idx, n, err := parseChannelID(scalar(body["id"]))
		if err != nil {
			return nil, http.StatusBadRequest, err
		}
		chs, err := s.channels(ctx, idx)
		if err != nil {
			return nil, http.StatusBadGateway, err
		}
		if n < 0 || n >= len(chs) {
			return nil, http.StatusBadRequest, errors.New("频道不存在")
		}
		return coercePlay(map[string]any{"parse": 0, "url": chs[n].URL}, chs[n].Headers), http.StatusOK, nil
	case "search":
		return map[string]any{"page": 1, "pagecount": 1, "list": []any{}}, http.StatusOK, nil
	default:
		return nil, http.StatusNotFound, errors.New("未知操作")
	}
}

func (s *Server) channels(ctx context.Context, idx int) ([]Channel, error) {
	s.mu.Lock()
	if chs, ok := s.lives[idx]; ok {
		s.mu.Unlock()
		return chs, nil
	}
	s.mu.Unlock()
	src := s.cfg.Lives[idx]
	if isDeviceProxy(src.URL) {
		return nil, errors.New("这个直播列表指向本机 9978 端口，桌面和网页模式没有这个代理")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 JSTVPlayer")
	for k, v := range src.Headers {
		req.Header.Set(k, v)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, errors.New("直播列表 HTTP " + strconv.Itoa(resp.StatusCode))
	}
	chs, err := ParsePlaylist(string(body), src.URL, src.Headers)
	if err != nil {
		return nil, err
	}
	if len(chs) > 2000 {
		chs = chs[:2000]
	}
	s.mu.Lock()
	if s.ln != nil {
		s.lives[idx] = chs
	}
	s.mu.Unlock()
	return chs, nil
}

func parseChannelID(id string) (int, int, error) {
	parts := strings.Split(id, ":")
	if len(parts) != 3 || parts[0] != "ch" {
		return 0, 0, errors.New("频道不存在")
	}
	a, err1 := strconv.Atoi(parts[1])
	b, err2 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil {
		return 0, 0, errors.New("频道不存在")
	}
	return a, b, nil
}

func isDeviceProxy(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	if h != "127.0.0.1" && h != "localhost" && h != "::1" {
		return false
	}
	return u.Port() == "9978"
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func stringMap(v any) map[string]string {
	out := map[string]string{}
	m, ok := v.(map[string]any)
	if !ok {
		return out
	}
	for k, val := range m {
		out[k] = scalar(val)
	}
	return out
}

func positive(v any) int {
	n, _ := strconv.Atoi(scalar(v))
	if n < 1 {
		return 1
	}
	return n
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func coercePlay(m map[string]any, extra map[string]string) map[string]any {
	if m == nil {
		m = map[string]any{}
	}
	if hs, ok := m["header"].(string); ok && strings.TrimSpace(hs) != "" {
		var parsed map[string]any
		if json.Unmarshal([]byte(hs), &parsed) == nil {
			m["header"] = parsed
		}
	}
	h, _ := m["header"].(map[string]any)
	if h == nil {
		h = map[string]any{}
	}
	for k, v := range extra {
		if _, ok := h[k]; !ok {
			h[k] = v
		}
	}
	if len(h) > 0 {
		m["header"] = h
	}
	if _, ok := m["parse"]; !ok {
		m["parse"] = 0
	}
	if u := scalar(m["url"]); directMedia(u) {
		m["parse"] = 0
	}
	return m
}

func directMedia(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	p := strings.ToLower(u.Path)
	for _, ext := range []string{".m3u8", ".mp4", ".m4v", ".flv", ".mkv", ".webm", ".mp3", ".m4a", ".aac", ".ts", ".mpd"} {
		if strings.HasSuffix(p, ext) {
			return true
		}
	}
	return false
}

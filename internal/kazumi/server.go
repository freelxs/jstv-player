package kazumi

import (
	"context"
	"encoding/json"
	"fmt"
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

// Server exposes a Kazumi rule bundle through the local CatVod site protocol.
type Server struct {
	bundle  *Bundle
	engine  *Engine
	bangumi *BangumiClient

	mu     sync.Mutex
	ln     net.Listener
	srv    *http.Server
	meta   map[string]PageMeta
	order  []string
}

// Options configures a local Kazumi adapter server.
type Options struct {
	HTTP    *http.Client
	Log     func(string)
	Bangumi *BangumiClient
}

// NewServer creates a local HTTP adapter for one Kazumi rule bundle.
func NewServer(bundle *Bundle, opt Options) *Server {
	bgm := opt.Bangumi
	if bgm == nil {
		bgm = &BangumiClient{HTTP: opt.HTTP}
	}
	return &Server{
		bundle:  bundle,
		engine:  &Engine{Client: opt.HTTP, Log: opt.Log},
		bangumi: bgm,
		meta:    map[string]PageMeta{},
	}
}

// Sites returns every rule in the bundle as a selectable site.
func (s *Server) Sites() []catvod.Site {
	if s.bundle == nil {
		return nil
	}
	one, zero := 1, 0
	out := make([]catvod.Site, 0, len(s.bundle.Rules)+1)
	if len(s.bundle.Rules) > 0 {
		out = append(out, catvod.Site{
			Key:         "kazumi:catalog",
			Name:        "番剧目录",
			Type:        3,
			API:         "/kazumi/catalog",
			Searchable:  &zero,
			QuickSearch: &zero,
			Filterable:  &one,
		})
	}
	for i, rule := range s.bundle.Rules {
		out = append(out, catvod.Site{
			Key:         "kazumi:" + rule.Name,
			Name:        rule.Name,
			Type:        3,
			API:         fmt.Sprintf("/kazumi/r%d", i),
			Searchable:  &one,
			QuickSearch: &one,
			Filterable:  &one,
		})
	}
	return out
}

// Start binds to 127.0.0.1 on an ephemeral port.
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

// Base returns the loopback origin, or "" after Stop.
func (s *Server) Base() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ln == nil {
		return ""
	}
	return "http://" + s.ln.Addr().String()
}

// Stop shuts down the local HTTP server.
func (s *Server) Stop() {
	s.mu.Lock()
	ln := s.ln
	srv := s.srv
	s.ln = nil
	s.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = srv.Shutdown(ctx)
		cancel()
	}
	if ln != nil {
		_ = ln.Close()
	}
}

func (s *Server) rememberMeta(ruleIdx int, item SearchItem) {
	key := strconv.Itoa(ruleIdx) + "\x00" + item.Src
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.meta[key]; !exists {
		s.order = append(s.order, key)
		if len(s.order) > 2000 {
			evict := s.order[0]
			s.order = s.order[1:]
			delete(s.meta, evict)
		}
	}
	s.meta[key] = PageMeta{
		Title:    item.Name,
		Pic:      item.Pic,
		Content:  item.Content,
		Year:     item.Year,
		Area:     item.Area,
		TypeName: item.TypeName,
		Actor:    item.Actor,
		Director: item.Director,
		Remarks:  item.Remarks,
	}
}

func (s *Server) lookupMeta(ruleIdx int, src string) PageMeta {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta[strconv.Itoa(ruleIdx)+"\x00"+src]
}

func (s *Server) route(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/config" && r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{"video": map[string]any{"sites": s.Sites()}})
		return
	}
	if r.Method != http.MethodPost || !strings.HasPrefix(r.URL.Path, "/kazumi/") {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/kazumi/")
	idPart, action, ok := strings.Cut(rest, "/")
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
	if idPart == "catalog" {
		out, code, err := s.dispatchCatalog(r.Context(), action, body, "msearch:bgm:")
		if err != nil {
			writeJSON(w, code, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(idPart, "r"))
	if err != nil || s.bundle == nil || idx < 0 || idx >= len(s.bundle.Rules) {
		http.NotFound(w, r)
		return
	}
	rule := s.bundle.Rules[idx]
	out, code, err := s.dispatch(r.Context(), idx, rule, action, body)
	if err != nil {
		writeJSON(w, code, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) dispatchCatalog(ctx context.Context, action string, body map[string]any, prefix string) (any, int, error) {
	switch action {
	case "init":
		return map[string]any{}, http.StatusOK, nil
	case "home":
		return s.bangumi.HomePayload(), http.StatusOK, nil
	case "category":
		tid, page, filters := parseCategoryBody(body)
		out, err := s.bangumi.CategoryPage(ctx, tid, page, filters, prefix)
		if err != nil {
			return nil, http.StatusBadGateway, err
		}
		return out, http.StatusOK, nil
	default:
		return nil, http.StatusNotFound, fmt.Errorf("不支持的操作: %s", action)
	}
}

func parseCategoryBody(body map[string]any) (string, int, map[string]string) {
	tid := ""
	page := 1
	filters := map[string]string{}
	if body == nil {
		return tid, page, filters
	}
	if id, ok := body["id"].(string); ok {
		tid = strings.TrimSpace(id)
	}
	switch pg := body["page"].(type) {
	case float64:
		if int(pg) > 0 {
			page = int(pg)
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(pg)); err == nil && n > 0 {
			page = n
		}
	}
	if rawFilters, ok := body["filters"].(map[string]any); ok {
		for k, v := range rawFilters {
			if vs := toStringValue(v); vs != "" {
				filters[k] = vs
			}
		}
	}
	return tid, page, filters
}

func (s *Server) dispatch(ctx context.Context, idx int, rule Rule, action string, body map[string]any) (any, int, error) {
	switch action {
	case "init":
		return map[string]any{}, http.StatusOK, nil
	case "home":
		home := s.bangumi.HomePayload()
		home["kazumi"] = map[string]any{
			"name":        rule.Name,
			"version":     string(rule.Version),
			"baseUrl":     rule.BaseURL,
			"searchMode":  rule.NormalizedSearchMode(),
			"chapterMode": rule.NormalizedChapterMode(),
			"antiCrawler": rule.AntiCrawlerConfig != nil && rule.AntiCrawlerConfig.Enabled,
		}
		return home, http.StatusOK, nil
	case "category":
		tid, page, filters := parseCategoryBody(body)
		out, err := s.bangumi.CategoryPage(ctx, tid, page, filters, "ssearch:bgm:")
		if err != nil {
			return nil, http.StatusBadGateway, err
		}
		return out, http.StatusOK, nil
	case "search":
		wd, _ := body["wd"].(string)
		items, err := s.engine.Search(ctx, rule, wd)
		if err != nil {
			return nil, http.StatusBadGateway, err
		}
		list := make([]any, 0, len(items))
		for _, item := range items {
			s.rememberMeta(idx, item)
			list = append(list, map[string]any{
				"vod_id":      item.Src,
				"vod_name":    item.Name,
				"vod_pic":     item.Pic,
				"vod_remarks": nonEmpty(item.Remarks, rule.Name),
				"type_name":   nonEmpty(item.TypeName, rule.Type),
				"vod_year":    item.Year,
			})
		}
		return map[string]any{
			"page":      1,
			"pagecount": 1,
			"limit":     len(list),
			"total":     len(list),
			"list":      list,
		}, http.StatusOK, nil
	case "detail":
		id := extractDetailID(body)
		meta := s.lookupMeta(idx, id)
		var (
			bgmCh   chan *BangumiItem
			bgmItem *BangumiItem
		)
		if meta.Title != "" && s.shouldLookupBangumi(rule) {
			bgmCh = make(chan *BangumiItem, 1)
			go func(title string) {
				bgmCh <- s.lookupBangumi(ctx, title)
			}(meta.Title)
		}
		roads, pageMeta, err := s.engine.Chapters(ctx, rule, id)
		if err != nil {
			return nil, http.StatusBadGateway, err
		}
		if len(roads) == 0 {
			return nil, http.StatusBadGateway, fmt.Errorf("规则 %s 未解析到任何播放线路或剧集", rule.Name)
		}
		if bgmCh != nil {
			bgmItem = <-bgmCh
		} else if pageMeta.Title != "" && s.shouldLookupBangumi(rule) {
			bgmItem = s.lookupBangumi(ctx, pageMeta.Title)
		}
		var bgmName, bgmPic, bgmSummary, bgmYear, bgmArea, bgmType, bgmActor, bgmDirector string
		var bgmScore float64
		if bgmItem != nil {
			bgmName = bgmItem.Name
			bgmPic = bgmItem.Pic
			bgmSummary = bgmItem.Summary
			bgmYear = bgmItem.Year
			bgmArea = bgmItem.Area
			bgmType = formatBangumiTypeName(*bgmItem)
			bgmActor = bgmItem.Actor
			bgmDirector = bgmItem.Director
			bgmScore = bgmItem.Score
		}
		name := firstNonEmpty(meta.Title, pageMeta.Title, bgmName, id)
		pic := firstNonEmpty(meta.Pic, pageMeta.Pic, bgmPic)
		content := pickBestDescription(pageMeta.Content, meta.Content, bgmSummary)
		year := firstNonEmpty(pageMeta.Year, meta.Year, bgmYear)
		area := firstNonEmpty(pageMeta.Area, meta.Area, bgmArea)
		actor := firstNonEmpty(pageMeta.Actor, meta.Actor, bgmActor)
		director := firstNonEmpty(pageMeta.Director, meta.Director, bgmDirector)
		typeName := firstNonEmpty(pageMeta.TypeName, meta.TypeName, bgmType, rule.Type)
		remarks := buildDetailRemarks(rule.Name, firstNonEmpty(pageMeta.Remarks, meta.Remarks), bgmScore)
		fromParts := make([]string, 0, len(roads))
		urlParts := make([]string, 0, len(roads))
		for _, road := range roads {
			fromParts = append(fromParts, cleanRoadName(road.Name))
			epParts := make([]string, 0, len(road.Episodes))
			for _, ep := range road.Episodes {
				epParts = append(epParts, cleanEpisodeLabel(ep.Name)+"$"+ep.URL)
			}
			urlParts = append(urlParts, strings.Join(epParts, "#"))
		}
		vod := map[string]any{
			"vod_id":        id,
			"vod_name":      name,
			"vod_pic":       pic,
			"vod_content":   content,
			"vod_year":      year,
			"vod_area":      area,
			"vod_actor":     actor,
			"vod_director":  director,
			"vod_remarks":   remarks,
			"type_name":     typeName,
			"vod_play_from": strings.Join(fromParts, "$$$"),
			"vod_play_url":  strings.Join(urlParts, "$$$"),
		}
		return map[string]any{"list": []any{vod}}, http.StatusOK, nil
	case "play":
		epURL, _ := body["id"].(string)
		epURL = NormalizeEpisodeURL(rule.BaseURL, epURL)
		if epURL == "" {
			return nil, http.StatusBadRequest, fmt.Errorf("缺少播放地址")
		}
		headers := map[string]string{}
		if ref := strings.TrimSpace(rule.Referer); ref != "" {
			headers["Referer"] = ref
		}
		if ua := strings.TrimSpace(rule.UserAgent); ua != "" {
			headers["User-Agent"] = ua
		}
		parse := 1
		if isDirectMediaURL(epURL) {
			parse = 0
		}
		out := map[string]any{
			"parse": parse,
			"url":   epURL,
		}
		if len(headers) > 0 {
			out["header"] = headers
		}
		return out, http.StatusOK, nil
	default:
		return nil, http.StatusNotFound, fmt.Errorf("不支持的操作: %s", action)
	}
}

func extractDetailID(body map[string]any) string {
	if body == nil {
		return ""
	}
	if id, ok := body["id"].(string); ok && strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id)
	}
	if ids, ok := body["ids"].([]any); ok && len(ids) > 0 {
		if id, ok := ids[0].(string); ok {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

func cleanRoadName(name string) string {
	name = strings.ReplaceAll(strings.TrimSpace(name), "$$$", "-")
	if name == "" {
		return "播放线路"
	}
	return name
}

func cleanEpisodeLabel(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "$", " ")
	name = strings.ReplaceAll(name, "#", " ")
	name = strings.TrimSpace(name)
	if name == "" {
		return "播放"
	}
	return name
}

func isDirectMediaURL(raw string) bool {
	lower := strings.ToLower(strings.Split(raw, "?")[0])
	for _, ext := range []string{".m3u8", ".mp4", ".m4v", ".flv", ".mkv", ".webm", ".mpd"} {
		if strings.HasSuffix(lower, ext) {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) shouldLookupBangumi(rule Rule) bool {
	if s.bangumi == nil {
		return false
	}
	if strings.TrimSpace(s.bangumi.APIBaseURL) != "" {
		return true
	}
	return !isLoopbackBaseURL(rule.BaseURL)
}

func (s *Server) lookupBangumi(ctx context.Context, title string) *BangumiItem {
	if s.bangumi == nil || strings.TrimSpace(title) == "" {
		return nil
	}
	bgmCtx, cancel := context.WithTimeout(ctx, 2500*time.Millisecond)
	defer cancel()
	item, err := s.bangumi.LookupSubject(bgmCtx, title)
	if err != nil {
		return nil
	}
	return item
}

func isLoopbackBaseURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func pickBestDescription(pageDesc, searchDesc, bgmSummary string) string {
	pageDesc = strings.TrimSpace(pageDesc)
	searchDesc = strings.TrimSpace(searchDesc)
	bgmSummary = strings.TrimSpace(bgmSummary)
	if bgmSummary != "" {
		if pageDesc == "" || isSEOBoilerplateDescription(pageDesc) || len([]rune(bgmSummary)) >= 20 {
			return bgmSummary
		}
	}
	return firstNonEmpty(pageDesc, searchDesc, bgmSummary)
}

func formatBangumiTypeName(item BangumiItem) string {
	subtitle := strings.TrimSpace(item.Platform)
	if len(item.Tags) > 0 {
		limit := min(3, len(item.Tags))
		joined := strings.Join(item.Tags[:limit], " / ")
		if subtitle != "" {
			subtitle += " · " + joined
		} else {
			subtitle = joined
		}
	}
	return subtitle
}

func buildDetailRemarks(ruleName, note string, score float64) string {
	parts := []string{fmt.Sprintf("Kazumi · %s", ruleName)}
	if score > 0 {
		parts = append(parts, fmt.Sprintf("★ %.1f", score))
	}
	if note = strings.TrimSpace(note); note != "" && !strings.EqualFold(note, ruleName) {
		parts = append(parts, note)
	}
	return strings.Join(parts, " · ")
}

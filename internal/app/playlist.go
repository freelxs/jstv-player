package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/freelxs/jstv-player/internal/atomicfile"
	"github.com/freelxs/jstv-player/internal/catvod"
	"github.com/freelxs/jstv-player/internal/externalplayer"
)

// Continuous playback (连播): a show's remaining episodes are handed to the
// player as one local playlist. Every entry points at /ep/<id>/<n> on a
// loopback server, which asks the plugin for that episode only when the
// player reaches it (plugin URLs are often short-lived) and redirects to
// the proxied stream, so headers and HLS rewriting work as for one episode.

type playlist struct {
	generation             uint64
	api, vodID, show, flag string
	subID                  string // subscription that owns this show; empty uses the active one
	eps                    []catvod.Episode
	start                  int
	quality                string         // rendition name to prefer; empty uses the first playable URL
	first                  *catvod.Stream // already resolved start episode

	mu            sync.Mutex
	pending       map[int]*resolved // resolved or being resolved, by index
	hasFail       bool
	originFailIdx int
	lastFailIdx   int
	lastFailAt    time.Time
}

const externalFailCascadeWindow = 5 * time.Second

func (pl *playlist) isFailCascade(n int, now time.Time) (bool, int) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	if !pl.hasFail {
		return false, 0
	}
	if now.Sub(pl.lastFailAt) >= externalFailCascadeWindow || n <= pl.lastFailIdx {
		pl.hasFail = false
		return false, 0
	}
	pl.lastFailIdx = n
	pl.lastFailAt = now
	return true, pl.originFailIdx
}

func (pl *playlist) markFailed(n int, now time.Time) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	pl.hasFail = true
	pl.originFailIdx = n
	pl.lastFailIdx = n
	pl.lastFailAt = now
}

func (pl *playlist) clearFailed() {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	pl.hasFail = false
}

// resolved is one episode's plugin answer; done closes when it is known.
type resolved struct {
	done chan struct{}
	st   *catvod.Stream
	err  error
	at   time.Time
}

// resolvedTTL is how long a prefetched answer is trusted; plugin URLs are
// often signed and expire, so an old one is resolved again.
const resolvedTTL = 10 * time.Minute

// HistoryEntry is the last episode played of a show.
type HistoryEntry struct {
	Flag     string  `json:"flag"`
	Index    int     `json:"index"`
	Name     string  `json:"name"`
	Time     int64   `json:"time"`
	Position float64 `json:"position,omitempty"`
	Duration float64 `json:"duration,omitempty"`
	ShowName string  `json:"showName,omitempty"`
	Picture  string  `json:"picture,omitempty"`
	Watched  bool    `json:"watched,omitempty"`
}

type episodeServer struct {
	base string

	mu    sync.Mutex
	lists map[string]*playlist
	order []string
	seq   int
}

const maxPlaylists = 32

func (s *episodeServer) add(pl *playlist) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lists == nil {
		s.lists = map[string]*playlist{}
	}
	s.seq++
	id := strconv.FormatInt(time.Now().UnixNano()%1e9, 36) + strconv.Itoa(s.seq)
	s.lists[id] = pl
	s.order = append(s.order, id)
	if len(s.order) > maxPlaylists {
		delete(s.lists, s.order[0])
		s.order = s.order[1:]
	}
	return id
}

func (s *episodeServer) get(id string) *playlist {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lists[id]
}

func (s *episodeServer) epURL(id string, n int) string {
	return fmt.Sprintf("%s/ep/%s/%d", s.base, id, n)
}

func (s *episodeServer) playlistURL(id string) string {
	return s.base + "/playlist/" + id + ".m3u"
}

func entryTitle(show, ep string) string {
	t := strings.TrimSpace(show + " - " + ep)
	return strings.NewReplacer("\r", " ", "\n", " ").Replace(t)
}

// m3u renders the extended M3U playlist from the start episode on.
func (s *episodeServer) m3u(id string, pl *playlist) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	for i := pl.start; i < len(pl.eps); i++ {
		fmt.Fprintf(&b, "#EXTINF:-1,%s\n%s\n", entryTitle(pl.show, pl.eps[i].Name), s.epURL(id, i))
	}
	return b.String()
}

// parseEpPath splits "/ep/<id>/<n>" (an optional trailing name is ignored).
func parseEpPath(p string) (string, int, bool) {
	parts := strings.Split(strings.TrimPrefix(p, "/ep/"), "/")
	if len(parts) < 2 || parts[0] == "" {
		return "", 0, false
	}
	n, err := strconv.Atoi(parts[1])
	if err != nil || n < 0 {
		return "", 0, false
	}
	return parts[0], n, true
}

func (p *Player) startEpisodeServer() error {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	p.eps.base = "http://" + l.Addr().String()
	go http.Serve(l, http.HandlerFunc(p.serveEpisodes))
	return nil
}

func (p *Player) serveEpisodes(w http.ResponseWriter, r *http.Request) {
	// Media is served by the header proxy on another loopback port, and the
	// page prefetches episode URLs. Let the player read those responses.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	switch {
	case strings.HasPrefix(r.URL.Path, "/progress/"):
		p.serveProgress(w, r)
	case strings.HasPrefix(r.URL.Path, "/playlist/"):
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/playlist/"), ".m3u")
		pl := p.eps.get(id)
		if pl == nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "audio/x-mpegurl; charset=utf-8")
		w.Write([]byte(p.eps.m3u(id, pl)))
	case r.URL.Path == "/hls.min.js":
		b, err := fs.ReadFile(p.assets, "webassets/hls.min.js")
		if err != nil {
			http.Error(w, "播放器组件缺失", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	case r.URL.Path == "/artplayer.js":
		b, err := fs.ReadFile(p.assets, "webassets/artplayer.js")
		if err != nil {
			http.Error(w, "播放器组件缺失", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	case r.URL.Path == "/player/close":
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			http.Error(w, "method", http.StatusMethodNotAllowed)
			return
		}
		// The page cannot close a MyGo window itself. This only closes the
		// in-window player, and only while the desktop UI loop is running.
		p.closeBuiltin()
		w.WriteHeader(http.StatusNoContent)
	case r.URL.Path == "/player/state":
		p.servePlayerState(w, r)
	case strings.HasPrefix(r.URL.Path, "/ep/"):
		id, n, ok := parseEpPath(r.URL.Path)
		pl := p.eps.get(id)
		if !ok || pl == nil || n >= len(pl.eps) {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Get("prefetch") == "1" {
			// The built-in player asks for this shortly before it needs it.
			go p.prefetchEpisode(pl, n)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		direct := r.URL.Query().Get("direct") == "1"
		if !direct {
			if blocked, failedIdx := pl.isFailCascade(n, time.Now()); blocked {
				msg := fmt.Sprintf("第%d项播放失败，已停止连播", failedIdx+1)
				p.log("连播: " + msg)
				http.Error(w, msg, http.StatusBadGateway)
				return
			}
		}
		u, st, err := p.resolveEpisodeStream(pl, n)
		if err != nil {
			if !direct {
				pl.markFailed(n, time.Now())
			}
			p.log(fmt.Sprintf("连播 第%d项: %v", n+1, err))
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if !direct {
			pl.clearFailed()
		}
		w.Header().Set("Cache-Control", "no-store")
		if direct {
			// The video element follows this redirect unreliably, and hls.js
			// needs the final proxy URL. External players keep the 302.
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			kind, err := catvod.InspectStream(nil, u, nil)
			if err != nil {
				kind = mediaKind(u)
			}
			progressID, position := p.newProgressSession(pl.subID, pl.api, pl.vodID, HistoryEntry{Flag: pl.flag, Index: n, Name: pl.eps[n].Name, ShowName: pl.show, Picture: p.lookupVodPicture(pl.api, pl.vodID)}, pl.generation)
			var fallbacks []string
			if st != nil && len(st.Fallbacks) > 0 {
				fallbacks = p.proxiedFallbacks(st.Fallbacks)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"url": u, "media": kind, "progressId": progressID, "position": position, "fallbacks": fallbacks})
			return
		}
		http.Redirect(w, r, u, http.StatusFound)
	default:
		http.NotFound(w, r)
	}
}

// resolveEpisode returns the URL the player should fetch for episode n
// (through the header proxy), using a prefetched answer when there is a
// fresh one, and starts resolving episode n+1 in the background so the
// switch to it does not wait on the plugin.
func (p *Player) resolveEpisode(pl *playlist, n int) (string, error) {
	u, _, err := p.resolveEpisodeStream(pl, n)
	return u, err
}

func (p *Player) resolveEpisodeStream(pl *playlist, n int) (string, *catvod.Stream, error) {
	st, err := p.episodeStream(pl, n, true)
	if err != nil {
		return "", nil, err
	}
	u, err := p.wrapStream(pl, st)
	if err != nil {
		return "", nil, err
	}
	p.recordHistory(pl.subID, pl.api, pl.vodID, HistoryEntry{Flag: pl.flag, Index: n, Name: pl.eps[n].Name, ShowName: pl.show, Picture: p.lookupVodPicture(pl.api, pl.vodID)})
	if n+1 < len(pl.eps) {
		go p.prefetchEpisode(pl, n+1)
	}
	return u, st, nil
}

func (p *Player) wrapStream(pl *playlist, st *catvod.Stream) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pl.generation != p.sourceGeneration {
		return "", errSourceChanged
	}
	if p.proxy == nil {
		return "", errors.New("本地播放代理未启动")
	}
	return p.proxy.WrapGranted(st.URL, st.Headers)
}

// episodeStream resolves episode n once at a time: concurrent callers (the
// player and a prefetch) share one plugin call. consume drops the answer
// after use so a later replay resolves a fresh URL.
func (p *Player) episodeStream(pl *playlist, n int, consume bool) (*catvod.Stream, error) {
	if !p.isGeneration(pl.generation) {
		return nil, errSourceChanged
	}
	if n < 0 || n >= len(pl.eps) {
		return nil, errors.New("剧集不存在")
	}
	pl.mu.Lock()
	if pl.pending == nil {
		pl.pending = map[int]*resolved{}
		if pl.first != nil {
			c := make(chan struct{})
			close(c)
			pl.pending[pl.start] = &resolved{done: c, st: pl.first, at: time.Now()}
			pl.first = nil
		}
	}
	r := pl.pending[n]
	if r != nil {
		select {
		case <-r.done:
			if r.err != nil || time.Since(r.at) > resolvedTTL {
				r = nil // failed or stale: resolve again
			}
		default: // in flight: wait for it
		}
	}
	owner := r == nil
	if owner {
		r = &resolved{done: make(chan struct{})}
		pl.pending[n] = r
	}
	pl.mu.Unlock()
	if owner {
		streams, err := p.playAtGeneration(pl.api, pl.flag, pl.eps[n].ID, pl.generation)
		if err == nil {
			if r.st = selectPlayable(streams, pl.quality); r.st == nil {
				err = errors.New("这一集没有可用于连播的 HTTP(S) 播放地址")
			}
		}
		r.err, r.at = err, time.Now()
		close(r.done)
	}
	<-r.done
	if consume {
		pl.mu.Lock()
		if pl.pending[n] == r {
			delete(pl.pending, n)
		}
		pl.mu.Unlock()
	}
	if !p.isGeneration(pl.generation) {
		return nil, errSourceChanged
	}
	return r.st, r.err
}

// prefetchEpisode resolves episode n ahead of time and warms the start of
// its stream through the proxy (a playlist and its first segment, or the
// first bytes of a file), so the player's request for it is served at once.
func (p *Player) prefetchEpisode(pl *playlist, n int) {
	if n < 0 || n >= len(pl.eps) {
		return
	}
	st, err := p.episodeStream(pl, n, false)
	if err != nil {
		p.log(fmt.Sprintf("预取第 %d 项失败: %v", n+1, err))
		return
	}
	u, err := p.wrapStream(pl, st)
	if err != nil {
		p.log("预热失败: " + err.Error())
		return
	}
	warmStream(u)
}

const warmBytes = 2 << 20

var warmClient = &http.Client{Timeout: 20 * time.Second}

func warmStream(u string) {
	resp, err := warmClient.Get(u)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, warmBytes))
	if !bytes.HasPrefix(bytes.TrimSpace(body), []byte("#EXTM3U")) {
		return
	}
	// HLS: the proxy rewrote entries to absolute proxied URLs; warm the
	// first one (a variant playlist or a segment), one level deep.
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "http://") || strings.HasPrefix(line, "https://") {
			if r2, err := warmClient.Get(line); err == nil {
				b2, _ := io.ReadAll(io.LimitReader(r2.Body, warmBytes))
				r2.Body.Close()
				if bytes.HasPrefix(bytes.TrimSpace(b2), []byte("#EXTM3U")) {
					for _, l2 := range strings.Split(string(b2), "\n") {
						l2 = strings.TrimSpace(l2)
						if strings.HasPrefix(l2, "http") {
							if r3, err := warmClient.Get(l2); err == nil {
								io.Copy(io.Discard, io.LimitReader(r3.Body, warmBytes))
								r3.Body.Close()
							}
							break
						}
					}
				}
			}
		}
		break
	}
}

func continuousCandidate(st catvod.Stream) bool {
	// Player.Play grants only these URL forms to the proxy. Keep automatic
	// episode selection within that same authorization boundary.
	return !st.Parse && (st.Direct || !catvod.IsWebPage(st.URL)) && catvod.IsHTTPMediaURL(st.URL)
}

func firstPlayable(streams []catvod.Stream) *catvod.Stream {
	for i := range streams {
		if continuousCandidate(streams[i]) {
			return &streams[i]
		}
	}
	return nil
}

// selectPlayable prefers the rendition the user already chose. A later
// episode that does not offer that name still plays its first usable URL.
func selectPlayable(streams []catvod.Stream, quality string) *catvod.Stream {
	if quality = strings.TrimSpace(quality); quality != "" {
		for i := range streams {
			if streams[i].Name == quality && continuousCandidate(streams[i]) {
				return &streams[i]
			}
		}
	}
	return firstPlayable(streams)
}

// narrowToQuality keeps one named rendition when the caller has chosen it.
// An unknown name leaves the full list so the page can ask again.
func narrowToQuality(streams []catvod.Stream, quality string) []catvod.Stream {
	if strings.TrimSpace(quality) == "" {
		return streams
	}
	for i := range streams {
		if streams[i].Name == quality && continuousCandidate(streams[i]) {
			return []catvod.Stream{streams[i]}
		}
	}
	return streams
}

func preferredQuality(requested string, first *catvod.Stream) string {
	if q := strings.TrimSpace(requested); q != "" {
		return q
	}
	if first != nil {
		return first.Name
	}
	return ""
}

// PlayEpisodes plays episode start of a line. quality names one rendition
// when play() returned several. With 连播 on and a player that takes
// playlists, the rest of the line follows automatically and nil is returned.
// Otherwise (连播 off, the system player, SenPlayer, several qualities still
// to choose from, or a web page) the streams of that episode are returned
// and the page continues with Open, or asks for a quality and calls again.
func (p *Player) PlayEpisodes(ctx context.Context, api, vodID, show, flag string, eps []catvod.Episode, start int, quality string) ([]catvod.Stream, error) {
	if p.webMode.Load() {
		return nil, errors.New("网页模式请使用浏览器播放")
	}
	if start < 0 || start >= len(eps) {
		return nil, errors.New("剧集不存在")
	}
	settings, generation := p.playbackState()
	streams, err := p.playAtGenerationContext(ctx, api, flag, eps[start].ID, generation)
	if err != nil {
		return nil, err
	}
	streams = narrowToQuality(streams, quality)
	s := settings
	subID := ""
	if sub, ok := s.active(); ok {
		subID = sub.ID
	}
	histEntry := HistoryEntry{
		Flag:     flag,
		Index:    start,
		Name:     eps[start].Name,
		ShowName: show,
		Picture:  p.lookupVodPicture(api, vodID),
	}
	p.recordHistory(subID, api, vodID, histEntry)
	progressID, position := p.newProgressSession(subID, api, vodID, histEntry, generation)
	for i := range streams {
		streams[i].ProgressID, streams[i].Position = progressID, position
	}
	first := firstPlayable(streams)
	if s.DisableContinuous || !externalplayer.PlayerUsesPlaylist(s.Player) || len(streams) != 1 || p.eps.base == "" {
		return streams, nil
	}
	if first == nil {
		for _, st := range streams {
			if st.Parse || (!st.Direct && catvod.IsWebPage(st.URL)) {
				return streams, nil
			}
		}
		return nil, errors.New("没有可用于连播的 HTTP(S) 播放地址")
	}
	// Same checks as Open, on the first episode, so errors show up here.
	u := first.URL
	if p.proxy == nil {
		return nil, errors.New("本地播放代理未启动")
	}
	u, err = p.proxy.WrapGranted(u, first.Headers)
	if err != nil {
		return nil, err
	}
	var pre map[string]string
	kind, err := catvod.InspectStream(nil, u, pre)
	if err != nil {
		p.log("preflight: " + err.Error())
		return nil, err
	}
	if kind == "html" {
		return streams, nil
	}
	id, err := p.addPlaylist(&playlist{generation: generation, api: api, vodID: vodID, show: show, flag: flag, subID: subID, eps: eps, start: start, quality: preferredQuality(quality, first), first: first})
	if err != nil {
		return nil, err
	}
	p.log(fmt.Sprintf("连播(%s): %s 从第 %d 项起，共 %d 项", s.Player, p.eps.playlistURL(id), start+1, len(eps)-start))
	title := entryTitle(show, eps[start].Name)
	if s.Player == "builtin" || s.Player == "" {
		// The clicked episode is already resolved. Hand that proxied URL to
		// the page; other episodes stay as /ep/ links and are resolved with
		// ?direct=1, because a video element follows the 302 unreliably and
		// hls.js needs the final proxy URL. External players still get the M3U.
		items := builtinEpisodeItems(show, eps, start, func(i int) string { return p.eps.epURL(id, i) }, u)
		items[start].Media = kind
		items[start].ProgressID, items[start].Position = progressID, position
		items[start].Fallbacks = p.proxiedFallbacks(first.Fallbacks)
		return nil, startContinuousPlayback(p, s.Player, "", title, builtinPlayerPage(items, start, s.SkipIntro, s.SkipOutro))
	}
	return nil, startContinuousPlayback(p, s.Player, p.eps.playlistURL(id), "", "")
}

// startContinuousPlayback opens the queued episodes. Tests replace it so they
// can observe the queue without a window or an installed player.
var startContinuousPlayback = func(p *Player, player, media, title, page string) error {
	if player == "builtin" || player == "" {
		p.openBuiltin(title, page)
		return nil
	}
	return p.launch(player, media, title)
}

type builtinItem struct {
	Title      string   `json:"t"`
	URL        string   `json:"u"`
	Media      string   `json:"m,omitempty"`
	ProgressID string   `json:"p,omitempty"`
	Position   float64  `json:"s,omitempty"`
	Fallbacks  []string `json:"f,omitempty"`
}

// builtinEpisodeItems lists the whole line. The clicked episode uses the
// already resolved proxy URL when one exists; every other entry points at
// the episode server so it is resolved only when the player reaches it.
// Previous and next can therefore move both ways. External M3U playlists
// still start at the clicked episode and are built separately.
func builtinEpisodeItems(show string, eps []catvod.Episode, start int, epURL func(int) string, firstURL string) []builtinItem {
	items := make([]builtinItem, 0, len(eps))
	for i := 0; i < len(eps); i++ {
		u := epURL(i)
		if i == start && firstURL != "" {
			u = firstURL
		}
		items = append(items, builtinItem{Title: entryTitle(show, eps[i].Name), URL: u})
	}
	return items
}

func playbackPageBase(loopback string) string {
	if loopback == "" {
		return "http://127.0.0.1/"
	}
	return strings.TrimRight(loopback, "/") + "/"
}

// builtinPlaylistPage is the in-window player starting at the first item.
func builtinPlaylistPage(items []builtinItem) string {
	return builtinPlayerPage(items, 0, 0, 0)
}

// builtinPlayerPage is the desktop built-in player: the same HTML5 video and
// hls.js path as web mode. start is the index to play first. The page is
// loaded with the episode server as its base, so hls.min.js and player/close
// resolve there. macOS can play HLS natively; other web views use hls.js
// (enableWorker false, because a blob worker is not required and is easier
// to block). Episode-server URLs are resolved with ?direct=1 first.
func builtinPlayerPage(items []builtinItem, start int, skipIntro, skipOutro int) string {
	if items == nil {
		items = []builtinItem{}
	}
	if start < 0 || start >= len(items) {
		start = 0
	}
	j, _ := json.Marshal(items)
	payload := strings.ReplaceAll(string(j), "</", `<\/`)
	title := ""
	if len(items) > 0 {
		title = items[start].Title
	}
	hint, _ := json.Marshal(builtinPlaybackHint())
	return `<!doctype html><html><head><meta charset="utf-8"><title>` + html.EscapeString(title) + `</title>
<meta name="viewport" content="width=device-width, initial-scale=1.0">
<style>
html,body{margin:0;padding:0;width:100%;height:100%;background:#000;color:#fff;font:13px system-ui,-apple-system,BlinkMacSystemFont,"Segoe UI",Roboto,sans-serif;overflow:hidden;user-select:none}
#playerWrap{position:relative;width:100%;height:100%;background:#000;overflow:hidden}
#artplayer{width:100%;height:100%;background:#000}
.art-video-player{width:100%;height:100%}
#bar{position:absolute;top:0;left:0;right:0;height:44px;display:flex;gap:8px;align-items:center;padding:0 12px;background:linear-gradient(rgba(0,0,0,0.85),rgba(0,0,0,0));z-index:80;transition:opacity .25s ease}
#bar.bar-hidden{opacity:0;pointer-events:none}
#t{flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap;font-size:13px;font-weight:500}
button,#ls{background:rgba(255,255,255,0.14);color:#fff;border:1px solid rgba(255,255,255,0.18);border-radius:6px;padding:5px 10px;font-size:12px;cursor:pointer;backdrop-filter:blur(6px);transition:background .15s}
button:hover,#ls:hover{background:rgba(255,255,255,0.24)}
#ls{outline:none;font:inherit}#ls option{background:#222;color:#fff}
button:disabled{opacity:.35;cursor:default}#e{color:#f88;max-width:38%;font-size:12px;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
#go{position:fixed;left:50%;top:52%;transform:translate(-50%,-50%);background:#38bdf8;color:#0f172a;font-weight:600;font-size:14px;padding:9px 18px;border-radius:6px;border:0;cursor:pointer;z-index:100}
</style></head>
<body>
<div id="playerWrap">
  <div id="artplayer"></div>
  <div id="bar">
    <span id="t"></span>
    <span id="e"></span>
    <select id="ls" title="切换线路" hidden></select>
    <button id="p" type="button">上一集</button>
    <button id="n" type="button">下一集</button>
    <button id="fs" type="button">全屏</button>
    <button id="c" type="button">关闭</button>
  </div>
  <button id="go" type="button" hidden>点击播放</button>
</div>
<script src="hls.min.js"></script>
<script src="artplayer.js"></script>
<script>
const L=` + payload + `;
const START=` + strconv.Itoa(start) + `;
const HINT=` + string(hint) + `;
const SKIP_INTRO=` + strconv.Itoa(skipIntro) + `;
const SKIP_OUTRO=` + strconv.Itoa(skipOutro) + `;
let i=-1, token=0, hls=null, pf=-1, suppress=false, failed=false, played=false, stallTimer=0, progressId='', progressAt=0, pendingSeek=0, progressReady=false, outroTriggered=false;
let art=null;
try{
  art = new Artplayer({
    container: '#artplayer',
    url: '',
    theme: '#38bdf8',
    volume: 0.8,
    isLive: false,
    muted: false,
    autoplay: true,
    pip: true,
    autoSize: false,
    autoMini: false,
    screenshot: true,
    setting: true,
    loop: false,
    flip: true,
    playbackRate: true,
    aspectRatio: true,
    hotkey: true,
    fullscreen: true,
    fullscreenWeb: true,
    subtitleOffset: false,
    miniProgressBar: true,
    mutex: true,
    backdrop: true,
    playsInline: true,
    autoPlayback: false,
    airplay: true,
    lang: 'zh-cn',
    moreVideoAttr: {
      controls: false,
      crossOrigin: 'anonymous',
      playsinline: 'true',
      'webkit-playsinline': 'true'
    },
    customType: {
      m3u8: function(video, url, instance){
        attachHlsToVideo(video, url, instance);
      }
    }
  });
}catch(e){
  console.error('Artplayer init error:', e);
}
const v=(art&&art.video)||document.querySelector('video')||document.createElement('video');
const err=document.getElementById('e');
const goBtn=document.getElementById('go');
const lineSel=document.getElementById('ls');
const topBar=document.getElementById('bar');
const wrap=document.getElementById('playerWrap')||document.body;

if(art&&art.template&&art.template.$player){
  art.template.$player.appendChild(topBar);
}

let barHideTimer=0;
function showTopBar(){
  clearTimeout(barHideTimer);
  topBar.classList.remove('bar-hidden');
  if(v&&!v.paused){
    barHideTimer=setTimeout(function(){topBar.classList.add('bar-hidden');},3000);
  }
}
wrap.addEventListener('mousemove',showTopBar);
wrap.addEventListener('touchstart',showTopBar);
topBar.addEventListener('mouseenter',function(){clearTimeout(barHideTimer);topBar.classList.remove('bar-hidden');});
topBar.addEventListener('mouseleave',showTopBar);
if(art){
  art.on('controls',function(state){
    if(state) showTopBar();
    else if(v&&!v.paused) topBar.classList.add('bar-hidden');
  });
}
function showErr(m){err.textContent=m||HINT;}
function reportProgress(final=false){
 if(!progressReady||!progressId||!Number.isFinite(v.duration)||v.duration<=0)return;
 const now=Date.now();if(!final&&now-progressAt<10000)return;progressAt=now;
 fetch('progress/'+encodeURIComponent(progressId),{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({position:v.currentTime||0,duration:v.duration,final}),keepalive:final}).catch(()=>{});
}
function syncNowPlaying(){
 const item=(i>=0&&L[i])?L[i]:null;
 fetch('player/state',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({title:(item&&item.t)||document.title||'',playing:!v.paused&&!v.ended,position:Number.isFinite(v.currentTime)?v.currentTime:0,duration:Number.isFinite(v.duration)&&v.duration>0?v.duration:0,hasPrev:i>0,hasNext:i>=0&&i+1<L.length})}).catch(()=>{});
}
window.__jstvMediaCmd=function(cmd,pos){
 if(cmd==='play') tryPlay();
 else if(cmd==='pause') { if(art) art.pause(); else if(v) v.pause(); }
 else if(cmd==='next'&&i+1<L.length) go(i+1);
 else if(cmd==='prev'&&i>0) go(i-1);
 else if(cmd==='seek'&&Number.isFinite(pos)){
   if(art) art.currentTime=Math.max(0,pos);
   else if(v) v.currentTime=Math.max(0,pos);
   syncNowPlaying();
 }
 else if(cmd==='stop') document.getElementById('c').click();
};
window.__jstvGo=function(idx){
  go(idx);
};
window.__jstvCanJump=function(idx){
  return typeof idx==='number'&&idx>=0&&idx<L.length;
};
window.__jstvDestroy=function(){
  destroyHls();
  if(art&&typeof art.destroy==='function'){try{art.destroy(false);}catch(e){}}
};
function sync(){
  document.getElementById('p').disabled=i<=0;
  document.getElementById('n').disabled=i<0||i+1>=L.length;
  const label=L.length&&i>=0?(L[i].t+'  ('+(i+1)+'/'+L.length+')'):'';
  document.getElementById('t').textContent=label;
  if(i>=0&&L[i]) document.title=L[i].t||'';
  try{if(window.parent&&window.parent!==window)window.parent.postMessage({type:'jstv-episode-change',index:i},'*');}catch(e){}
}
function destroyHls(){
  clearTimeout(stallTimer);
  suppress=true;
  if(art&&art.hls){try{art.hls.destroy();}catch(e){}art.hls=null;}
  if(hls){hls.destroy();hls=null;}
  v.pause();
  v.removeAttribute('src');
  try{v.load();}catch(e){}
}
function failPlayback(msg){
  failed=true;
  clearTimeout(stallTimer);
  if(progressReady&&progressId&&Number.isFinite(v.duration)&&v.duration>0&&v.currentTime>0&&v.duration-v.currentTime>3){
    reportProgress(true);
  }
  progressReady=false;
  destroyHls();
  if(art&&art.notice){art.notice.show=msg||('播放失败，已停止连播。'+HINT.replace(/^播放失败[，,]?/,''));}
  showErr(msg||('播放失败，已停止连播。'+HINT.replace(/^播放失败[，,]?/,'')));
  syncNowPlaying();
}
function tryPlay(){
  goBtn.hidden=true;
  if(art){
    const p=art.play();
    if(p&&p.catch) p.catch(function(){goBtn.hidden=false;});
    return;
  }
  const pending=v.play();
  if(pending&&pending.catch) pending.catch(function(){goBtn.hidden=false;});
}
function isEpisode(u){
  try{return /^\/ep\/[^/]+\/\d+(\/|$)/.test(new URL(u).pathname);}catch(e){return false;}
}
function isHls(u){return /\.m3u8?(\?|#|$)/i.test(u);}
async function mediaURL(u){
  if(!isEpisode(u)) return {url:u};
  const sep=u.indexOf('?')>=0?'&':'?';
  const r=await fetch(u+sep+'direct=1',{headers:{Accept:'application/json'}});
  const body=await r.json().catch(function(){return {};});
  if(!r.ok||!body.url) throw new Error((body&&body.error)||'这一集解析失败');
  return body;
}
let lineAt=0;
function lineList(item){return [item.u].concat(item.f||[]).filter(Boolean);}
function syncLines(){
  const item=(i>=0&&L[i])?L[i]:null;
  const list=item?lineList(item):[];
  if(!lineSel)return;
  if(list.length<=1){lineSel.hidden=true;lineSel.replaceChildren();return;}
  lineSel.hidden=false;
  lineSel.replaceChildren(...list.map(function(_,idx){
    const opt=document.createElement('option');
    opt.value=idx;
    opt.textContent='线路 '+(idx+1)+(idx===0?' (默认)':'');
    opt.selected=idx===lineAt;
    return opt;
  }));
}
if(lineSel){
  lineSel.onchange=function(){
    const idx=parseInt(this.value,10);
    const item=(i>=0&&L[i])?L[i]:null; const list=item?lineList(item):[];
    if(!item||idx<0||idx>=list.length||idx===lineAt)return;
    lineAt=idx;played=false;
    if(Number.isFinite(v.currentTime)&&v.currentTime>0)pendingSeek=v.currentTime;
    err.textContent='已切换至线路 '+(lineAt+1)+'/'+list.length;
    destroyHls();attach(list[lineAt],item.m,token);arm(token);
  };
}
function arm(mine){
 clearTimeout(stallTimer);
 stallTimer=setTimeout(function(){if(mine===token&&!failed&&!(v.paused&&!goBtn.hidden)&&v.currentTime<0.2) failLine(mine);},15000);
}
function failLine(mine){
 if(mine!==token||failed) return;
 const item=L[i]; const list=item?lineList(item):[];
 if(!item||lineAt+1>=list.length){failPlayback();return;}
 lineAt++; played=false; err.textContent='正在切换线路 '+(lineAt+1)+'/'+list.length;
 syncLines();
 destroyHls(); attach(list[lineAt], item.m, mine); arm(mine);
}
function attachHlsToVideo(video,url,instance){
  const mine=token;
  if(video.canPlayType('application/vnd.apple.mpegurl')){
    suppress=false;
    video.src=url;
    tryPlay();
    return;
  }
  if(window.Hls&&window.Hls.isSupported()){
    if(instance&&instance.hls){try{instance.hls.destroy();}catch(e){}instance.hls=null;}
    hls=new window.Hls({enableWorker:false,lowLatencyMode:false});
    hls.on(window.Hls.Events.ERROR,function(_,data){if(!data||!data.fatal)return;failLine(mine);});
    hls.on(window.Hls.Events.MANIFEST_PARSED,function(){if(mine===token)tryPlay();});
    hls.loadSource(url);
    hls.attachMedia(video);
    if(instance) instance.hls=hls;
    setTimeout(function(){suppress=false;},0);
    return;
  }
  failPlayback('当前窗口不能播放 HLS（没有 Media Source Extensions），已停止连播。'+HINT.replace(/^播放失败[，,]?/,''));
}
function attach(url,media,mine){
  const hlsLike=media==='hls'||isHls(url);
  suppress=false;
  if(art){
    art.type=hlsLike?'m3u8':'';
    art.option.type=hlsLike?'m3u8':'';
    art.url=url;
    if(!hlsLike){
      tryPlay();
    }
    return;
  }
  if(hlsLike&&v.canPlayType('application/vnd.apple.mpegurl')){suppress=false;v.src=url;tryPlay();return;}
  if(hlsLike&&window.Hls&&window.Hls.isSupported()){
    hls=new window.Hls({enableWorker:false,lowLatencyMode:false});
    hls.on(window.Hls.Events.ERROR,function(_,data){if(!data||!data.fatal)return;failLine(mine);});
    hls.on(window.Hls.Events.MANIFEST_PARSED,function(){if(mine===token)tryPlay();});
    hls.loadSource(url);
    hls.attachMedia(v);
    setTimeout(function(){suppress=false;},0);
    return;
  }
  if(hlsLike){failPlayback('当前窗口不能播放 HLS（没有 Media Source Extensions），已停止连播。'+HINT.replace(/^播放失败[，,]?/,''));return;}
  suppress=false;v.src=url;tryPlay();
}
async function go(k){
  if(!L.length||k<0||k>=L.length)return;
  if(!failed) reportProgress(true);
  progressId='';progressReady=false;
  const mine=++token;
  failed=false;played=false;pf=-1;outroTriggered=false;
  i=k;sync();err.textContent='';goBtn.hidden=true;
  destroyHls();
  let result;const item=L[i];
  try{result=await mediaURL(item.u);}catch(e){if(mine===token)failPlayback(((e&&e.message)||'这一集解析失败')+'，已停止连播');return;}
  if(mine!==token)return;
  progressId=item.p||result.progressId||'';pendingSeek=item.s||result.position||0;progressAt=0;
  if(result.fallbacks&&result.fallbacks.length) item.f=result.fallbacks;
  lineAt=0; syncLines(); attach(result.url,item.m||result.media,mine); arm(mine);
}
v.addEventListener('loadedmetadata',function(){
  if(pendingSeek>0&&Number.isFinite(v.duration)&&pendingSeek<v.duration-3){
    v.currentTime=pendingSeek;
  }else if(SKIP_INTRO>0&&Number.isFinite(v.duration)&&SKIP_INTRO<v.duration-3){
    v.currentTime=SKIP_INTRO;
  }
  pendingSeek=0;progressReady=true;syncNowPlaying();
});
v.addEventListener('play',()=>{if(!suppress){goBtn.hidden=true;syncNowPlaying();showTopBar();}});
v.addEventListener('seeked',()=>{if(!suppress)syncNowPlaying();});
v.addEventListener('pause',()=>{if(!suppress&&!failed){reportProgress(true);syncNowPlaying();showTopBar();}});
window.addEventListener('pagehide',()=>{if(!failed)reportProgress(true);});
window.addEventListener('beforeunload',()=>{destroyHls();if(!failed)reportProgress(true);});
v.addEventListener('timeupdate',function(){
  if(suppress||failed)return;
  if(v.currentTime>=0.2){played=true;clearTimeout(stallTimer);}
  reportProgress();
  if(!outroTriggered&&SKIP_OUTRO>0&&Number.isFinite(v.duration)&&v.duration>SKIP_OUTRO+10){
    if(v.duration-v.currentTime<=SKIP_OUTRO){
      outroTriggered=true;
      reportProgress(true);
      if(i+1<L.length)go(i+1);
    }
  }
  if(i+1<L.length&&pf!==i+1&&v.duration&&isFinite(v.duration)&&v.duration-v.currentTime<60){
    pf=i+1;const u=L[i+1].u;
    if(isEpisode(u)) fetch(u+(u.indexOf('?')>=0?'&':'?')+'prefetch=1').catch(function(){});
  }
});
v.addEventListener('ended',function(){
  if(suppress||failed||v.error)return;
  const dur=v.duration;
  if(!Number.isFinite(dur)||dur<=0||(!played&&v.currentTime<=0)){failPlayback('播放异常中断，已停止连播');return;}
  if(dur>30&&dur-v.currentTime>15){failPlayback('播放提前中断，已停止连播');return;}
  reportProgress(true);
  if(i+1<L.length)go(i+1);
});
v.addEventListener('error',function(){if(suppress||failed)return;if(!v.getAttribute('src')&&!hls&&(!art||!art.hls))return;failLine(token);});
document.getElementById('p').onclick=function(){go(i-1);};
document.getElementById('n').onclick=function(){go(i+1);};
document.getElementById('fs').onclick=function(){
  if(art){
    art.fullscreen=!art.fullscreen;
    return;
  }
  const isFs=!!(document.fullscreenElement||document.webkitFullscreenElement);
  if(isFs){if(document.exitFullscreen)document.exitFullscreen();else if(document.webkitExitFullscreen)document.webkitExitFullscreen();}
  else{const req=wrap.requestFullscreen||wrap.webkitRequestFullscreen;if(req)req.call(wrap);}
};
document.getElementById('c').onclick=function(){
  reportProgress(true);progressId='';token++;destroyHls();
  if(art&&typeof art.destroy==='function'){try{art.destroy(false);}catch(e){}}
  fetch('player/close',{method:'POST',mode:'no-cors'}).catch(function(){});
  try{if(window.parent&&window.parent!==window)window.parent.postMessage({type:'jstv-close-player'},'*');}catch(e){}
};
goBtn.onclick=function(){tryPlay();};
if(L.length) go(START); else showErr('没有可播放的地址');
</script></body></html>`
}

// ---- watch history ----

func historyKey(subID, api, vodID string) string {
	return subID + "\x00" + persistentAPI(api) + "\x00" + vodID
}

func legacyHistoryParts(key string) (api, vodID string, ok bool) {
	if strings.Count(key, "\x00") != 1 {
		return "", "", false
	}
	api, vodID, ok = strings.Cut(key, "\x00")
	if !ok || api == "" || vodID == "" {
		return "", "", false
	}
	return api, vodID, true
}

func (p *Player) activeSubID() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if i := p.settings.find(p.settings.Active); i >= 0 {
		return p.settings.Active
	}
	return ""
}

func (p *Player) recordHistory(subID, api, vodID string, e HistoryEntry) {
	if vodID == "" {
		return
	}
	if p.GetSettings().DisableHistory {
		return
	}
	if subID == "" {
		subID = p.activeSubID()
	}
	e.Time = time.Now().Unix()
	p.histMu.Lock()
	defer p.histMu.Unlock()
	h := p.loadHistoryLocked(subID)
	if h == nil {
		return
	}
	old := h[historyKey(subID, api, vodID)]
	if old.Flag == e.Flag && old.Index == e.Index && e.Position == 0 && e.Duration == 0 {
		e.Position, e.Duration = old.Position, old.Duration
	}
	if e.ShowName == "" {
		e.ShowName = old.ShowName
	}
	if e.Picture == "" {
		e.Picture = old.Picture
	}
	h[historyKey(subID, api, vodID)] = e
	if len(h) > 2000 {
		var oldK string
		var oldT int64 = 1 << 62
		for k, v := range h {
			if v.Time < oldT {
				oldK, oldT = k, v.Time
			}
		}
		delete(h, oldK)
	}
	if err := atomicfile.JSON(filepath.Join(p.dir, "history.json"), h); err != nil {
		p.log("保存播放记录失败: " + err.Error())
	}
}

func (p *Player) loadHistoryLocked(subID string) map[string]HistoryEntry {
	h := map[string]HistoryEntry{}
	path := filepath.Join(p.dir, "history.json")
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &h); err != nil || h == nil {
			backup, backupErr := os.CreateTemp(p.dir, "history-corrupt-*.json")
			if backupErr != nil {
				p.log("无法备份损坏的播放记录: " + backupErr.Error())
				return nil
			}
			name := backup.Name()
			backup.Close()
			if err := atomicfile.Write(name, b, 0o600); err != nil {
				p.log("备份播放记录失败: " + err.Error())
				return nil
			}
			h = map[string]HistoryEntry{}
			if err := atomicfile.JSON(path, h); err != nil {
				p.log("重建播放记录失败: " + err.Error())
				return nil
			}
			p.log("损坏的播放记录已保留到 " + filepath.Base(name))
		}
	} else if !os.IsNotExist(err) {
		p.log("读取播放记录失败: " + err.Error())
		return nil
	}
	if subID == "" {
		return h
	}
	changed := false
	for key, entry := range h {
		api, vodID, ok := legacyHistoryParts(key)
		if !ok {
			continue
		}
		scoped := historyKey(subID, api, vodID)
		if _, exists := h[scoped]; !exists {
			h[scoped] = entry
		}
		delete(h, key)
		changed = true
	}
	if changed {
		if err := atomicfile.JSON(path, h); err != nil {
			p.log("迁移播放记录失败: " + err.Error())
		}
	}
	return h
}

// GetHistory returns the last episode played of a show in the active
// subscription, or nil.
func (p *Player) GetHistory(api, vodID string) *HistoryEntry {
	subID := p.activeSubID()
	p.histMu.Lock()
	defer p.histMu.Unlock()
	if e, ok := p.loadHistoryLocked(subID)[historyKey(subID, api, vodID)]; ok {
		return &e
	}
	return nil
}

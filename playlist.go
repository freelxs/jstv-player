package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/freelxs/jstv-player/internal/catvod"
)

// Continuous playback (连播): a show's remaining episodes are handed to the
// player as one local playlist. Every entry points at /ep/<id>/<n> on a
// loopback server, which asks the plugin for that episode only when the
// player reaches it (plugin URLs are often short-lived) and redirects to
// the proxied stream, so headers and HLS rewriting work as for one episode.

type playlist struct {
	api, vodID, show, flag string
	subID                  string // subscription that owns this show; empty uses the active one
	eps                    []catvod.Episode
	start                  int
	first                  *catvod.Stream // already resolved start episode

	mu      sync.Mutex
	pending map[int]*resolved // resolved or being resolved, by index
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
	Flag  string `json:"flag"`
	Index int    `json:"index"`
	Name  string `json:"name"`
	Time  int64  `json:"time"`
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
		b, err := webAssets.ReadFile("webassets/hls.min.js")
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
		u, err := p.resolveEpisode(pl, n)
		if err != nil {
			p.log(fmt.Sprintf("连播 第%d项: %v", n+1, err))
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Query().Get("direct") == "1" {
			// The video element follows this redirect unreliably, and hls.js
			// needs the final proxy URL. External players keep the 302.
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(w).Encode(map[string]string{"url": u})
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
	st, err := p.episodeStream(pl, n, true)
	if err != nil {
		return "", err
	}
	p.recordHistory(pl.subID, pl.api, pl.vodID, HistoryEntry{Flag: pl.flag, Index: n, Name: pl.eps[n].Name})
	if n+1 < len(pl.eps) {
		go p.prefetchEpisode(pl, n+1)
	}
	return p.wrapStream(st)
}

func (p *Player) wrapStream(st *catvod.Stream) (string, error) {
	if p.proxy == nil {
		return "", errors.New("本地播放代理未启动")
	}
	return p.proxy.WrapGranted(st.URL, st.Headers)
}

// episodeStream resolves episode n once at a time: concurrent callers (the
// player and a prefetch) share one plugin call. consume drops the answer
// after use so a later replay resolves a fresh URL.
func (p *Player) episodeStream(pl *playlist, n int, consume bool) (*catvod.Stream, error) {
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
		streams, err := p.Play(pl.api, pl.flag, pl.eps[n].ID)
		if err == nil {
			if r.st = firstPlayable(streams); r.st == nil {
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
	u, err := p.wrapStream(st)
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

func firstPlayable(streams []catvod.Stream) *catvod.Stream {
	for i := range streams {
		// Player.Play grants only these URL forms to the proxy. Keep automatic
		// episode selection within that same authorization boundary.
		if !streams[i].Parse && !catvod.IsWebPage(streams[i].URL) && catvod.IsHTTPMediaURL(streams[i].URL) {
			return &streams[i]
		}
	}
	return nil
}

// PlayEpisodes plays episode start of a line. With 连播 on and a player
// that takes playlists, the rest of the line follows automatically and nil
// is returned. Otherwise (连播 off, the system player, SenPlayer, several
// qualities to choose from, or a web page) the streams of that episode are
// returned and the page continues with Open as before.
func (p *Player) PlayEpisodes(api, vodID, show, flag string, eps []catvod.Episode, start int) ([]catvod.Stream, error) {
	if p.webMode.Load() {
		return nil, errors.New("网页模式请使用浏览器播放")
	}
	if start < 0 || start >= len(eps) {
		return nil, errors.New("剧集不存在")
	}
	streams, err := p.Play(api, flag, eps[start].ID)
	if err != nil {
		return nil, err
	}
	s := p.GetSettings()
	subID := ""
	if sub, ok := s.active(); ok {
		subID = sub.ID
	}
	p.recordHistory(subID, api, vodID, HistoryEntry{Flag: flag, Index: start, Name: eps[start].Name})
	first := firstPlayable(streams)
	if s.DisableContinuous || !playerUsesPlaylist(s.Player) || len(streams) != 1 || p.eps.base == "" {
		return streams, nil
	}
	if first == nil {
		for _, st := range streams {
			if st.Parse || catvod.IsWebPage(st.URL) {
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
	isHTML, err := catvod.Preflight(nil, u, pre)
	if err != nil {
		p.log("preflight: " + err.Error())
		return nil, err
	}
	if isHTML {
		return streams, nil
	}
	id := p.eps.add(&playlist{api: api, vodID: vodID, show: show, flag: flag, subID: subID, eps: eps, start: start, first: first})
	p.log(fmt.Sprintf("连播(%s): %s 从第 %d 项起，共 %d 项", s.Player, p.eps.playlistURL(id), start+1, len(eps)-start))
	if s.Player == "builtin" || s.Player == "" {
		// The clicked episode is already resolved. Hand that proxied URL to
		// the page; other episodes stay as /ep/ links and are resolved with
		// ?direct=1, because a video element follows the 302 unreliably and
		// hls.js needs the final proxy URL. External players still get the M3U.
		items := builtinEpisodeItems(show, eps, start, func(i int) string { return p.eps.epURL(id, i) }, u)
		p.openBuiltin(entryTitle(show, eps[start].Name), builtinPlayerPage(items, start))
		return nil, nil
	}
	return nil, p.launch(s.Player, p.eps.playlistURL(id), "")
}

type builtinItem struct {
	Title string `json:"t"`
	URL   string `json:"u"`
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
	return builtinPlayerPage(items, 0)
}

// builtinPlayerPage is the desktop built-in player: the same HTML5 video and
// hls.js path as web mode. start is the index to play first. The page is
// loaded with the episode server as its base, so hls.min.js and player/close
// resolve there. macOS can play HLS natively; other web views use hls.js
// (enableWorker false, because a blob worker is not required and is easier
// to block). Episode-server URLs are resolved with ?direct=1 first.
func builtinPlayerPage(items []builtinItem, start int) string {
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
<style>html,body{margin:0;height:100%;background:#000;color:#fff;font:13px system-ui,sans-serif}video{width:100%;height:100%;background:#000}
#bar{position:fixed;top:0;left:0;right:0;display:flex;gap:8px;align-items:center;padding:8px 12px;background:linear-gradient(#000c,#0000);z-index:2}
#t{flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap}button{background:#fff2;color:#fff;border:0;border-radius:6px;padding:6px 12px;cursor:pointer}
button:disabled{opacity:.35;cursor:default}#e{color:#f88;max-width:42%}
#go{position:fixed;left:50%;top:56%;transform:translate(-50%,-50%);background:#fff;color:#111;font-size:15px;padding:10px 18px;z-index:3}</style></head>
<body><video controls autoplay playsinline crossorigin="anonymous"></video><button id="go" type="button" hidden>点击播放</button>
<div id="bar"><span id="t"></span><span id="e"></span><button id="p" type="button">上一集</button><button id="n" type="button">下一集</button><button id="c" type="button">关闭</button></div>
<script src="hls.min.js"></script>
<script>
const L=` + payload + `;
const START=` + strconv.Itoa(start) + `;
const HINT=` + string(hint) + `;
let i=-1, token=0, hls=null, pf=-1, suppress=false;
const v=document.querySelector('video');
const err=document.getElementById('e');
const goBtn=document.getElementById('go');
function showErr(m){err.textContent=m||HINT;}
function sync(){
  document.getElementById('p').disabled=i<=0;
  document.getElementById('n').disabled=i<0||i+1>=L.length;
  const label=L.length&&i>=0?(L[i].t+'  ('+(i+1)+'/'+L.length+')'):'';
  document.getElementById('t').textContent=label;
  if(i>=0&&L[i]) document.title=L[i].t||'';
}
function destroyHls(){
  suppress=true;
  if(hls){hls.destroy();hls=null;}
  v.pause();
  v.removeAttribute('src');
  try{v.load();}catch(e){}
}
function tryPlay(){
  goBtn.hidden=true;
  const pending=v.play();
  if(pending&&pending.catch) pending.catch(function(){goBtn.hidden=false;});
}
function isEpisode(u){
  try{return /^\/ep\/[^/]+\/\d+(\/|$)/.test(new URL(u).pathname);}catch(e){return false;}
}
function isHls(u){return /\.m3u8?(\?|#|$)/i.test(u);}
async function mediaURL(u){
  if(!isEpisode(u)) return u;
  const sep=u.indexOf('?')>=0?'&':'?';
  const r=await fetch(u+sep+'direct=1',{headers:{Accept:'application/json'}});
  const body=await r.json().catch(function(){return {};});
  if(!r.ok||!body.url) throw new Error((body&&body.error)||'这一集解析失败');
  return body.url;
}
function attach(url){
  const hlsLike=isHls(url);
  if(hlsLike&&v.canPlayType('application/vnd.apple.mpegurl')){suppress=false;v.src=url;tryPlay();return;}
  if(hlsLike&&window.Hls&&window.Hls.isSupported()){
    hls=new window.Hls({enableWorker:false,lowLatencyMode:false});
    hls.on(window.Hls.Events.ERROR,function(_,data){if(!data||!data.fatal)return;showErr(HINT);});
    hls.on(window.Hls.Events.MANIFEST_PARSED,function(){tryPlay();});
    hls.loadSource(url);
    hls.attachMedia(v);
    setTimeout(function(){suppress=false;},0);
    return;
  }
  if(hlsLike){showErr('当前窗口不能播放 HLS（没有 Media Source Extensions）。'+HINT);return;}
  suppress=false;v.src=url;tryPlay();
}
async function go(k){
  if(!L.length||k<0||k>=L.length)return;
  const mine=++token;
  i=k;sync();err.textContent='';goBtn.hidden=true;
  destroyHls();
  let url;
  try{url=await mediaURL(L[i].u);}catch(e){if(mine===token)showErr((e&&e.message)||HINT);return;}
  if(mine!==token)return;
  attach(url);
}
v.addEventListener('timeupdate',function(){
  if(i+1<L.length&&pf!==i+1&&v.duration&&isFinite(v.duration)&&v.duration-v.currentTime<60){
    pf=i+1;const u=L[i+1].u;
    if(isEpisode(u)) fetch(u+(u.indexOf('?')>=0?'&':'?')+'prefetch=1').catch(function(){});
  }
});
v.addEventListener('ended',function(){if(i+1<L.length)go(i+1);});
v.addEventListener('error',function(){if(suppress)return;if(!v.getAttribute('src')&&!hls)return;if(!err.textContent)showErr(HINT);});
document.getElementById('p').onclick=function(){go(i-1);};
document.getElementById('n').onclick=function(){go(i+1);};
document.getElementById('c').onclick=function(){token++;destroyHls();fetch('player/close',{method:'POST',mode:'no-cors'}).catch(function(){});};
goBtn.onclick=function(){tryPlay();};
if(L.length) go(START); else showErr('没有可播放的地址');
</script></body></html>`
}

// ---- watch history ----

func historyKey(subID, api, vodID string) string {
	return subID + "\x00" + api + "\x00" + vodID
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
	if subID == "" {
		subID = p.activeSubID()
	}
	e.Time = time.Now().Unix()
	p.histMu.Lock()
	defer p.histMu.Unlock()
	h := p.loadHistoryLocked(subID)
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
	b, _ := json.Marshal(h)
	_ = os.WriteFile(filepath.Join(p.dir, "history.json"), b, 0o600)
}

func (p *Player) loadHistoryLocked(subID string) map[string]HistoryEntry {
	h := map[string]HistoryEntry{}
	path := filepath.Join(p.dir, "history.json")
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &h)
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
		if b, err := json.Marshal(h); err == nil {
			_ = os.WriteFile(path, b, 0o600)
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

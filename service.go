package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/freelxs/jstv-player/internal/catvod"
)

// Settings are kept in settings.json in the user data directory.
type Settings struct {
	SourceURL string `json:"sourceUrl"` // user-supplied plugin address (index.js or index.js.md5)
	NodePath  string `json:"nodePath"`  // optional explicit node executable
	Player    string `json:"player"`    // builtin | iina | mpv | system
	StrictMD5 bool   `json:"strictMd5"` // refuse to run when index.js.md5 differs
	// BridgeTemplate is where the built-in play bridge (/play?vid=) finds
	// media: {vid} and {res} are substituted. Empty disables the bridge.
	BridgeTemplate string `json:"bridgeTemplate"`
}

// LoadResult is what LoadSource reports to the page.
type LoadResult struct {
	Sites       []catvod.Site `json:"sites"`
	MD5         string        `json:"md5"`
	ExpectedMD5 string        `json:"expectedMd5"`
	Mismatch    bool          `json:"mismatch"`
	Node        string        `json:"node"`
}

// DetailResult is a vod with its play lines parsed.
type DetailResult struct {
	Vod   catvod.M      `json:"vod"`
	Lines []catvod.Line `json:"lines"`
}

// SiteResults is one site's search answer.
type SiteResults struct {
	Site  catvod.Site `json:"site"`
	List  []any       `json:"list"`
	Error string      `json:"error,omitempty"`
}

// Player is the service the page calls (window.mygo.call("Player.X", …)).
type Player struct {
	dir    string
	proxy  *catvod.HeaderProxy
	bridge *catvod.Bridge

	mu       sync.Mutex
	settings Settings
	host     *catvod.Host
	sites    []catvod.Site
	inited   map[string]bool
	logs     []string
}

func newPlayer(dir string) *Player {
	p := &Player{dir: dir, inited: map[string]bool{}, proxy: &catvod.HeaderProxy{}}
	if err := p.proxy.Start(); err != nil {
		p.proxy = nil
	}
	if b, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
		_ = json.Unmarshal(b, &p.settings)
	}
	p.startBridge(p.settings.BridgeTemplate)
	if p.settings.Player == "" {
		p.settings.Player = "builtin"
	}
	return p
}

func (p *Player) log(s string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.logs = append(p.logs, time.Now().Format("15:04:05 ")+s)
	if len(p.logs) > 500 {
		p.logs = p.logs[len(p.logs)-500:]
	}
}

// Logs returns the recent output of the source process.
func (p *Player) Logs() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.logs...)
}

// GetSettings returns the saved settings.
func (p *Player) GetSettings() Settings {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.settings
}

// SaveSettings stores the settings.
func (p *Player) SaveSettings(s Settings) error {
	if s.Player == "" {
		s.Player = "builtin"
	}
	s.SourceURL = strings.TrimSpace(s.SourceURL)
	b, _ := json.MarshalIndent(s, "", "  ")
	if err := os.WriteFile(filepath.Join(p.dir, "settings.json"), b, 0o600); err != nil {
		return err
	}
	p.mu.Lock()
	p.settings = s
	p.mu.Unlock()
	p.startBridge(s.BridgeTemplate)
	return nil
}

// startBridge runs the play bridge only when the user configured a
// template; with an empty template it stays off.
func (p *Player) startBridge(tpl string) {
	tpl = strings.TrimSpace(tpl)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bridge != nil {
		p.bridge.Template = tpl
		return
	}
	if tpl == "" {
		return
	}
	b := &catvod.Bridge{Template: tpl}
	if err := b.Start(catvod.DefaultBridgePort); err == nil {
		p.bridge = b
	}
}

func (p *Player) bridgeAddr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bridge == nil || !p.bridge.Enabled() {
		return ""
	}
	return p.bridge.Addr()
}

// LoadSource downloads the configured plugin, starts it and lists its sites.
func (p *Player) LoadSource() (*LoadResult, error) {
	s := p.GetSettings()
	if s.SourceURL == "" {
		return nil, errors.New("请先在设置中填写源地址")
	}
	node, err := catvod.FindNode(s.NodePath)
	if err != nil {
		return nil, err
	}
	p.log("下载源: " + s.SourceURL)
	src, err := catvod.Fetch(nil, s.SourceURL, p.dir)
	if err != nil {
		return nil, err
	}
	if src.MD5Mismatch() {
		p.log(fmt.Sprintf("md5 不一致: 文件 %s, 发布 %s", src.MD5, src.Expected))
		if s.StrictMD5 {
			return nil, fmt.Errorf("md5 校验失败（文件 %s ≠ 发布 %s）", src.MD5, src.Expected)
		}
	}
	p.StopSource()
	h := &catvod.Host{Node: node, Dir: filepath.Join(p.dir, "runtime"), Script: src.Path, ConfigFile: src.ConfigPath, LogFunc: p.log}
	if err := h.Start(90 * time.Second); err != nil {
		return nil, err
	}
	cfg, err := (&catvod.Client{Base: h.Base()}).Config()
	if err != nil {
		h.Stop()
		return nil, err
	}
	p.mu.Lock()
	p.host, p.sites, p.inited = h, cfg.Video.Sites, map[string]bool{}
	p.mu.Unlock()
	return &LoadResult{Sites: cfg.Video.Sites, MD5: src.MD5, ExpectedMD5: src.Expected, Mismatch: src.MD5Mismatch(), Node: node}, nil
}

// StopSource stops the running source process.
func (p *Player) StopSource() {
	p.mu.Lock()
	h := p.host
	p.host = nil
	p.mu.Unlock()
	if h != nil {
		h.Stop()
	}
}

func (p *Player) client(api string) (*catvod.Client, error) {
	p.mu.Lock()
	h := p.host
	need := !p.inited[api]
	p.inited[api] = true
	p.mu.Unlock()
	if h == nil || h.Base() == "" {
		return nil, errors.New("源未加载")
	}
	c := &catvod.Client{Base: h.Base()}
	if need && api != "" {
		if err := c.Init(api); err != nil {
			p.log("init " + api + ": " + err.Error())
		}
	}
	return c, nil
}

// Home returns the site's categories and filters.
func (p *Player) Home(api string) (catvod.M, error) {
	c, err := p.client(api)
	if err != nil {
		return nil, err
	}
	return c.Home(api)
}

// Category lists one page of a category.
func (p *Player) Category(api, tid string, page int, filters map[string]string) (catvod.M, error) {
	c, err := p.client(api)
	if err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	return c.Category(api, tid, page, filters)
}

// Search searches one site.
func (p *Player) Search(api, wd string, page int) (catvod.M, error) {
	c, err := p.client(api)
	if err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	return c.Search(api, wd, page)
}

// SearchAll searches every searchable site at once, streaming each site's
// answer to the page as it arrives.
func (p *Player) SearchAll(ctx context.Context, wd string, out *mygo.Channel[SiteResults]) error {
	p.mu.Lock()
	sites := append([]catvod.Site(nil), p.sites...)
	p.mu.Unlock()
	var wg sync.WaitGroup
	sem := make(chan struct{}, 6)
	for _, s := range sites {
		if !s.CanSearch() {
			continue
		}
		wg.Add(1)
		go func(s catvod.Site) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			r := SiteResults{Site: s}
			m, err := p.Search(s.API, wd, 1)
			if err != nil {
				r.Error = err.Error()
			} else if l, ok := m["list"].([]any); ok {
				r.List = l
			}
			if len(r.List) > 0 || r.Error != "" {
				out.Send(r)
			}
		}(s)
	}
	wg.Wait()
	return nil
}

// Detail returns a vod with its play lines.
func (p *Player) Detail(api, id string) (*DetailResult, error) {
	c, err := p.client(api)
	if err != nil {
		return nil, err
	}
	m, err := c.Detail(api, id)
	if err != nil {
		return nil, err
	}
	l, _ := m["list"].([]any)
	if len(l) == 0 {
		return nil, errors.New("详情为空")
	}
	vod, _ := l[0].(map[string]any)
	from, _ := vod["vod_play_from"].(string)
	urls, _ := vod["vod_play_url"].(string)
	return &DetailResult{Vod: vod, Lines: catvod.ParseLines(from, urls)}, nil
}

// Play resolves an episode to streams.
func (p *Player) Play(api, flag, id string) ([]catvod.Stream, error) {
	c, err := p.client(api)
	if err != nil {
		return nil, err
	}
	m, err := c.Play(api, flag, id)
	if err != nil {
		return nil, err
	}
	s := catvod.NormalizePlay(m)
	if len(s) == 0 {
		if e, ok := m["error"].(string); ok && e != "" {
			return nil, errors.New("源返回错误：" + e)
		}
		return nil, errors.New("没有可播放的地址")
	}
	var allowed []string
	bridgeAddr := p.bridgeAddr()
	if bridgeAddr != "" {
		allowed = append(allowed, bridgeAddr)
	}
	for i := range s {
		s[i].URL = catvod.LocalizeURL(s[i].URL, c.Base, bridgeAddr)
	}
	if err := catvod.CheckLocalStreams(s, c.Base, allowed...); err != nil {
		p.log("play: " + err.Error())
		return nil, err
	}
	return s, nil
}

// Open plays a stream with the configured player.
func (p *Player) Open(st catvod.Stream, title string) error {
	player := p.GetSettings().Player
	if st.Parse || catvod.IsWebPage(st.URL) {
		// A web page that needs sniffing: show it in a window.
		mygo.RunOnMain(func() {
			w := mygo.NewWindow(mygo.WindowOptions{Title: title, Width: 1100, Height: 700})
			w.Page().LoadURL(st.URL)
		})
		return nil
	}
	// One path for every URL, local or remote: everything goes through the
	// local proxy, which adds the stream's headers and rewrites HLS
	// playlists (absolute URLs, segments renamed to .ts so FFmpeg-based
	// players such as IINA accept disguised .jpg/.png segments). Loopback
	// URLs were already re-pointed at the current source by LocalizeURL.
	u := st.URL
	if p.proxy != nil && player != "system" {
		u = p.proxy.WrapAlways(u, st.Headers)
	}
	p.log(fmt.Sprintf("播放(%s): %s → %s headers=%v", player, st.URL, u, st.Headers))
	var pre map[string]string
	if u == st.URL {
		pre = st.Headers
	}
	html, err := catvod.Preflight(nil, u, pre)
	if err != nil {
		p.log("preflight: " + err.Error())
		return err
	}
	if html {
		// Any HTML page (not media): show it in a window.
		mygo.RunOnMain(func() {
			w := mygo.NewWindow(mygo.WindowOptions{Title: title, Width: 1100, Height: 700})
			w.Page().LoadURL(st.URL)
		})
		return nil
	}
	switch player {
	case "iina", "mpv":
		var name string
		var args []string
		if player == "iina" {
			name = findExe("/Applications/IINA.app/Contents/MacOS/iina-cli", "iina")
			if name == "" {
				return errors.New("未找到 IINA（/Applications/IINA.app）")
			}
			args = iinaArgs(u)
		} else {
			name = findExe("mpv", "/opt/homebrew/bin/mpv", "/usr/local/bin/mpv", "/Applications/mpv.app/Contents/MacOS/mpv")
			if name == "" {
				return errors.New("未找到 mpv（brew install mpv）")
			}
			args = mpvArgs(u, title)
		}
		p.log(name + " " + strings.Join(args, " "))
		cmd := exec.Command(name, args...)
		devnull, _ := os.Open(os.DevNull)
		cmd.Stdin = devnull
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
		return nil
	case "system":
		return mygo.Shell.OpenExternal(u)
	}
	mygo.RunOnMain(func() {
		w := mygo.NewWindow(mygo.WindowOptions{Title: title, Width: 1100, Height: 650})
		w.Page().LoadHTML(playerPage(u, title), "")
	})
	return nil
}

// iinaArgs: options before the URL, and --no-stdin. Without it iina-cli
// probes stdin, which is not a terminal when launched from an app, adds
// --stdin, and IINA shows "Cannot open file or stream!".
func iinaArgs(u string) []string {
	return []string{"--no-stdin", "--mpv-demuxer-lavf-o=extension_picky=0", u}
}

func mpvArgs(u, title string) []string {
	// extension_picky=0: accept HLS segments disguised as .jpg/.png.
	args := []string{"--force-window=immediate", "--title=" + title, "--demuxer-lavf-o=extension_picky=0"}
	if runtime.GOOS == "darwin" {
		args = append(args, "--macos-app-activation-policy=regular")
	}
	return append(args, "--", u)
}

func findExe(names ...string) string {
	for _, n := range names {
		if strings.Contains(n, "/") {
			if _, err := os.Stat(n); err == nil {
				return n
			}
		} else if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// playerPage is the built-in player: WKWebView plays HLS (m3u8) and MP4
// natively; streams that need headers go through the local HeaderProxy.
func playerPage(u, title string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><title>` + html.EscapeString(title) + `</title>
<style>html,body{margin:0;height:100%;background:#000}video{width:100%;height:100%}#e{position:fixed;top:8px;left:8px;color:#f88;font:13px system-ui}</style></head>
<body><video src="` + html.EscapeString(u) + `" controls autoplay playsinline></video><div id="e"></div>
<script>document.querySelector('video').addEventListener('error',()=>{document.getElementById('e').textContent='播放失败，可在设置里改用 IINA / mpv'})</script></body></html>`
}

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
	// Subscriptions are the plugin addresses the user added; Active is the
	// ID of the one currently loaded.
	Subscriptions []Subscription `json:"subscriptions"`
	Active        string         `json:"active"`
	// SourceURL is the pre-0.2 single address, migrated on load.
	SourceURL string `json:"sourceUrl,omitempty"`
	NodePath  string `json:"nodePath"`  // optional explicit node executable
	Player    string `json:"player"`    // builtin | potplayer | iina | mpv | system
	StrictMD5 bool   `json:"strictMd5"` // refuse to run when index.js.md5 differs
	// BridgeTemplate is where the built-in play bridge (/play?vid=) finds
	// media: {vid} and {res} are substituted. Empty disables the bridge.
	BridgeTemplate string `json:"bridgeTemplate"`
	// DisableContinuous turns off 连播 (playing the following episodes).
	DisableContinuous bool `json:"disableContinuous"`
	// AllowLAN binds the plugin's HTTP server to all interfaces so its
	// configuration page is reachable from other devices. Off by default:
	// the server listens on loopback only.
	AllowLAN bool `json:"allowLan"`
}

// LoadResult is what LoadSource reports to the page.
type LoadResult struct {
	Sites       []catvod.Site `json:"sites"`
	MD5         string        `json:"md5"`
	ExpectedMD5 string        `json:"expectedMd5"`
	Mismatch    bool          `json:"mismatch"`
	Node        string        `json:"node"`
	Sub         Subscription  `json:"subscription"`
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

	loadMu sync.Mutex // serializes LoadSource / switching
	eps    episodeServer
	histMu sync.Mutex

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
	_ = p.startEpisodeServer()
	if b, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
		_ = json.Unmarshal(b, &p.settings)
	}
	legacy := p.settings.SourceURL != ""
	p.settings.migrate()
	if legacy {
		_ = p.writeSettings(p.settings)
	}
	p.startBridge(p.settings.BridgeTemplate)
	if p.settings.Player == "" {
		p.settings.Player = defaultPlayer()
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

func (p *Player) writeSettings(s Settings) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(filepath.Join(p.dir, "settings.json"), b, 0o600)
}

// SaveSettings stores the general settings (player, node, md5, bridge).
// Subscriptions are managed by the Subscription methods and kept as they are.
func (p *Player) SaveSettings(s Settings) error {
	if s.Player == "" {
		s.Player = defaultPlayer()
	}
	p.mu.Lock()
	oldLAN := p.settings.AllowLAN
	s.Subscriptions, s.Active, s.SourceURL = p.settings.Subscriptions, p.settings.Active, ""
	p.mu.Unlock()
	if err := p.writeSettings(s); err != nil {
		return err
	}
	p.mu.Lock()
	p.settings = s
	p.mu.Unlock()
	p.startBridge(s.BridgeTemplate)
	// The plugin server binds its listen address at start; rebind on toggle.
	if oldLAN != s.AllowLAN && s.Active != "" {
		go func() {
			if _, err := p.LoadSource(); err != nil {
				p.log("重启源以应用局域网设置: " + err.Error())
			}
		}()
	}
	return nil
}

// editSubs applies f to a copy of the settings and saves the result.
func (p *Player) editSubs(f func(s *Settings) error) (Settings, error) {
	p.mu.Lock()
	s := p.settings
	s.Subscriptions = append([]Subscription(nil), s.Subscriptions...)
	p.mu.Unlock()
	if err := f(&s); err != nil {
		return Settings{}, err
	}
	if err := p.writeSettings(s); err != nil {
		return Settings{}, err
	}
	p.mu.Lock()
	p.settings = s
	p.mu.Unlock()
	return s, nil
}

// AddSubscription adds a plugin address. The first one becomes active.
func (p *Player) AddSubscription(name, url string) (Settings, error) {
	return p.editSubs(func(s *Settings) error { _, err := s.add(name, url); return err })
}

// UpdateSubscription renames a subscription or changes its address.
func (p *Player) UpdateSubscription(id, name, url string) (Settings, error) {
	return p.editSubs(func(s *Settings) error { return s.update(id, name, url) })
}

// RemoveSubscription deletes a subscription and its downloaded files. When
// it was the active one, its plugin process is stopped.
func (p *Player) RemoveSubscription(id string) (Settings, error) {
	wasActive := p.GetSettings().Active == id
	s, err := p.editSubs(func(s *Settings) error { return s.remove(id) })
	if err != nil {
		return s, err
	}
	if wasActive {
		p.loadMu.Lock()
		p.StopSource()
		p.loadMu.Unlock()
	}
	if id != "" && !strings.ContainsAny(id, `/\.`) {
		_ = os.RemoveAll(filepath.Join(p.dir, "subs", id))
	}
	return s, nil
}

// SwitchSubscription makes id the active subscription and reloads it.
func (p *Player) SwitchSubscription(id string) (*LoadResult, error) {
	if _, err := p.editSubs(func(s *Settings) error {
		if s.find(id) < 0 {
			return errNoSub
		}
		s.Active = id
		return nil
	}); err != nil {
		return nil, err
	}
	return p.LoadSource()
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

// LoadSource downloads the active subscription's plugin, starts it and
// lists its sites. Any previous plugin process is stopped first.
func (p *Player) LoadSource() (*LoadResult, error) {
	p.loadMu.Lock()
	defer p.loadMu.Unlock()
	s := p.GetSettings()
	sub, ok := s.active()
	if !ok {
		p.StopSource()
		return nil, errors.New("请先添加一个订阅")
	}
	// Each subscription keeps its own plugin copy and data files.
	dir := filepath.Join(p.dir, "subs", sub.ID)
	node, err := catvod.FindNode(s.NodePath)
	if err != nil {
		return nil, err
	}
	p.log("加载订阅「" + sub.Name + "」: " + sub.URL)
	src, err := catvod.Fetch(nil, sub.URL, dir)
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
	h := &catvod.Host{Node: node, Dir: filepath.Join(dir, "runtime"), Script: src.Path, ConfigFile: src.ConfigPath, LogFunc: p.log, ListenLAN: s.AllowLAN}
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
	return &LoadResult{Sites: cfg.Video.Sites, MD5: src.MD5, ExpectedMD5: src.Expected, Mismatch: src.MD5Mismatch(), Node: node, Sub: sub}, nil
}

// StopSource stops the running source process.
func (p *Player) StopSource() {
	p.mu.Lock()
	h := p.host
	p.host, p.sites, p.inited = nil, nil, map[string]bool{}
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
	c := &catvod.Client{Base: h.Base(), LAN: p.GetSettings().AllowLAN}
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
	catvod.LocalizePlayStreams(s, c.Base, bridgeAddr, c.LAN)
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
	if player == "builtin" || player == "" {
		p.openBuiltin(title, playerPage(u, title))
		return nil
	}
	return p.launch(player, u, title)
}

func (p *Player) openBuiltin(title, page string) {
	mygo.RunOnMain(func() {
		w := mygo.NewWindow(mygo.WindowOptions{Title: title, Width: 1100, Height: 650})
		w.Page().LoadHTML(page, "")
	})
}

// launch opens u (a stream or an M3U playlist) in an external player.
func (p *Player) launch(player, u, title string) error {
	switch player {
	case "potplayer":
		if runtime.GOOS != "windows" {
			return errors.New("PotPlayer 只支持 Windows，请改用 mpv")
		}
		exe := findPotPlayer()
		if exe == "" {
			return errors.New("未找到 PotPlayer（PotPlayerMini64.exe）：请安装，或在设置里改用 mpv")
		}
		cmd := potPlayerCommand(exe, u, title)
		p.log(potPlayerCmdLine(exe, u, title))
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
		return nil
	case "iina", "mpv":
		var name string
		var args []string
		if player == "iina" {
			if runtime.GOOS != "darwin" {
				return errors.New("IINA 只支持 macOS，请改用 mpv")
			}
			name = findExe("/Applications/IINA.app/Contents/MacOS/iina-cli", "iina")
			if name == "" {
				return errors.New("未找到 IINA（/Applications/IINA.app）")
			}
			args = iinaArgs(u)
		} else {
			name = findExe(mpvCandidates()...)
			if name == "" {
				return errors.New(mpvHint())
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
	return fmt.Errorf("未知播放器: %s", player)
}

// iinaArgs: options before the URL, and --no-stdin. Without it iina-cli
// probes stdin, which is not a terminal when launched from an app, adds
// --stdin, and IINA shows "Cannot open file or stream!".
func iinaArgs(u string) []string {
	// prefetch-playlist: open the next playlist entry before this one
	// ends, so 连播 switches without a pause.
	// IINA's CLI forwards single-dash key/value pairs to NSArgumentDomain.
	// Keep the override scoped to this launch and avoid an empty mpv value:
	// IINA's CLI parser drops empty values after '='.
	return []string{"--no-stdin", "-disableOSDFileStartMsg", "YES", "--mpv-demuxer-lavf-o=extension_picky=0", "--mpv-prefetch-playlist=yes", u}
}

func mpvArgs(u, title string) []string {
	// extension_picky=0: accept HLS segments disguised as .jpg/.png.
	// No --title for playlists: mpv then shows each entry's #EXTINF title.
	// prefetch-playlist: open the next 连播 entry before this one ends.
	// osd-playing-msg / term-playing-msg empty: never print the entry URL.
	args := []string{"--force-window=immediate", "--demuxer-lavf-o=extension_picky=0", "--prefetch-playlist=yes", "--osd-playing-msg=", "--term-playing-msg="}
	if title != "" {
		args = append(args, "--title="+title)
	}
	if runtime.GOOS == "darwin" {
		args = append(args, "--macos-app-activation-policy=regular")
	}
	return append(args, "--", u)
}

// defaultPlayer: the macOS web view plays HLS itself; on Windows and Linux
// the web views generally do not, so an installed external player is the
// better default there: PotPlayer (Windows), then mpv, then built-in.
func defaultPlayer() string {
	return pickDefaultPlayer(runtime.GOOS, findPotPlayer() != "", findExe(mpvCandidates()...) != "")
}

func pickDefaultPlayer(goos string, hasPot, hasMPV bool) string {
	switch {
	case goos == "darwin":
		return "builtin"
	case goos == "windows" && hasPot:
		return "potplayer"
	case hasMPV:
		return "mpv"
	}
	return "builtin"
}

// mpvCandidates lists where mpv usually lives on each platform; PATH is
// checked first. GUI apps on macOS start with a minimal PATH.
func mpvCandidates() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return []string{"mpv", "/opt/homebrew/bin/mpv", "/usr/local/bin/mpv", "/Applications/mpv.app/Contents/MacOS/mpv"}
	case "windows":
		c := []string{"mpv.exe", "mpv"}
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
			if d := os.Getenv(env); d != "" {
				c = append(c, filepath.Join(d, "mpv", "mpv.exe"), filepath.Join(d, "Programs", "mpv", "mpv.exe"))
			}
		}
		if home != "" {
			c = append(c, filepath.Join(home, "scoop", "apps", "mpv", "current", "mpv.exe"))
		}
		if d := os.Getenv("ChocolateyInstall"); d != "" {
			c = append(c, filepath.Join(d, "bin", "mpv.exe"))
		}
		return c
	default:
		return []string{"mpv", "/usr/bin/mpv", "/usr/local/bin/mpv", "/snap/bin/mpv", "/var/lib/flatpak/exports/bin/io.mpv.Mpv"}
	}
}

func mpvHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "未找到 mpv（brew install mpv）"
	case "windows":
		return "未找到 mpv：请安装 mpv（如 scoop install mpv）并加入 PATH"
	default:
		return "未找到 mpv：请安装 mpv（如 sudo apt install mpv）"
	}
}

func findExe(names ...string) string {
	for _, n := range names {
		if filepath.IsAbs(n) {
			if _, err := os.Stat(n); err == nil {
				return n
			}
		} else if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// playerPage is the built-in player: the macOS web view plays HLS (m3u8)
// and MP4 natively, other platforms' web views mostly MP4 only; streams that need headers go through the local HeaderProxy.
func playerPage(u, title string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><title>` + html.EscapeString(title) + `</title>
<style>html,body{margin:0;height:100%;background:#000}video{width:100%;height:100%}#e{position:fixed;top:8px;left:8px;color:#f88;font:13px system-ui}</style></head>
<body><video src="` + html.EscapeString(u) + `" controls autoplay playsinline></video><div id="e"></div>
<script>document.querySelector('video').addEventListener('error',()=>{document.getElementById('e').textContent='播放失败，可在设置里改用 mpv 或 IINA'})</script></body></html>`
}

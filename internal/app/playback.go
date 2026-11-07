package app

import (
	"errors"
	"fmt"
	"runtime"
	"strings"

	"github.com/egoist/mygo"
	"github.com/freelxs/jstv-player/internal/catvod"
	"github.com/freelxs/jstv-player/internal/externalplayer"
)

// Open plays a stream with the configured player.
func (p *Player) Open(st catvod.Stream, title string) error {
	if p.webMode.Load() {
		return errors.New("网页模式请使用浏览器播放")
	}
	player := p.GetSettings().Player
	if st.Parse || catvod.IsWebPage(st.URL) {
		// A web page that needs sniffing: show it in a window.
		p.openWebPage(title, st.URL)
		return nil
	}
	// One path for every URL, local or remote: everything goes through the
	// local proxy, which adds the stream's headers and rewrites HLS
	// playlists (absolute URLs, segments renamed to .ts so FFmpeg-based
	// players such as IINA accept disguised .jpg/.png segments). Loopback
	// URLs were already re-pointed at the current source by LocalizeURL.
	u := st.URL
	if player != "system" {
		if p.proxy == nil {
			return errors.New("本地播放代理未启动")
		}
		var err error
		u, err = p.proxy.WrapGranted(u, st.Headers)
		if err != nil {
			return err
		}
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
		p.openWebPage(title, st.URL)
		return nil
	}
	if player == "builtin" || player == "" {
		p.openBuiltin(title, playerPage(u, title))
		return nil
	}
	return p.launch(player, u, title)
}

func (p *Player) openBuiltin(title, page string) {
	if p.webMode.Load() {
		return
	}
	base := playbackPageBase(p.eps.base)
	mygo.RunOnMain(func() {
		p.builtinMu.Lock()
		old := p.builtinWin
		p.builtinWin = nil
		p.builtinMu.Unlock()
		if old != nil {
			old.Close()
		}
		w := mygo.NewWindow(mygo.WindowOptions{Title: title, Width: 1100, Height: 650})
		w.OnClosed(func() {
			p.builtinMu.Lock()
			if p.builtinWin == w {
				p.builtinWin = nil
			}
			p.builtinMu.Unlock()
		})
		p.builtinMu.Lock()
		p.builtinWin = w
		p.builtinMu.Unlock()
		w.Page().LoadHTML(page, base)
	})
}

// closeBuiltin closes the in-window player. It is safe to call when none
// is open. Do not call it from a unit test: Window.Close waits on the UI
// thread, and the test process is not running the GUI loop.
func (p *Player) closeBuiltin() {
	p.builtinMu.Lock()
	w := p.builtinWin
	p.builtinMu.Unlock()
	if w != nil {
		w.Close()
	}
}

// launch opens u (a stream or an M3U playlist) in an external player.
func (p *Player) launch(player, u, title string) error {
	switch player {
	case "system":
		return mygo.Shell.OpenExternal(u)
	case "potplayer":
		if runtime.GOOS != "windows" {
			return errors.New("PotPlayer 只支持 Windows，请改用 mpv")
		}
		exe := externalplayer.FindPotPlayer()
		if exe == "" {
			return errors.New("未找到 PotPlayer（PotPlayerMini64.exe）：请安装，或在设置里改用 mpv")
		}
		cmd := externalplayer.PotPlayerCommand(exe, u, title)
		p.log(externalplayer.PotPlayerCmdLine(exe, u, title))
		if err := cmd.Start(); err != nil {
			return err
		}
		go cmd.Wait()
		return nil
	}
	name, args, err := p.externalCommand(player, u, title)
	if err != nil {
		return err
	}
	p.log(name + " " + strings.Join(args, " "))
	return externalplayer.RunExternal(name, args)
}

func (p *Player) externalCommand(player, u, title string) (string, []string, error) {
	switch player {
	case "iina":
		if runtime.GOOS != "darwin" {
			return "", nil, errors.New("IINA 只支持 macOS，请改用 mpv")
		}
		name := externalplayer.FindExe("/Applications/IINA.app/Contents/MacOS/iina-cli", "iina")
		if name == "" {
			return "", nil, errors.New("未找到 IINA（/Applications/IINA.app）")
		}
		return name, externalplayer.IinaArgs(u), nil
	case "mpv":
		name := externalplayer.FindExe(externalplayer.MpvCandidates()...)
		if name == "" {
			return "", nil, errors.New(externalplayer.MpvHint())
		}
		return name, externalplayer.MpvArgs(u, title), nil
	case "senplayer":
		if runtime.GOOS != "darwin" {
			return "", nil, errors.New("SenPlayer 只支持 macOS，请改用 mpv")
		}
		if externalplayer.FindSenPlayer() == "" {
			return "", nil, errors.New("未找到 SenPlayer（/Applications/SenPlayer.app）")
		}
		return "open", externalplayer.SenPlayerOpenArgs(u), nil
	case "custom":
		s := p.GetSettings()
		return externalplayer.CustomLaunch(s.CustomPlayer, s.CustomArgs, u)
	default:
		return "", nil, fmt.Errorf("未知播放器: %s", player)
	}
}

// playerPage is one already-resolved stream in the in-window player.
func playerPage(u, title string) string {
	return builtinPlayerPage([]builtinItem{{Title: title, URL: u}}, 0)
}

// builtinPlaybackHint names a player the current OS can actually launch.
func builtinPlaybackHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "播放失败，可在设置里改用 IINA、SenPlayer 或 mpv"
	case "windows":
		return "播放失败，可在设置里改用 PotPlayer 或 mpv"
	default:
		return "播放失败，可在设置里改用 mpv"
	}
}

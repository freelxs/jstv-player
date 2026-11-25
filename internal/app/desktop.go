package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// DesktopHooks connects desktop-only MyGo APIs without invoking GUI/App
// singletons during web mode or unit tests.
type DesktopHooks struct {
	KeepRunning         func(reason string) func()
	KeepAwake           func(reason string, display bool) func()
	NowPlayingSet       func(info mygo.NowPlayingInfo)
	NowPlayingClear     func()
	UpdaterEnabled      func() bool
	UpdaterCheck        func(ctx context.Context) (*mygo.Update, error)
	Relaunch            func(args ...string)
	OpenExternal        func(u string) error
	CompileContentRules func(id, rules string) (*mygo.ContentRules, error)
	EmbedBuiltin        func(title, page, base string) bool
}

// DefaultDesktopHooks returns hooks backed by the live MyGo desktop runtime.
func DefaultDesktopHooks() DesktopHooks {
	return DesktopHooks{
		KeepRunning:         mygo.Power.KeepRunning,
		KeepAwake:           mygo.Power.KeepAwake,
		NowPlayingSet:       mygo.NowPlaying.Set,
		NowPlayingClear:     mygo.NowPlaying.Clear,
		UpdaterEnabled:      mygo.Updater.Enabled,
		UpdaterCheck:        mygo.Updater.Check,
		Relaunch:            mygo.App.Relaunch,
		OpenExternal:        mygo.Shell.OpenExternal,
		CompileContentRules: mygo.CompileContentRules,
	}
}

func (p *Player) setKeepRunning(active bool) {
	if p.webMode.Load() || p.desktop.KeepRunning == nil {
		return
	}
	p.powerMu.Lock()
	if active {
		if p.keepRunningRelease != nil {
			p.powerMu.Unlock()
			return
		}
		p.powerMu.Unlock()
		release := p.desktop.KeepRunning("XsPlayer 本地媒体代理运行中")
		if release == nil {
			return
		}
		p.powerMu.Lock()
		if p.keepRunningRelease == nil {
			p.keepRunningRelease = release
			release = nil
		}
		p.powerMu.Unlock()
		if release != nil {
			release()
		}
		return
	}
	release := p.keepRunningRelease
	p.keepRunningRelease = nil
	p.powerMu.Unlock()
	if release != nil {
		release()
	}
}

func (p *Player) setBuiltinAwake(active bool) {
	if p.webMode.Load() || p.desktop.KeepAwake == nil {
		return
	}
	p.powerMu.Lock()
	if active {
		if p.builtinAwakeRelease != nil {
			p.powerMu.Unlock()
			return
		}
		p.powerMu.Unlock()
		release := p.desktop.KeepAwake("XsPlayer 正在播放视频", true)
		if release == nil {
			return
		}
		p.powerMu.Lock()
		if p.builtinAwakeRelease == nil {
			p.builtinAwakeRelease = release
			release = nil
		}
		p.powerMu.Unlock()
		if release != nil {
			release()
		}
		return
	}
	release := p.builtinAwakeRelease
	p.builtinAwakeRelease = nil
	p.powerMu.Unlock()
	if release != nil {
		release()
	}
}

type builtinPlayerState struct {
	Title    string  `json:"title"`
	Playing  bool    `json:"playing"`
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
	HasPrev  bool    `json:"hasPrev"`
	HasNext  bool    `json:"hasNext"`
}

func (p *Player) updateNowPlaying(state builtinPlayerState) {
	if p.webMode.Load() || p.desktop.NowPlayingSet == nil {
		return
	}
	title := strings.TrimSpace(state.Title)
	if title == "" {
		title = "XsPlayer"
	}
	pbState := mygo.PlaybackPaused
	if state.Playing {
		pbState = mygo.PlaybackPlaying
	}
	cmds := mygo.MediaPlay | mygo.MediaPause | mygo.MediaStop
	if state.HasPrev {
		cmds |= mygo.MediaPrevious
	}
	if state.HasNext {
		cmds |= mygo.MediaNext
	}
	var dur, pos time.Duration
	if !math.IsNaN(state.Duration) && !math.IsInf(state.Duration, 0) && state.Duration > 0 && state.Duration <= 30*24*3600 {
		dur = time.Duration(state.Duration * float64(time.Second))
		cmds |= mygo.MediaSeek
	}
	if !math.IsNaN(state.Position) && !math.IsInf(state.Position, 0) && state.Position > 0 {
		pos = time.Duration(state.Position * float64(time.Second))
		if dur > 0 && pos > dur {
			pos = dur
		}
	}
	p.desktop.NowPlayingSet(mygo.NowPlayingInfo{
		Title:    title,
		Artist:   "XsPlayer",
		Duration: dur,
		Position: pos,
		State:    pbState,
		Commands: cmds,
	})
}

func (p *Player) clearNowPlaying() {
	if p.webMode.Load() || p.desktop.NowPlayingClear == nil {
		return
	}
	p.desktop.NowPlayingClear()
}

func (p *Player) servePlayerState(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 4097))
	if err != nil || len(data) > 4096 {
		http.Error(w, "状态请求无效", http.StatusBadRequest)
		return
	}
	var state builtinPlayerState
	if json.Unmarshal(data, &state) != nil {
		http.Error(w, "状态请求无效", http.StatusBadRequest)
		return
	}
	p.updateNowPlaying(state)
	w.WriteHeader(http.StatusNoContent)
}

func (p *Player) handleMediaCommand(cmd mygo.MediaCommand, pos time.Duration) {
	if p.webMode.Load() {
		return
	}
	p.builtinMu.Lock()
	w := p.builtinWin
	p.builtinMu.Unlock()
	if w == nil {
		return
	}
	var script string
	switch cmd {
	case mygo.MediaPlay:
		script = `window.__jstvMediaCmd && window.__jstvMediaCmd("play")`
	case mygo.MediaPause:
		script = `window.__jstvMediaCmd && window.__jstvMediaCmd("pause")`
	case mygo.MediaNext:
		script = `window.__jstvMediaCmd && window.__jstvMediaCmd("next")`
	case mygo.MediaPrevious:
		script = `window.__jstvMediaCmd && window.__jstvMediaCmd("prev")`
	case mygo.MediaStop:
		script = `window.__jstvMediaCmd && window.__jstvMediaCmd("stop")`
	case mygo.MediaSeek:
		sec := pos.Seconds()
		if math.IsNaN(sec) || math.IsInf(sec, 0) || sec < 0 {
			sec = 0
		}
		script = fmt.Sprintf(`window.__jstvMediaCmd && window.__jstvMediaCmd("seek", %.3f)`, sec)
	default:
		return
	}
	mygo.RunOnMain(func() {
		p.builtinMu.Lock()
		cur := p.builtinWin
		p.builtinMu.Unlock()
		if cur != nil {
			_, _ = cur.Page().Eval(script)
		}
	})
}

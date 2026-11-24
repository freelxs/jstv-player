// JSTV Player: a desktop player for CatPawOpen-compatible JS plugins.
package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"os"
	"runtime"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/freelxs/jstv-player/internal/app"
	"github.com/freelxs/jstv-player/internal/webserver"
)

//go:embed all:web webassets
var webFS embed.FS

//go:embed mygo.json
var mygoConfigJSON []byte

func embeddedAppVersion() string {
	var cfg struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(mygoConfigJSON, &cfg) == nil {
		return cfg.Version
	}
	return ""
}

func main() {
	if webserver.Enabled() {
		if err := webserver.RunWithVersion(webFS, embeddedAppVersion()); err != nil {
			log.Fatal(err)
		}
		return
	}
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	web, _ := fs.Sub(webFS, "web")
	mygo.SetFrontend(web)

	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		log.Fatal(err)
	}
	_ = os.MkdirAll(dir, 0o755)
	rt := app.New(dir, webFS, false)
	rt.SetAppVersion(embeddedAppVersion())
	rt.EnableDesktopIntegration(app.DefaultDesktopHooks())
	mygo.Bind(rt.Player)
	mygo.NowPlaying.OnCommand(func(cmd mygo.MediaCommand, pos time.Duration) {
		rt.HandleMediaCommand(cmd, pos)
	})

	var mainWinMu sync.Mutex
	var mainWin *mygo.Window
	mygo.App.OnSecondInstance(func(_ []string, _ string) {
		mainWinMu.Lock()
		win := mainWin
		mainWinMu.Unlock()
		if win != nil {
			win.Show()
			win.Focus()
		}
	})

	mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
		rt.Player.StopSource()
		mygo.NowPlaying.Clear()
	})
	mygo.App.WhenReady(func() {
		go rt.InitPrivacyRules()
		win := mygo.NewWindow(mygo.WindowOptions{Title: "XsPlayer", URL: "/", Width: 1280, Height: 820, MinWidth: 900, MinHeight: 600})
		mainWinMu.Lock()
		mainWin = win
		mainWinMu.Unlock()
		evalMain := func(script string) {
			mainWinMu.Lock()
			target := mainWin
			mainWinMu.Unlock()
			if target == nil {
				return
			}
			target.Show()
			target.Focus()
			_, _ = target.Page().Eval(script)
		}
		installDesktopMenu(evalMain)
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func installDesktopMenu(evalMain func(string)) {
	openUpdate := func(*mygo.MenuItem, *mygo.Window) {
		evalMain(`window.__jstvOpenUpdate && window.__jstvOpenUpdate()`)
	}
	openSettings := func(*mygo.MenuItem, *mygo.Window) {
		evalMain(`window.__jstvOpenSettings && window.__jstvOpenSettings()`)
	}
	reloadSource := func(*mygo.MenuItem, *mygo.Window) {
		evalMain(`window.__jstvReloadSource && window.__jstvReloadSource()`)
	}
	if runtime.GOOS == "darwin" {
		mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
			{Label: "XsPlayer", Submenu: []*mygo.MenuItem{
				{Role: mygo.RoleAbout},
				{Label: "检查更新…", Click: openUpdate},
				mygo.Separator(),
				{Label: "设置…", Accelerator: "CmdOrCtrl+,", Click: openSettings},
				mygo.Separator(),
				{Role: mygo.RoleServices},
				mygo.Separator(),
				{Role: mygo.RoleHide},
				{Role: mygo.RoleHideOthers},
				{Role: mygo.RoleUnhide},
				mygo.Separator(),
				{Role: mygo.RoleQuit},
			}},
			{Role: mygo.RoleEditMenu},
			{Label: "显示", Submenu: []*mygo.MenuItem{
				{Label: "重新加载订阅", Accelerator: "CmdOrCtrl+R", Click: reloadSource},
				mygo.Separator(),
				{Role: mygo.RoleToggleFullScreen},
			}},
			{Role: mygo.RoleWindowMenu},
		}))
		return
	}
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Label: "文件", Submenu: []*mygo.MenuItem{
			{Label: "重新加载订阅", Accelerator: "CmdOrCtrl+R", Click: reloadSource},
			{Label: "设置…", Accelerator: "CmdOrCtrl+,", Click: openSettings},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}},
		{Role: mygo.RoleEditMenu},
		{Label: "帮助", Submenu: []*mygo.MenuItem{
			{Label: "检查更新…", Click: openUpdate},
		}},
	}))
}

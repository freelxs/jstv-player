// JSTV Player: a desktop player for CatPawOpen-compatible JS plugins.
package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"log"
	"os"
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
		win := mygo.NewWindow(mygo.WindowOptions{Title: "JSTV Player", URL: "/", Width: 1280, Height: 820, MinWidth: 900, MinHeight: 600})
		mainWinMu.Lock()
		mainWin = win
		mainWinMu.Unlock()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

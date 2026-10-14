// JSTV Player: a desktop player for CatPawOpen-compatible JS plugins.
package main

import (
	"embed"
	"io/fs"
	"log"
	"os"

	"github.com/egoist/mygo"
)

//go:embed all:web
var webFS embed.FS

func main() {
	web, _ := fs.Sub(webFS, "web")
	mygo.SetFrontend(web)

	dir, err := mygo.App.Path(mygo.PathUserData)
	if err != nil {
		log.Fatal(err)
	}
	_ = os.MkdirAll(dir, 0o755)
	p := newPlayer(dir)
	mygo.Bind(p)

	mygo.App.OnWillQuit(func(*mygo.QuitEvent) { p.StopSource() })
	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{Title: "JSTV Player", URL: "/", Width: 1280, Height: 820, MinWidth: 900, MinHeight: 600})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

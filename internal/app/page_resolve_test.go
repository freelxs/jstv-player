package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/freelxs/jstv-player/internal/catvod"
	"github.com/freelxs/jstv-player/internal/tvbox"
)

type loopHost struct{ base string }

func (h loopHost) Base() string { return h.base }
func (h loopHost) Stop()        {}

func TestResolvedPageJoinsContinuousPlayback(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vod/init":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		case "/vod/play":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"parse":0,"jx":1,"url":"` + srv.URL + `/page"}`))
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<!doctype html><p>no media</p>`))
		case "/json":
			if r.URL.Query().Get("url") == "" {
				http.Error(w, "missing", 400)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"url":"` + srv.URL + `/media.m3u8"}`))
		case "/media.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:1,\nseg.ts\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	if err := p.SaveSettings(Settings{Player: "builtin"}); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.host = loopHost{base: srv.URL}
	p.vodConfig = &tvbox.Config{Parses: []tvbox.Parser{{
		Name: "JSON", Type: 1, URL: srv.URL + "/json?url=", Flags: []string{"线路"},
	}}}
	p.mu.Unlock()
	eps := []catvod.Episode{{Name: "第1集", ID: "e1"}, {Name: "第2集", ID: "e2"}}
	var page string
	restorePlayback(t, func(_ *Player, player, media, title, builtinPage string) error {
		page = builtinPage
		if player != "builtin" || media != "" || !strings.Contains(title, "第1集") {
			t.Fatalf("player=%s media=%s title=%s", player, media, title)
		}
		return nil
	})
	streams, err := p.PlayEpisodes(context.Background(), "/vod", "show", "示例", "线路", eps, 0, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 0 || !strings.Contains(page, "/ep/") || strings.Contains(page, "/page\"") {
		t.Fatalf("streams=%d\n%s", len(streams), page)
	}
	p.webMode.Store(true)
	web, err := p.WebPlay(context.Background(), "/vod", "show", "示例", "线路", eps, 0, "")
	if err != nil || web.Action != "play" || web.PlaylistID == "" || web.Count != 2 {
		t.Fatalf("web %+v %v", web, err)
	}
}

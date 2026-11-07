package app

import (
	"strings"
	"testing"

	"github.com/freelxs/jstv-player/internal/catvod"
)

func TestPublicMediaPath(t *testing.T) {
	got, ok := publicMediaPath("http://127.0.0.1:9", "http://127.0.0.1:9/p/abc/stream.m3u8")
	if !ok || got != "/media/p/abc/stream.m3u8" {
		t.Fatalf("%q %v", got, ok)
	}
	if _, ok := publicMediaPath("http://127.0.0.1:9", "http://127.0.0.1:8/p/abc/a.ts"); ok {
		t.Fatal("accepted a different proxy origin")
	}
	if _, ok := publicMediaPath("http://127.0.0.1:9", "http://127.0.0.1:9/ep/1"); ok {
		t.Fatal("accepted a non-proxy path")
	}

}

func TestWebModeDoesNotOpenDesktopPlayer(t *testing.T) {
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	p.webMode.Store(true)
	if err := p.Open(catvod.Stream{URL: "https://example.com/a.mp4"}, "t"); err == nil || !strings.Contains(err.Error(), "网页") {
		t.Fatal(err)
	}
	if _, err := p.PlayEpisodes("/api", "v", "show", "line", []catvod.Episode{{Name: "1", ID: "e"}}, 0); err == nil {
		t.Fatal("PlayEpisodes launched from web mode")
	}
	p.webMode.Store(false)
	if _, err := p.WebPlay("/api", "v", "show", "line", []catvod.Episode{{Name: "1", ID: "e"}}, 0); err == nil || !strings.Contains(err.Error(), "仅网页") {
		t.Fatal(err)
	}
}

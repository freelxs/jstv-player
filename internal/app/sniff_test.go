package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/freelxs/jstv-player/internal/catvod"
)

func TestDecodeSniffKeepsHTTPMediaAndReferer(t *testing.T) {
	got, ok := decodeSniff(map[string]any{
		"url": "https://cdn.example/a.m3u8", "referer": "https://example.com/play",
		"headers": map[string]any{"Origin": "https://example.com"},
	})
	if !ok || got.URL != "https://cdn.example/a.m3u8" || got.Headers["Referer"] != "https://example.com/play" || got.Headers["Origin"] != "https://example.com" {
		t.Fatalf("%+v %v", got, ok)
	}
	if _, ok := decodeSniff(map[string]any{"url": "file:///tmp/a.mp4"}); ok {
		t.Fatal("file url accepted")
	}
	if _, ok := decodeSniff(map[string]any{"url": "https://example.com/page"}); ok {
		t.Fatal("page url accepted")
	}
	if !strings.Contains(sniffScript, "XMLHttpRequest") || !strings.Contains(sniffScript, "PerformanceObserver") || strings.Contains(sniffScript, "mygo.") {
		t.Fatal("sniff script lost a hook or calls Go")
	}
}

func TestDesktopSniffReplacesAnUnresolvedPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/vod/init":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
		case "/vod/play":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"parse":1,"url":"` + srvURL(r) + `/page"}`))
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			_, _ = w.Write([]byte(`<html>player</html>`))
		case "/media.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:1,\nseg.ts\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	prev := sniffPage
	sniffPage = func(ctx context.Context, page string, headers map[string]string) (sniffed, error) {
		if !strings.HasSuffix(page, "/page") {
			t.Fatalf("sniffed %s", page)
		}
		return sniffed{URL: srv.URL + "/media.m3u8", Headers: map[string]string{"Referer": page}}, nil
	}
	t.Cleanup(func() { sniffPage = prev })
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	if err := p.SaveSettings(Settings{Player: "builtin"}); err != nil {
		t.Fatal(err)
	}
	p.mu.Lock()
	p.host = loopHost{base: srv.URL}
	p.mu.Unlock()
	var queued bool
	restorePlayback(t, func(_ *Player, player, _, title, page string) error {
		queued = player == "builtin" && strings.Contains(title, "第1集") && strings.Contains(page, "/ep/")
		return nil
	})
	eps := []catvod.Episode{{Name: "第1集", ID: "e1"}, {Name: "第2集", ID: "e2"}}
	streams, err := p.PlayEpisodes("/vod", "show", "示例", "线路", eps, 0, "")
	if err != nil || len(streams) != 0 || !queued {
		t.Fatalf("streams=%d queued=%t err=%v", len(streams), queued, err)
	}
	p.webMode.Store(true)
	called := false
	sniffPage = func(context.Context, string, map[string]string) (sniffed, error) {
		called = true
		return sniffed{}, errors.New("should not sniff")
	}
	web, err := p.WebPlay("/vod", "show", "示例", "线路", eps, 0, "")
	if err != nil || web.Action != "page" || called {
		t.Fatalf("web %+v called=%t err=%v", web, called, err)
	}
}

func TestSniffTimeoutIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/init") {
			_, _ = w.Write([]byte(`{}`))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/play") {
			_, _ = w.Write([]byte(`{"parse":1,"url":"` + srvURL(r) + `/page.html"}`))
			return
		}
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<html>empty</html>`))
	}))
	defer srv.Close()
	prev := sniffPage
	sniffPage = func(context.Context, string, map[string]string) (sniffed, error) {
		return sniffed{}, errors.New("播放页在 12 秒内没有出现可播放的视频")
	}
	t.Cleanup(func() { sniffPage = prev })
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	p.mu.Lock()
	p.host = loopHost{base: srv.URL}
	p.mu.Unlock()
	_, err := p.Play("/vod", "线路", "e1")
	if err == nil || !strings.Contains(err.Error(), "12 秒") {
		t.Fatal(err)
	}
}

func srvURL(r *http.Request) string {
	return "http://" + r.Host
}

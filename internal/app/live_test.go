package app

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/freelxs/jstv-player/internal/catvod"
)

func TestNativeLiveNeedsNoNodeAndPlaysHLSWithHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/channels":
			fmt.Fprint(w, "#EXTM3U\n#EXTINF:-1 group-title=\"新闻\",频道一\n/play.php?id=a$b%23c|User-Agent=live-fixture&Referer=https%3A%2F%2Fexample.com\n")
		case "/play.php":
			if r.UserAgent() != "live-fixture" || r.Referer() != "https://example.com" || r.URL.Query().Get("id") != "a$b#c" {
				http.Error(w, "headers or special query lost", 400)
				return
			}
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-TARGETDURATION:6\n#EXTINF:6,\nseg.ts\n")
		case "/seg.ts":
			if r.UserAgent() != "live-fixture" || r.Referer() != "https://example.com" {
				http.Error(w, "segment headers missing", 400)
				return
			}
			fmt.Fprint(w, "fixture-segment")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	p.webMode.Store(true)
	if err := p.SaveSettings(Settings{NodePath: "/nonexistent-node", AllowLANMedia: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AddSubscription("直播", server.URL+"/channels"); err != nil {
		t.Fatal(err)
	}
	loaded, err := p.LoadSource()
	if err != nil || loaded.TrustRequired || len(loaded.Sites) != 1 || p.host != nil {
		t.Fatalf("native live used JS host: %+v %v", loaded, err)
	}
	api := loaded.Sites[0].API
	home, err := p.Home(api)
	if err != nil || len(home["class"].([]any)) != 1 {
		t.Fatalf("home: %v %v", home, err)
	}
	category, err := p.Category(api, "新闻", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := category["list"].([]any)[0].(catvod.M)["vod_id"].(string)
	detail, err := p.Detail(api, id)
	if err != nil {
		t.Fatal(err)
	}
	playback, err := p.WebPlay(api, id, "频道一", "直播", detail.Lines[0].Episodes, 0)
	if err != nil || playback.Media != "hls" || !strings.HasPrefix(playback.URL, "/media/") {
		t.Fatalf("native playback: %+v %v", playback, err)
	}
	streamURL := p.proxy.BaseURL() + strings.TrimPrefix(playback.URL, "/media")
	resp, err := http.Get(streamURL)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var segment string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "http") {
			segment = line
		}
	}
	if segment == "" {
		t.Fatalf("playlist not rewritten: %s", data)
	}
	resp, err = http.Get(segment)
	if err != nil {
		t.Fatal(err)
	}
	data, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(data) != "fixture-segment" {
		t.Fatalf("segment: %s", data)
	}
	p.StopSource()
	resp, err = http.Get(streamURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatal("stopped live grant remains usable")
	}
	if _, err := p.Play(api, "直播", id); err == nil {
		t.Fatal("old live API still resolves")
	}
	server.Close()
	loaded, err = p.StartCachedSource()
	if err != nil || len(loaded.Sites) != 1 {
		t.Fatalf("offline cached native startup: %+v %v", loaded, err)
	}
}

func TestNativeLiveExternalPlayerLaunchAndActualDecode(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg is required for external decoder smoke")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/channels.m3u" {
			fmt.Fprint(w, "#EXTM3U\n#EXTINF:-1 group-title=\"TV\",Fixture\n/stream.m3u8|User-Agent=external-fixture\n")
			return
		}
		if r.UserAgent() != "external-fixture" {
			http.Error(w, "header missing", 403)
			return
		}
		file := filepath.Join("../../testdata/playback", filepath.Base(r.URL.Path))
		if strings.HasSuffix(file, ".m3u8") {
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		} else {
			w.Header().Set("Content-Type", "video/mp2t")
		}
		http.ServeFile(w, r, file)
	}))
	defer server.Close()
	dir := t.TempDir()
	frames := filepath.Join(dir, "decoded.frames")
	p := newPlayer(dir)
	defer p.StopSource()
	if err := p.SaveSettings(Settings{Player: "custom", CustomPlayer: ffmpeg, CustomArgs: "-v error -rw_timeout 5000000 -i {url} -t 1 -f framemd5 " + strconv.Quote(frames), AllowLANMedia: true, DisableContinuous: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.AddSubscription("external fixture", server.URL+"/channels.m3u"); err != nil {
		t.Fatal(err)
	}
	result, err := p.LoadSource()
	if err != nil {
		t.Fatal(err)
	}
	catalog, _, _ := p.nativeCatalog(result.Sites[0].API)
	channel := catalog.Channels[0]
	detail, err := p.Detail(result.Sites[0].API, channel.ID)
	if err != nil {
		t.Fatal(err)
	}
	streams, err := p.PlayEpisodes(result.Sites[0].API, channel.ID, channel.Name, "直播", detail.Lines[0].Episodes, 0)
	if err != nil || len(streams) != 1 {
		t.Fatalf("external stream selection: %+v %v", streams, err)
	}
	if err := p.Open(streams[0], channel.Name); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(frames)
		if strings.Contains(string(data), "\n0,") {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("external player did not decode a video frame")
}

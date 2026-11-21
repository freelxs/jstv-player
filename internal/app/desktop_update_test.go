package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
)

func TestCompareSemver(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.6.3", "0.6.2", 1},
		{"v0.6.2", "0.6.2", 0},
		{"0.6.2", "0.7.0", -1},
		{"1.0.0", "1.0.0-rc.1", 1},
		{"1.0.0-beta.1", "1.0.0", -1},
		{"0.10.0", "0.9.9", 1},
	}
	for _, tc := range cases {
		if got := compareSemver(tc.a, tc.b); got != tc.want {
			t.Errorf("compareSemver(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestSelectReleaseAsset(t *testing.T) {
	assets := []githubReleaseAsset{
		{Name: "JSTV-Player-0.7.0-macos-universal.dmg", BrowserDownloadURL: "https://github.com/example/dl/mac.dmg"},
		{Name: "JSTV-Player-0.7.0-macos-universal-with-node.dmg", BrowserDownloadURL: "https://github.com/example/dl/mac-node.dmg"},
		{Name: "JSTV-Player-0.7.0-windows-amd64-setup.exe", BrowserDownloadURL: "https://github.com/example/dl/win.exe"},
		{Name: "JSTV-Player-0.7.0-windows-amd64-with-node-setup.exe", BrowserDownloadURL: "https://github.com/example/dl/win-node.exe"},
		{Name: "jstv-player_0.7.0_amd64.deb", BrowserDownloadURL: "https://github.com/example/dl/linux.deb"},
	}
	if name, _ := selectReleaseAsset(assets, "darwin", "arm64", false); name != "JSTV-Player-0.7.0-macos-universal.dmg" {
		t.Fatalf("darwin slim = %q", name)
	}
	if name, _ := selectReleaseAsset(assets, "darwin", "arm64", true); name != "JSTV-Player-0.7.0-macos-universal-with-node.dmg" {
		t.Fatalf("darwin with-node = %q", name)
	}
	if name, _ := selectReleaseAsset(assets, "windows", "amd64", false); name != "JSTV-Player-0.7.0-windows-amd64-setup.exe" {
		t.Fatalf("windows slim = %q", name)
	}
	if name, _ := selectReleaseAsset(assets, "linux", "amd64", false); name != "jstv-player_0.7.0_amd64.deb" {
		t.Fatalf("linux = %q", name)
	}
}

func TestCheckAndInstallUpdateGitHubFallback(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(githubReleaseResponse{
			TagName:     "v0.7.0",
			HTMLURL:     srv.URL + "/releases/tag/v0.7.0",
			Body:        "## 0.7.0\n- 新功能",
			PublishedAt: "2026-11-22T00:00:00Z",
			Assets: []githubReleaseAsset{
				{Name: "JSTV-Player-0.7.0-macos-universal.dmg", BrowserDownloadURL: srv.URL + "/download/mac.dmg"},
				{Name: "JSTV-Player-0.7.0-windows-amd64-setup.exe", BrowserDownloadURL: srv.URL + "/download/win.exe"},
				{Name: "jstv-player_0.7.0_amd64.deb", BrowserDownloadURL: srv.URL + "/download/linux.deb"},
			},
		})
	}))
	defer srv.Close()

	rt := New(t.TempDir(), nil, false)
	rt.SetAppVersion("0.6.2")
	var opened string
	rt.EnableDesktopIntegration(DesktopHooks{
		OpenExternal: func(u string) error { opened = u; return nil },
	})
	rt.Player.updateAPIURL = srv.URL
	rt.Player.updateHTTP = srv.Client()

	info, err := rt.Player.CheckUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if !info.Available || info.CanAutoInstall || info.LatestVersion != "0.7.0" || info.CurrentVersion != "0.6.2" || info.Source != "github" {
		t.Fatalf("unexpected update info: %+v", info)
	}
	if !strings.Contains(info.Notes, "新功能") {
		t.Fatalf("missing notes: %+v", info)
	}
	gotURL, err := rt.Player.InstallUpdate(context.Background())
	if err != nil {
		t.Fatalf("InstallUpdate: %v", err)
	}
	if gotURL == "" || opened != gotURL {
		t.Fatalf("expected OpenExternal(%q), got %q", gotURL, opened)
	}

	// Already up-to-date case clears pending URL.
	rt.SetAppVersion("0.7.0")
	info, err = rt.Player.CheckUpdate(context.Background())
	if err != nil || info.Available {
		t.Fatalf("expected up-to-date, got info=%+v err=%v", info, err)
	}
	if _, err := rt.Player.InstallUpdate(context.Background()); err == nil {
		t.Fatal("expected error installing when no update is pending")
	}
}

func TestCheckUpdateMyGoSignedHook(t *testing.T) {
	rt := New(t.TempDir(), nil, false)
	rt.SetAppVersion("0.6.2")
	rt.EnableDesktopIntegration(DesktopHooks{
		UpdaterEnabled: func() bool { return true },
		UpdaterCheck: func(ctx context.Context) (*mygo.Update, error) {
			return &mygo.Update{Version: "0.7.0", Notes: "Signed update", Date: time.Date(2026, 11, 22, 0, 0, 0, 0, time.UTC)}, nil
		},
	})
	info, err := rt.Player.CheckUpdate(context.Background())
	if err != nil {
		t.Fatalf("CheckUpdate: %v", err)
	}
	if !info.Available || !info.CanAutoInstall || info.Source != "mygo" || info.LatestVersion != "0.7.0" {
		t.Fatalf("unexpected signed update info: %+v", info)
	}
}

func TestKeepRunningAndNowPlayingHooks(t *testing.T) {
	rt := New(t.TempDir(), nil, false)
	var acquires, releases atomic.Int32
	var npMu sync.Mutex
	var lastNP mygo.NowPlayingInfo
	rt.EnableDesktopIntegration(DesktopHooks{
		KeepRunning: func(reason string) func() {
			acquires.Add(1)
			return func() { releases.Add(1) }
		},
		NowPlayingSet: func(info mygo.NowPlayingInfo) {
			npMu.Lock()
			lastNP = info
			npMu.Unlock()
		},
	})

	rt.Player.setKeepRunning(true)
	rt.Player.setKeepRunning(true) // idempotent while held
	if acquires.Load() != 1 || releases.Load() != 0 {
		t.Fatalf("acquires=%d releases=%d", acquires.Load(), releases.Load())
	}
	rt.Player.StopSource()
	if releases.Load() != 1 {
		t.Fatalf("expected release on StopSource, got %d", releases.Load())
	}

	payload := `{"title":"示例剧集 - 第1集","playing":true,"position":12.5,"duration":120,"hasPrev":false,"hasNext":true}`
	resp, err := http.Post(rt.Player.eps.base+"/player/state", "application/json", bytes.NewBufferString(payload))
	if err != nil {
		t.Fatalf("POST /player/state: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	npMu.Lock()
	got := lastNP
	npMu.Unlock()
	if got.Title != "示例剧集 - 第1集" || got.State != mygo.PlaybackPlaying || got.Duration != 120*time.Second || got.Commands&mygo.MediaNext == 0 || got.Commands&mygo.MediaPrevious != 0 {
		t.Fatalf("unexpected NowPlaying: %+v", got)
	}
}

func TestHomeRespectsContextCancellation(t *testing.T) {
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/init") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{}`))
			return
		}
		select {
		case started <- struct{}{}:
		default:
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer srv.Close()
	defer close(release)

	rt := New(t.TempDir(), nil, false)
	p := rt.Player
	p.mu.Lock()
	p.host = loopHost{base: srv.URL}
	p.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := p.Home(ctx, "/slow")
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream request never started")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Home did not return promptly after context cancel")
	}
}

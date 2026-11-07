package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freelxs/jstv-player/internal/catvod"
)

func TestExecutableConfigChangesRequireApproval(t *testing.T) {
	if _, err := catvod.FindNode(""); err != nil {
		t.Skip(err)
	}
	script, err := os.ReadFile("../catvod/testdata/mockspider.js")
	if err != nil {
		t.Fatal(err)
	}
	config := `require('fs').writeFileSync(process.env.NODE_PATH+'/config-marker','one'); module.exports={};`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.js":
			w.Write(script)
		case "/index.config.js":
			if config == "" {
				http.NotFound(w, r)
			} else {
				w.Write([]byte(config))
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	settings, err := p.AddSubscription("config test", server.URL+"/index.js")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(p.dir, "subs", settings.Active, "runtime", "config-marker")
	result, err := p.LoadSource()
	if err != nil || !result.TrustRequired || result.TrustConfigSHA256 == "" {
		t.Fatalf("initial approval: %+v %v", result, err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("unapproved config executed")
	}
	if err := p.ApproveSource(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadSource(); err != nil {
		t.Fatal(err)
	}
	oldBundle := result.TrustSHA256
	config = `require('fs').writeFileSync(process.env.NODE_PATH+'/config-marker','two'); module.exports={};`
	result, err = p.LoadSource()
	if err != nil || !result.TrustRequired || result.TrustSHA256 == oldBundle {
		t.Fatalf("config-only change: %+v %v", result, err)
	}
	if b, _ := os.ReadFile(marker); string(b) != "one" {
		t.Fatalf("new config ran without approval: %q", b)
	}
	if err := p.ApproveSource(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadSource(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(marker); string(b) != "two" {
		t.Fatalf("approved config did not run: %q", b)
	}
	config = ""
	result, err = p.LoadSource()
	if err != nil || !result.TrustRequired || result.TrustConfigSHA256 != "" {
		t.Fatalf("configuration removal: %+v %v", result, err)
	}
	if err := p.ApproveSource(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadSource(); err != nil {
		t.Fatal(err)
	}
	config = `module.exports={newConfiguration:true};`
	result, err = p.LoadSource()
	if err != nil || !result.TrustRequired || result.TrustConfigSHA256 == "" {
		t.Fatalf("configuration addition: %+v %v", result, err)
	}
}

func TestFailedStartRetainsWorkingVersionAndSupportsExplicitRollback(t *testing.T) {
	if _, err := catvod.FindNode(""); err != nil {
		t.Skip(err)
	}
	script, err := os.ReadFile("../catvod/testdata/mockspider.js")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.js" {
			w.Write(script)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	settings, err := p.AddSubscription("rollback", server.URL+"/index.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadSource(); err != nil {
		t.Fatal(err)
	}
	if err := p.ApproveSource(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadSource(); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(p.dir, "subs", settings.Active)
	old, err := catvod.CachedSource(dir)
	if err != nil {
		t.Fatal(err)
	}
	script = []byte(`exports.start=()=>{throw Error('failed-start-fixture')};exports.stop=()=>{};`)
	if result, err := p.LoadSource(); err != nil || !result.TrustRequired {
		t.Fatalf("changed source: %+v %v", result, err)
	}
	if err := p.ApproveSource(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadSource(); err == nil {
		t.Fatal("broken startup unexpectedly succeeded")
	}
	cached, err := catvod.CachedSource(dir)
	if err != nil || cached.BundleSHA256 != old.BundleSHA256 {
		t.Fatal("failed startup replaced working pointer")
	}
	// Rollback is explicit and reaffirms the exact cached manifest, never a
	// silent execution of an older code version after approving a newer one.
	if result, err := p.StartCachedSource(); err != nil || !result.TrustRequired {
		t.Fatalf("rollback confirmation: %+v %v", result, err)
	}
	if err := p.ApproveSource(); err != nil {
		t.Fatal(err)
	}
	if result, err := p.StartCachedSource(); err != nil || result.TrustRequired || p.host == nil {
		t.Fatalf("rollback: %+v %v", result, err)
	}
}

func TestStopCancelsAnUnpublishedStartingHost(t *testing.T) {
	if _, err := catvod.FindNode(""); err != nil {
		t.Skip(err)
	}
	script := `exports.start=async()=>{console.log('waiting-start-fixture');await new Promise(()=>{})};exports.stop=()=>{};`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.js" {
			w.Write([]byte(script))
		} else {
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	if _, err := p.AddSubscription("starting", server.URL+"/index.js"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadSource(); err != nil {
		t.Fatal(err)
	}
	if err := p.ApproveSource(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := p.LoadSource(); done <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(strings.Join(p.Logs(), "\n"), "waiting-start-fixture") {
		if time.Now().After(deadline) {
			t.Fatal("fixture did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	p.StopSource()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("startup not canceled: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("unpublished host was not canceled")
	}
	if p.host != nil {
		t.Fatal("stopped startup republished a host")
	}
}

func TestStopCancelsInFlightDownload(t *testing.T) {
	if _, err := catvod.FindNode(""); err != nil {
		t.Skip(err)
	}
	started, canceled := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/index.js" {
			http.NotFound(w, r)
			return
		}
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	defer server.Close()
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	if _, err := p.AddSubscription("blocked", server.URL+"/index.js"); err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() { _, err := p.LoadSource(); result <- err }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("download did not start")
	}
	p.StopSource()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("not canceled: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stop did not cancel download")
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("upstream request still running")
	}
	if p.host != nil {
		t.Fatal("stale host published")
	}
}

func TestStoppedPlaylistCannotResolveOrReuseFirstStream(t *testing.T) {
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	pl := &playlist{generation: p.generation(), eps: []catvod.Episode{{ID: "old"}}, first: &catvod.Stream{URL: "https://example.com/old.m3u8"}}
	p.StopSource()
	if _, err := p.episodeStream(pl, 0, true); !errors.Is(err, errSourceChanged) {
		t.Fatalf("old first stream reused: %v", err)
	}
	if _, err := p.addPlaylist(pl); !errors.Is(err, errSourceChanged) {
		t.Fatalf("stale playlist published: %v", err)
	}
}

func TestHistoryCorruptionIsPreservedBeforeRecording(t *testing.T) {
	dir := t.TempDir()
	broken := []byte(`{"truncated":`)
	if err := os.WriteFile(filepath.Join(dir, "history.json"), broken, 0o600); err != nil {
		t.Fatal(err)
	}
	p := newPlayer(dir)
	defer p.StopSource()
	p.recordHistory("sub", "api", "vod", HistoryEntry{Name: "episode"})
	paths, err := filepath.Glob(filepath.Join(dir, "history-corrupt-*.json"))
	if err != nil || len(paths) != 1 {
		t.Fatalf("missing corruption backup: %v %v", paths, err)
	}
	if b, _ := os.ReadFile(paths[0]); string(b) != string(broken) {
		t.Fatal("backup did not preserve bytes")
	}
	if h := p.loadHistoryLocked("sub"); h[historyKey("sub", "api", "vod")].Name != "episode" {
		t.Fatal("new history was not recorded")
	}
}

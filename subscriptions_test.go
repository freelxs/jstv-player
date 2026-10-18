package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/freelxs/jstv-player/internal/catvod"
)

func TestMigrateLegacySource(t *testing.T) {
	s := Settings{SourceURL: "http://example.com/a/index.js.md5", Player: "mpv"}
	s.migrate()
	if s.SourceURL != "" || len(s.Subscriptions) != 1 || s.Subscriptions[0].URL != "http://example.com/a/index.js.md5" {
		t.Fatalf("%+v", s)
	}
	if a, ok := s.active(); !ok || a.ID != s.Subscriptions[0].ID {
		t.Fatalf("active not set: %+v", s)
	}
	// Running it again changes nothing.
	s.migrate()
	if len(s.Subscriptions) != 1 {
		t.Fatalf("%+v", s)
	}
}

func TestMigrateFixesDanglingActive(t *testing.T) {
	s := Settings{Subscriptions: []Subscription{{ID: "a", URL: "http://example.com/1"}}, Active: "gone"}
	s.migrate()
	if s.Active != "a" {
		t.Fatal(s.Active)
	}
}

func TestSubscriptionCRUD(t *testing.T) {
	var s Settings
	if _, err := s.add("x", "  "); err == nil {
		t.Fatal("empty url accepted")
	}
	a, _ := s.add("", "http://example.com/a/index.js")
	b, _ := s.add("B", "http://127.0.0.1:1/b/index.js")
	if a.Name != "订阅 1" || s.Active != a.ID {
		t.Fatalf("%+v", s)
	}
	if err := s.update(b.ID, "Beta", "http://127.0.0.1:1/b2/index.js"); err != nil {
		t.Fatal(err)
	}
	if s.Subscriptions[1].Name != "Beta" || s.Subscriptions[1].URL != "http://127.0.0.1:1/b2/index.js" {
		t.Fatalf("%+v", s.Subscriptions)
	}
	if err := s.update("nope", "n", "http://example.com"); err != errNoSub {
		t.Fatal(err)
	}
	// Removing the active one hands over to the next.
	if err := s.remove(a.ID); err != nil || s.Active != b.ID || len(s.Subscriptions) != 1 {
		t.Fatalf("%v %+v", err, s)
	}
	if err := s.remove(b.ID); err != nil || s.Active != "" || len(s.Subscriptions) != 0 {
		t.Fatalf("%v %+v", err, s)
	}
}

func TestPlayerPersistsSubscriptions(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"sourceUrl":"http://example.com/old/index.js","player":"mpv"}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	p := newPlayer(dir)
	s := p.GetSettings()
	if len(s.Subscriptions) != 1 || s.Player != "mpv" {
		t.Fatalf("%+v", s)
	}
	s, err := p.AddSubscription("Two", "http://127.0.0.1:9/two/index.js")
	if err != nil {
		t.Fatal(err)
	}
	two := s.Subscriptions[1].ID
	// Saving general settings must keep the subscriptions.
	if err := p.SaveSettings(Settings{Player: "system"}); err != nil {
		t.Fatal(err)
	}
	// Switching to an unreachable address fails to load but stays selected.
	if _, err := p.SwitchSubscription(two); err == nil {
		t.Fatal("expected load error")
	}
	if _, err := p.SwitchSubscription("missing"); err != errNoSub {
		t.Fatal(err)
	}
	os.MkdirAll(filepath.Join(dir, "subs", two), 0o755)
	if _, err := p.RemoveSubscription(two); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "subs", two)); !os.IsNotExist(err) {
		t.Fatal("subscription files left behind")
	}

	var disk Settings
	b, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err := json.Unmarshal(b, &disk); err != nil {
		t.Fatal(err)
	}
	if disk.SourceURL != "" || disk.Player != "system" || len(disk.Subscriptions) != 1 || disk.Active != disk.Subscriptions[0].ID {
		t.Fatalf("%s", b)
	}
}

func TestSwitchReloadsPlugin(t *testing.T) {
	if _, err := catvod.FindNode(""); err != nil {
		t.Skip(err)
	}
	script, err := os.ReadFile("internal/catvod/testdata/mockspider.js")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/index.js") {
			w.Write(script)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	p := newPlayer(t.TempDir())
	defer p.StopSource()
	p.AddSubscription("A", srv.URL+"/a/index.js")
	s, _ := p.AddSubscription("B", srv.URL+"/b/index.js")
	a, b := s.Subscriptions[0].ID, s.Subscriptions[1].ID

	r1, err := p.SwitchSubscription(a)
	if err == nil && r1.TrustRequired {
		if err = p.ApproveSource(); err == nil {
			r1, err = p.LoadSource()
		}
	}
	if err != nil || r1.Sub.ID != a || len(r1.Sites) == 0 {
		t.Fatal(r1, err)
	}
	base1 := p.host.Base()
	r2, err := p.SwitchSubscription(b)
	if err == nil && r2.TrustRequired {
		if err = p.ApproveSource(); err == nil {
			r2, err = p.LoadSource()
		}
	}
	if err != nil || r2.Sub.ID != b {
		t.Fatal(r2, err)
	}
	if p.host.Base() == base1 {
		t.Fatal("plugin process was not restarted")
	}
	if resp, err := http.Get(base1 + "/config"); err == nil {
		resp.Body.Close()
		t.Fatal("old plugin process still answering")
	}
	if _, err := os.Stat(filepath.Join(p.dir, "subs", b, "source", "index.js")); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Home(r2.Sites[0].API); err != nil {
		t.Fatal(err)
	}
	// Removing the active subscription stops its process.
	if _, err := p.RemoveSubscription(b); err != nil {
		t.Fatal(err)
	}
	if p.host != nil {
		t.Fatal("process still running after removal")
	}
}

func TestSourceTrustApprovalRequiredForFirstUseAndChangedContent(t *testing.T) {
	if _, err := catvod.FindNode(""); err != nil {
		t.Skip(err)
	}
	script, err := os.ReadFile("internal/catvod/testdata/mockspider.js")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/index.js") || strings.HasSuffix(r.URL.Path, "/other.js") {
			_, _ = w.Write(script)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/index.js.md5") {
			http.NotFound(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	settings, err := p.AddSubscription("mock", srv.URL+"/index.js")
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.LoadSource()
	if err != nil || !r.TrustRequired || r.TrustSHA256 == "" || p.host != nil {
		t.Fatalf("first use should wait for approval: result=%+v err=%v", r, err)
	}
	if err := p.ApproveSource(); err != nil {
		t.Fatal(err)
	}
	r, err = p.LoadSource()
	if err != nil || r.TrustRequired || p.host == nil {
		t.Fatalf("approved source failed to load: result=%+v err=%v", r, err)
	}
	oldBase := p.host.Base()
	script = append(append([]byte(nil), script...), []byte("\n// changed fixture\n")...)
	r, err = p.LoadSource()
	if err != nil || !r.TrustRequired || p.host != nil {
		t.Fatalf("changed content should stop and wait: result=%+v err=%v", r, err)
	}
	if resp, err := http.Get(oldBase + "/config"); err == nil {
		resp.Body.Close()
		t.Fatal("old plugin kept running after changed content was detected")
	}
	if _, err := p.UpdateSubscription(settings.Subscriptions[0].ID, "mock", srv.URL+"/other.js"); err != nil {
		t.Fatal(err)
	}
	r, err = p.LoadSource()
	if err != nil || !r.TrustRequired || p.host != nil {
		t.Fatalf("URL change should require a fresh trust decision: result=%+v err=%v", r, err)
	}
}

func TestDisablingLANStopsListenerBeforeFailedReload(t *testing.T) {
	if _, err := catvod.FindNode(""); err != nil {
		t.Skip(err)
	}
	script, err := os.ReadFile("internal/catvod/testdata/mockspider.js")
	if err != nil {
		t.Fatal(err)
	}
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".md5") {
			http.NotFound(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/index.js") {
			if fail.Load() {
				http.Error(w, "fixture unavailable", http.StatusServiceUnavailable)
				return
			}
			_, _ = w.Write(script)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	if _, err := p.AddSubscription("mock", srv.URL+"/index.js"); err != nil {
		t.Fatal(err)
	}
	r, err := p.LoadSource()
	if err != nil || !r.TrustRequired {
		t.Fatalf("initial trust: %+v %v", r, err)
	}
	if err := p.ApproveSource(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadSource(); err != nil {
		t.Fatal(err)
	}
	s := p.GetSettings()
	s.AllowLAN = true
	if err := p.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadSource(); err != nil {
		t.Fatal(err)
	}
	oldBase := p.host.Base()
	fail.Store(true)
	s = p.GetSettings()
	s.AllowLAN = false
	// Saving the off toggle must wait for any in-flight LoadSource holding the
	// same lock, then stop whatever host that load publishes.
	p.loadMu.Lock()
	saved := make(chan error, 1)
	go func() { saved <- p.SaveSettings(s) }()
	select {
	case err := <-saved:
		p.loadMu.Unlock()
		t.Fatalf("LAN toggle returned before source-load serialization: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	p.loadMu.Unlock()
	select {
	case err := <-saved:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("LAN toggle did not complete after source-load lock released")
	}
	if p.host != nil {
		t.Fatal("LAN host reference remained after disabling")
	}
	if resp, err := http.Get(oldBase + "/config"); err == nil {
		resp.Body.Close()
		t.Fatal("old LAN listener remained reachable after toggle")
	}
	if _, err := p.LoadSource(); err == nil {
		t.Fatal("expected reload failure from local fixture")
	}
	if p.host != nil {
		t.Fatal("failed loopback reload left a host running")
	}
}

func TestApproveSourceKeepsPendingTrustWhenPinWriteFails(t *testing.T) {
	if _, err := catvod.FindNode(""); err != nil {
		t.Skip(err)
	}
	script, err := os.ReadFile("internal/catvod/testdata/mockspider.js")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/index.js") {
			_, _ = w.Write(script)
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	settings, err := p.AddSubscription("mock", srv.URL+"/index.js")
	if err != nil {
		t.Fatal(err)
	}
	r, err := p.LoadSource()
	if err != nil || !r.TrustRequired {
		t.Fatalf("expected a trust prompt: %+v %v", r, err)
	}
	sourceDir := filepath.Join(p.dir, "subs", settings.Subscriptions[0].ID, "source")
	if err := os.RemoveAll(sourceDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceDir, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.ApproveSource(); err == nil {
		t.Fatal("expected pin write to fail")
	}
	if err := os.Remove(sourceDir); err != nil {
		t.Fatal(err)
	}
	if err := p.ApproveSource(); err != nil {
		t.Fatalf("approval was dropped after the failed pin write: %v", err)
	}
}

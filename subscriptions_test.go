package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if err != nil || r1.Sub.ID != a || len(r1.Sites) == 0 {
		t.Fatal(r1, err)
	}
	base1 := p.host.Base()
	r2, err := p.SwitchSubscription(b)
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

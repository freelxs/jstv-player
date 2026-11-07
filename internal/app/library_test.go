package app

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/freelxs/jstv-player/internal/catvod"
)

func TestProgressResumesCoalescesWritesAndRevokesOldTokens(t *testing.T) {
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	s, err := p.AddSubscription("fixture", "https://example.com/index.js")
	if err != nil {
		t.Fatal(err)
	}
	entry := HistoryEntry{Flag: "A", Index: 1, Name: "E2", Position: 42, Duration: 100}
	p.recordHistory(s.Active, "/api", "vod", entry)
	token, position := p.newProgressSession(s.Active, "/api", "vod", HistoryEntry{Flag: "A", Index: 1, Name: "E2"}, p.generation())
	if token == "" || position != 42 {
		t.Fatalf("resume position %v", position)
	}
	if err := p.ReportProgress(token, 73, 100, false); err != nil {
		t.Fatal(err)
	}
	if err := p.ReportProgress(token, 80, 100, false); err != nil {
		t.Fatal(err)
	}
	if got := p.GetHistory("/api", "vod"); got == nil || got.Position != 73 {
		t.Fatalf("uncoalesced progress: %+v", got)
	}
	for _, pair := range [][2]float64{{math.NaN(), 100}, {0, math.Inf(1)}, {-1, 100}, {0, 0}} {
		if err := p.ReportProgress(token, pair[0], pair[1], false); err == nil {
			t.Fatal("invalid progress accepted")
		}
	}
	p.mu.Lock()
	session := p.progress[token]
	session.lastWrite = time.Now().Add(-time.Minute)
	p.progress[token] = session
	p.mu.Unlock()
	if err := p.ReportProgress(token, 100, 100, true); err != nil {
		t.Fatal(err)
	}
	if got := p.GetHistory("/api", "vod"); got.Position != 0 {
		t.Fatal("completed video resumes at end")
	}
	newToken, _ := p.newProgressSession(s.Active, "/api", "vod", HistoryEntry{Flag: "A", Index: 0}, p.generation())
	if err := p.ReportProgress(token, 1, 100, true); err == nil {
		t.Fatal("old episode token remained active")
	}
	p.StopSource()
	if err := p.ReportProgress(newToken, 1, 100, true); err == nil {
		t.Fatal("stopped progress session remained active")
	}
}

func TestFavoritesUseStableNativeIdentityAndPreserveBrokenFiles(t *testing.T) {
	dir := t.TempDir()
	p := newPlayer(dir)
	defer p.StopSource()
	settings, err := p.AddSubscription("live", "https://example.com/live.m3u")
	if err != nil {
		t.Fatal(err)
	}
	first := "live:" + settings.Active + ":1"
	second := "live:" + settings.Active + ":2"
	p.mu.Lock()
	p.sites = []catvod.Site{{API: first}}
	p.liveAPI = first
	p.mu.Unlock()
	if added, err := p.ToggleFavorite(first, "channel", "频道一", ""); err != nil || !added {
		t.Fatalf("add: %v %v", added, err)
	}
	p.StopSource()
	p.mu.Lock()
	p.liveAPI = second
	p.sites = []catvod.Site{{API: second}}
	p.mu.Unlock()
	items, err := p.GetFavorites()
	if err != nil || len(items) != 1 || items[0].API != second {
		t.Fatalf("favorite lost across generations: %+v %v", items, err)
	}
	if added, err := p.ToggleFavorite(second, "channel", "频道一", ""); err != nil || added {
		t.Fatalf("same favorite duplicated: %v %v", added, err)
	}
	broken := []byte("{broken")
	path := filepath.Join(dir, "favorites.json")
	if err := os.WriteFile(path, broken, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ToggleFavorite(second, "channel", "频道一", ""); err == nil {
		t.Fatal("broken favorites overwritten")
	}
	if b, _ := os.ReadFile(path); string(b) != string(broken) {
		t.Fatal("broken favorites bytes lost")
	}
}

func TestCatalogCacheCopiesFiltersAndInvalidatesOnConfiguration(t *testing.T) {
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	calls := 0
	fetch := func() (catvod.M, error) { calls++; return catvod.M{"list": []any{catvod.M{"name": "original"}}}, nil }
	params := map[string]string{"area": "cn"}
	first, err := p.cachedCatalog("api", "category", params, fetch)
	if err != nil {
		t.Fatal(err)
	}
	first["list"].([]any)[0].(catvod.M)["name"] = "changed"
	second, err := p.cachedCatalog("api", "category", params, fetch)
	if err != nil || calls != 1 || second["list"].([]any)[0].(map[string]any)["name"] != "original" {
		t.Fatalf("cache reused mutable map: %+v %v", second, err)
	}
	if _, err := p.cachedCatalog("api", "category", map[string]string{"area": "hk"}, fetch); err != nil || calls != 2 {
		t.Fatal("different filter collided")
	}
	(&Runtime{Player: p}).InvalidateConfiguration(p.generation())
	if _, err := p.cachedCatalog("api", "category", params, fetch); err != nil || calls != 3 {
		t.Fatal("configuration cache not invalidated")
	}
	if _, err := p.cachedCatalog("error", "home", nil, func() (catvod.M, error) { return nil, errors.New("fixture") }); err == nil {
		t.Fatal("error cache fixture succeeded")
	}
}

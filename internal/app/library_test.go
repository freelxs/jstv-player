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

func TestLibraryListRemoveClearWatchedAndPauseHistory(t *testing.T) {
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	subA, err := p.AddSubscription("sub-a", "https://example.com/a.js")
	if err != nil {
		t.Fatal(err)
	}
	subB, err := p.AddSubscription("sub-b", "https://example.com/b.js")
	if err != nil {
		t.Fatal(err)
	}
	idA := subA.Active
	idB := subB.Subscriptions[1].ID

	p.recordHistory(idA, "/vod", "v1", HistoryEntry{Flag: "L1", Index: 0, Name: "第1集", ShowName: "剧集一", Picture: "https://img.example/1.jpg", Position: 120, Duration: 600})
	p.recordHistory(idA, "/vod", "v2", HistoryEntry{Flag: "L1", Index: 2, Name: "第3集", ShowName: "剧集二", Position: 50, Duration: 300})
	p.recordHistory(idB, "/vod", "other", HistoryEntry{Flag: "L1", Index: 0, Name: "第1集", ShowName: "其他订阅"})

	items, err := p.ListHistory()
	if err != nil || len(items) != 2 {
		t.Fatalf("ListHistory = %+v, err=%v", items, err)
	}
	if err := p.SetWatched("/vod", "v1", true); err != nil {
		t.Fatal(err)
	}
	got := p.GetHistory("/vod", "v1")
	if got == nil || !got.Watched || got.Position != 0 {
		t.Fatalf("SetWatched(true) = %+v", got)
	}
	if err := p.RemoveHistory("/vod", "v2"); err != nil {
		t.Fatal(err)
	}
	items, err = p.ListHistory()
	if err != nil || len(items) != 1 || items[0].ID != "v1" {
		t.Fatalf("after RemoveHistory = %+v, err=%v", items, err)
	}

	settings := p.GetSettings()
	settings.DisableHistory = true
	if err := p.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	p.recordHistory(idA, "/vod", "v3", HistoryEntry{Flag: "L1", Index: 0, Name: "第1集", ShowName: "不应记录"})
	if p.GetHistory("/vod", "v3") != nil {
		t.Fatal("history recorded while DisableHistory is true")
	}

	if err := p.ClearHistory(); err != nil {
		t.Fatal(err)
	}
	items, err = p.ListHistory()
	if err != nil || len(items) != 0 {
		t.Fatalf("after ClearHistory = %+v, err=%v", items, err)
	}
	p.histMu.Lock()
	remaining := p.loadHistoryLocked(idB)
	p.histMu.Unlock()
	if _, ok := remaining[historyKey(idB, "/vod", "other")]; !ok {
		t.Fatal("ClearHistory removed another subscription's history")
	}
}

func TestLibraryExportAndImportMerge(t *testing.T) {
	src := newPlayer(t.TempDir())
	defer src.StopSource()
	s1, err := src.AddSubscription("src", "https://example.com/src.js")
	if err != nil {
		t.Fatal(err)
	}
	src.mu.Lock()
	src.sites = []catvod.Site{{API: "/vod"}}
	src.mu.Unlock()
	if _, err := src.ToggleFavorite("/vod", "fav1", "收藏一", "https://img.example/f1.jpg"); err != nil {
		t.Fatal(err)
	}
	src.recordHistory(s1.Active, "/vod", "h1", HistoryEntry{Flag: "L1", Index: 1, Name: "第2集", ShowName: "剧集一", Position: 90, Duration: 600})

	backup, err := src.ExportLibrary()
	if err != nil {
		t.Fatal(err)
	}
	if backup.Version != libraryBackupVersion || len(backup.Favorites) != 1 || len(backup.History) != 1 {
		t.Fatalf("unexpected backup: %+v", backup)
	}

	dst := newPlayer(t.TempDir())
	defer dst.StopSource()
	if _, err := dst.AddSubscription("dst", "https://example.com/dst.js"); err != nil {
		t.Fatal(err)
	}
	res, err := dst.ImportLibrary(backup)
	if err != nil {
		t.Fatal(err)
	}
	if res.FavoritesAdded != 1 || res.HistoryAdded != 1 {
		t.Fatalf("unexpected import result: %+v", res)
	}
	favs, err := dst.GetFavorites()
	if err != nil || len(favs) != 1 || favs[0].Name != "收藏一" {
		t.Fatalf("imported favorites = %+v, err=%v", favs, err)
	}
	hist, err := dst.ListHistory()
	if err != nil || len(hist) != 1 || hist[0].ShowName != "剧集一" || hist[0].Position != 90 {
		t.Fatalf("imported history = %+v, err=%v", hist, err)
	}
	backup.Version = 99
	if _, err := dst.ImportLibrary(backup); err == nil {
		t.Fatal("unsupported backup version accepted")
	}
}

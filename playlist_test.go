package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/freelxs/jstv-player/internal/catvod"
)

func TestParseEpPath(t *testing.T) {
	for in, want := range map[string]int{"/ep/abc/0": 0, "/ep/abc/12": 12, "/ep/abc/3/name.m3u8": 3} {
		id, n, ok := parseEpPath(in)
		if !ok || id != "abc" || n != want {
			t.Errorf("%s: %s %d %v", in, id, n, ok)
		}
	}
	for _, bad := range []string{"/ep/", "/ep/abc", "/ep/abc/x", "/ep//1", "/ep/abc/-1"} {
		if _, _, ok := parseEpPath(bad); ok {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestM3UFromStart(t *testing.T) {
	s := &episodeServer{base: "http://127.0.0.1:1"}
	pl := &playlist{show: "Show\nX", eps: []catvod.Episode{{Name: "E1", ID: "a"}, {Name: "E2", ID: "b"}, {Name: "E3", ID: "c"}}, start: 1}
	id := s.add(pl)
	got := s.m3u(id, pl)
	want := "#EXTM3U\n#EXTINF:-1,Show X - E2\nhttp://127.0.0.1:1/ep/" + id + "/1\n#EXTINF:-1,Show X - E3\nhttp://127.0.0.1:1/ep/" + id + "/2\n"
	if got != want {
		t.Fatalf("%q", got)
	}
}

func TestPlaylistEviction(t *testing.T) {
	s := &episodeServer{}
	first := s.add(&playlist{})
	for i := 0; i < maxPlaylists; i++ {
		s.add(&playlist{})
	}
	if s.get(first) != nil || len(s.lists) != maxPlaylists {
		t.Fatal("old playlists not evicted")
	}
}

func TestBuiltinEpisodeItemsUseResolvedFirstURL(t *testing.T) {
	eps := []catvod.Episode{{Name: "E1"}, {Name: "E2"}, {Name: "E3"}}
	items := builtinEpisodeItems("Show", eps, 1, func(i int) string { return "http://127.0.0.1:1/ep/x/" + strconv.Itoa(i) }, "http://127.0.0.1:2/p/resolved/a.m3u8")
	if len(items) != 2 || items[0].URL != "http://127.0.0.1:2/p/resolved/a.m3u8" || items[1].URL != "http://127.0.0.1:1/ep/x/2" {
		t.Fatalf("%+v", items)
	}
	if items[0].Title != "Show - E2" {
		t.Fatal(items[0].Title)
	}
	lazy := builtinEpisodeItems("Show", eps, 0, func(i int) string { return "http://127.0.0.1:1/ep/x/" + strconv.Itoa(i) }, "")
	if lazy[0].URL != "http://127.0.0.1:1/ep/x/0" {
		t.Fatal(lazy[0].URL)
	}
}

func TestPlaybackPageBase(t *testing.T) {
	if playbackPageBase("") != "http://127.0.0.1/" || playbackPageBase("http://127.0.0.1:9") != "http://127.0.0.1:9/" {
		t.Fatal(playbackPageBase(""), playbackPageBase("http://127.0.0.1:9"))
	}
}

func TestBuiltinFailureHintMatchesPlatform(t *testing.T) {
	page := playerPage("http://127.0.0.1/a.mp4", "t") + builtinPlaylistPage(nil)
	switch runtime.GOOS {
	case "linux":
		if strings.Contains(page, "IINA") || strings.Contains(page, "PotPlayer") || !strings.Contains(page, "mpv") {
			t.Fatal("linux failure hint names a player this system cannot use")
		}
	case "darwin":
		if !strings.Contains(page, "IINA") || strings.Contains(page, "PotPlayer") {
			t.Fatal("darwin failure hint")
		}
	case "windows":
		if !strings.Contains(page, "PotPlayer") || strings.Contains(page, "IINA") {
			t.Fatal("windows failure hint")
		}
	}
}

func TestBuiltinPlaylistPage(t *testing.T) {
	page := builtinPlaylistPage([]builtinItem{{Title: "a</script>", URL: "http://127.0.0.1:1/ep/x/0"}, {Title: "b", URL: "http://127.0.0.1:1/ep/x/1"}})
	if strings.Contains(page, "a</script>") || !strings.Contains(page, "addEventListener('ended'") {
		t.Fatal(page)
	}
}

func TestMPVArgsWithoutTitle(t *testing.T) {
	for _, a := range mpvArgs("http://127.0.0.1:1/playlist/x.m3u", "") {
		if strings.HasPrefix(a, "--title") {
			t.Fatal("playlist should keep entry titles")
		}
	}
}

// Lazy resolution end to end against the mock plugin: the playlist lists
// the episodes, /ep/<id>/<n> asks the plugin for that episode and
// redirects to the proxied stream, and the episode is remembered.
func TestEpisodeServerResolvesLazily(t *testing.T) {
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
	res, err := p.LoadSource()
	if err != nil {
		t.Fatal(err)
	}
	if res.TrustRequired {
		if err := p.ApproveSource(); err != nil {
			t.Fatal(err)
		}
		res, err = p.LoadSource()
		if err != nil || res.TrustRequired {
			t.Fatalf("load after approval: result=%+v err=%v", res, err)
		}
	}
	api := res.Sites[0].API
	d, err := p.Detail(api, "v1")
	if err != nil {
		t.Fatal(err)
	}
	line := d.Lines[0]
	id := p.eps.add(&playlist{api: api, vodID: "v1", show: "Mock", flag: line.Flag, eps: line.Episodes, start: 0})

	resp, err := http.Get(p.eps.playlistURL(id))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("playlist response is not readable by the built-in player page")
	}
	b := make([]byte, 4096)
	n, _ := resp.Body.Read(b)
	resp.Body.Close()
	if !strings.Contains(string(b[:n]), p.eps.epURL(id, len(line.Episodes)-1)) {
		t.Fatalf("%s", b[:n])
	}

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err = noRedirect.Get(p.eps.epURL(id, 1))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if resp.StatusCode != http.StatusFound || !strings.Contains(loc, "/p/") || !strings.Contains(loc, "e2") {
		t.Fatalf("%d %s", resp.StatusCode, loc)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("episode redirect is not readable by the built-in player page")
	}
	if h := p.GetHistory(api, "v1"); h == nil || h.Index != 1 || h.Flag != line.Flag {
		t.Fatalf("%+v", h)
	}
	if resp, _ := noRedirect.Get(p.eps.epURL(id, 99)); resp.StatusCode != http.StatusNotFound {
		t.Fatal(resp.StatusCode)
	}

	// Serving an entry prefetches the next one; asking for it then uses
	// the prefetched answer (and consumes it).
	pl := p.eps.get(id)
	pl.eps = append(pl.eps, catvod.Episode{Name: "x3", ID: "e3"})
	if _, err := p.resolveEpisode(pl, 1); err != nil {
		t.Fatal(err)
	}
	var next *resolved
	for i := 0; i < 50 && next == nil; i++ {
		time.Sleep(50 * time.Millisecond)
		pl.mu.Lock()
		if r := pl.pending[2]; r != nil {
			select {
			case <-r.done:
				next = r
			default:
			}
		}
		pl.mu.Unlock()
	}
	if next == nil || next.err != nil || next.st == nil {
		t.Fatalf("episode 3 not prefetched: %+v", next)
	}
	u, err := p.resolveEpisode(pl, 2)
	if err != nil || !strings.Contains(u, "e3") {
		t.Fatal(u, err)
	}
	pl.mu.Lock()
	if r := pl.pending[2]; r == next {
		t.Fatal("prefetched answer not consumed")
	}
	pl.mu.Unlock()
	resp, err = http.Get(p.eps.epURL(id, 0) + "?prefetch=1")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("prefetch endpoint %d cors %q", resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"))
	}
}

func TestPlayersPrefetchAndStayQuiet(t *testing.T) {
	m := strings.Join(mpvArgs("http://127.0.0.1:1/playlist/x.m3u", ""), " ")
	for _, want := range []string{"--prefetch-playlist=yes", "--osd-playing-msg=", "--term-playing-msg="} {
		if !strings.Contains(m, want) {
			t.Errorf("mpv args missing %s: %s", want, m)
		}
	}
	if a := iinaArgs("http://127.0.0.1:1/playlist/x.m3u"); !strings.Contains(strings.Join(a, " "), "--mpv-prefetch-playlist=yes") || a[len(a)-1] != "http://127.0.0.1:1/playlist/x.m3u" {
		t.Error(a)
	}
}

func TestStaleResolvedIsRefetched(t *testing.T) {
	c := make(chan struct{})
	close(c)
	st := &catvod.Stream{URL: "http://example.com/old.m3u8"}
	pl := &playlist{eps: []catvod.Episode{{ID: "a"}}, pending: map[int]*resolved{0: {done: c, st: st, at: time.Now().Add(-2 * resolvedTTL)}}}
	p := &Player{inited: map[string]bool{}}
	// Stale: must not return the old URL; with no plugin loaded it errors.
	if got, err := p.episodeStream(pl, 0, true); err == nil || got == st {
		t.Fatal("stale answer reused")
	}
}

func TestFirstPlayableSkipsURLsThatCannotReceiveProxyGrants(t *testing.T) {
	streams := []catvod.Stream{
		{URL: "ftp://media.example/video"},
		{URL: "/relative/video"},
		{URL: " HTTPS://media.example/video"},
		{URL: "HTTPS://media.example/video"},
		{URL: "https://media.example/video"},
	}
	got := firstPlayable(streams)
	if got == nil || got.URL != "https://media.example/video" {
		t.Fatalf("selected %#v", got)
	}
	if got := firstPlayable(streams[:4]); got != nil {
		t.Fatalf("unsupported candidate selected: %#v", got)
	}
}

func TestHistoryScopedToActiveSubscription(t *testing.T) {
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	first, err := p.AddSubscription("A", "https://example.com/a/index.js")
	if err != nil {
		t.Fatal(err)
	}
	idA := first.Active
	second, err := p.AddSubscription("B", "https://example.com/b/index.js")
	if err != nil {
		t.Fatal(err)
	}
	var idB string
	for _, sub := range second.Subscriptions {
		if sub.ID != idA {
			idB = sub.ID
		}
	}
	p.recordHistory("", "/api", "show", HistoryEntry{Flag: "线路", Index: 2, Name: "第3集"})
	if h := p.GetHistory("/api", "show"); h == nil || h.Index != 2 {
		t.Fatalf("active subscription lost its history: %+v", h)
	}
	p.mu.Lock()
	p.settings.Active = idB
	p.mu.Unlock()
	if h := p.GetHistory("/api", "show"); h != nil {
		t.Fatalf("other subscription saw the history: %+v", h)
	}
	p.recordHistory(idA, "/api", "show", HistoryEntry{Flag: "线路", Index: 4, Name: "第5集"})
	if h := p.GetHistory("/api", "show"); h != nil {
		t.Fatalf("playlist subscription history leaked into the active one: %+v", h)
	}
	p.mu.Lock()
	p.settings.Active = idA
	p.mu.Unlock()
	if h := p.GetHistory("/api", "show"); h == nil || h.Index != 4 {
		t.Fatalf("history was not kept on the originating subscription: %+v", h)
	}
}

func TestLegacyHistoryMigratesToActiveSubscription(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "history.json"), []byte(`{"/api\u0000show":{"flag":"L","index":4,"name":"E5","time":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	p := newPlayer(dir)
	defer p.StopSource()
	first, err := p.AddSubscription("A", "https://example.com/a/index.js")
	if err != nil {
		t.Fatal(err)
	}
	idA := first.Active
	second, err := p.AddSubscription("B", "https://example.com/b/index.js")
	if err != nil {
		t.Fatal(err)
	}
	var idB string
	for _, sub := range second.Subscriptions {
		if sub.ID != idA {
			idB = sub.ID
		}
	}
	h := p.GetHistory("/api", "show")
	if h == nil || h.Index != 4 || h.Name != "E5" {
		t.Fatalf("legacy history was not adopted: %+v", h)
	}
	b, err := os.ReadFile(filepath.Join(dir, "history.json"))
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]HistoryEntry
	if err := json.Unmarshal(b, &stored); err != nil {
		t.Fatal(err)
	}
	if _, ok := stored["/api\x00show"]; ok {
		t.Fatal("legacy key remained readable by every subscription")
	}
	if _, ok := stored[idA+"\x00"+"/api\x00show"]; !ok {
		t.Fatalf("legacy entry was not moved onto the active subscription: %#v", stored)
	}
	p.mu.Lock()
	p.settings.Active = idB
	p.mu.Unlock()
	if h := p.GetHistory("/api", "show"); h != nil {
		t.Fatalf("migrated history still visible on another subscription: %+v", h)
	}
}

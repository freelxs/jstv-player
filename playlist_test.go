package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

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
	if h := p.GetHistory(api, "v1"); h == nil || h.Index != 1 || h.Flag != line.Flag {
		t.Fatalf("%+v", h)
	}
	if resp, _ := noRedirect.Get(p.eps.epURL(id, 99)); resp.StatusCode != http.StatusNotFound {
		t.Fatal(resp.StatusCode)
	}
}

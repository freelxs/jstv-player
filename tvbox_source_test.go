package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/freelxs/jstv-player/internal/catvod"
)

func TestTVBoxSubscriptionLoadApproveAndPlay(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "tv.json") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"sites":[
				{"key":"cms","name":"采集","type":1,"api":"` + r.Host + `","searchable":1},
				{"key":"jar","name":"安卓","type":3,"api":"csp_Demo","searchable":1}
			]}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("ac") {
		case "list":
			w.Write([]byte(`{"class":[{"type_id":"1","type_name":"电影"}]}`))
		default:
			w.Write([]byte(`{"page":1,"pagecount":1,"list":[{"vod_id":"3","vod_name":"示例","vod_play_from":"线路","vod_play_url":"第1集$https://cdn.example.com/a.m3u8#第2集$https://cdn.example.com/b.m3u8"}]}`))
		}
	}))
	defer upstream.Close()
	// The config URL and the CMS API must both be absolute. The handler above
	// only sees the path, so point the CMS api at the same server explicitly.
	config := upstream.URL + "/tv.json"
	body := `{"sites":[
		{"key":"cms","name":"采集","type":1,"api":"` + upstream.URL + `/api.php/provide/vod/","searchable":1,"header":{"Referer":"https://player.example.com"}},
		{"key":"jar","name":"安卓","type":3,"api":"csp_Demo","searchable":1}
	],"lives":[{"name":"台","url":"` + upstream.URL + `/live.txt"}]}`
	upstream.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/tv.json") {
			w.Write([]byte(body))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/live.txt") {
			w.Write([]byte("新闻,#genre#\n一台,https://cdn.example.com/live.m3u8\n"))
			return
		}
		switch r.URL.Query().Get("ac") {
		case "list":
			w.Write([]byte(`{"class":[{"type_id":"1","type_name":"电影"}]}`))
		default:
			w.Write([]byte(`{"page":1,"pagecount":1,"list":[{"vod_id":"3","vod_name":"示例","vod_play_from":"线路","vod_play_url":"第1集$https://cdn.example.com/a.m3u8#第2集$https://cdn.example.com/b.m3u8"}]}`))
		}
	})

	dir := t.TempDir()
	p := newPlayer(dir)
	defer p.StopSource()
	if _, err := p.AddSubscription("示例订阅", config); err != nil {
		t.Fatal(err)
	}
	first, err := p.LoadSource()
	if err != nil || !first.TrustRequired || first.TrustSHA256 == "" {
		t.Fatalf("%v %+v", err, first)
	}
	if err := p.ApproveSource(); err != nil {
		t.Fatal(err)
	}
	loaded, err := p.LoadSource()
	if err != nil || loaded.TrustRequired {
		t.Fatalf("%v %+v", err, loaded)
	}
	if len(loaded.Sites) != 3 {
		t.Fatalf("%d sites", len(loaded.Sites))
	}
	var cms, jar catvod.Site
	var live bool
	for _, s := range loaded.Sites {
		switch s.Key {
		case "cms":
			cms = s
		case "jar":
			jar = s
		case "live":
			live = true
		}
	}
	if cms.API == "" || !jar.Unsupported || jar.CanSearch() || !live {
		t.Fatalf("cms %+v jar %+v live %v", cms, jar, live)
	}
	home, err := p.Home(cms.API)
	if err != nil {
		t.Fatal(err)
	}
	if home["class"] == nil {
		t.Fatal(home)
	}
	detail, err := p.Detail(cms.API, "3")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Lines) != 1 || len(detail.Lines[0].Episodes) != 2 {
		t.Fatalf("%+v", detail.Lines)
	}
	streams, err := p.Play(cms.API, detail.Lines[0].Flag, detail.Lines[0].Episodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if streams[0].URL != "https://cdn.example.com/a.m3u8" || streams[0].Headers["Referer"] != "https://player.example.com" {
		t.Fatalf("%+v", streams)
	}
	found, err := p.Search(cms.API, "示例", 1)
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := found["list"].([]any); len(list) != 1 {
		t.Fatal(found)
	}
	if _, err := os.Stat(filepath.Join(dir, "subs", loaded.Sub.ID, "source", "pin.json")); err != nil {
		t.Fatal(err)
	}
}

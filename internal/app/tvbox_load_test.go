package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/freelxs/jstv-player/internal/catvod"
)

func TestTVBoxVODLoadsCMSAndGreysOutJar(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tv.json" {
			fmt.Fprintf(w, `{"sites":[{"key":"cms","name":"采集","type":"1","api":"%s/api.php","searchable":1},{"key":"jar","name":"安卓站点","type":3,"api":"csp_Nope","searchable":1}]}`, r.Host)
			return
		}
		switch r.URL.Query().Get("ac") {
		case "list":
			fmt.Fprint(w, `{"class":[{"type_id":1,"type_name":"电影"}],"list":[{"vod_id":7,"vod_name":"示例片","vod_pic":"https://example.com/a.jpg"}]}`)
		default:
			fmt.Fprint(w, `{"page":1,"pagecount":1,"list":[{"vod_id":7,"vod_name":"示例片","vod_play_from":"线路","vod_play_url":"第1集$https://cdn.example.com/a.m3u8#第2集$https://cdn.example.com/b.m3u8"}]}`)
		}
	}))
	defer upstream.Close()
	// The fixture writes the API host from the request host, which is host:port
	// without a scheme. Rewrite the handler to emit the full upstream URL.
	upstream.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/tv.json" {
			fmt.Fprintf(w, `{"sites":[{"key":"cms","name":"采集","type":1,"api":"%s/api.php","searchable":1},{"key":"jar","name":"安卓站点","type":3,"api":"csp_Nope","searchable":1}],"rules":[{"name":"去广告","hosts":["cdn.example.com"],"regex":["ad\\.ts\\n"]}]}`, upstream.URL)
			return
		}
		switch r.URL.Query().Get("ac") {
		case "list":
			fmt.Fprint(w, `{"class":[{"type_id":1,"type_name":"电影"}],"list":[{"vod_id":7,"vod_name":"示例片"}]}`)
		default:
			fmt.Fprint(w, `{"page":1,"pagecount":1,"list":[{"vod_id":7,"vod_name":"示例片","vod_play_from":"线路","vod_play_url":"第1集$https://cdn.example.com/a.m3u8#第2集$https://cdn.example.com/b.m3u8"}]}`)
		}
	})
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	if _, err := p.AddSubscriptionWithType("点播", upstream.URL+"/tv.json", "auto"); err != nil {
		t.Fatal(err)
	}
	first, err := p.LoadSource()
	if err != nil || !first.TrustRequired || first.TrustSHA256 == "" {
		t.Fatalf("trust: %+v %v", first, err)
	}
	if err := p.ApproveSource(); err != nil {
		t.Fatal(err)
	}
	loaded, err := p.LoadSource()
	if err != nil || loaded.TrustRequired || len(loaded.Sites) != 2 {
		t.Fatalf("%+v %v", loaded, err)
	}
	var jar catvod.Site
	for _, site := range loaded.Sites {
		if site.Key == "jar" {
			jar = site
		}
	}
	if !jar.Unsupported || jar.CanSearch() || !strings.Contains(jar.Reason, "JAR") {
		t.Fatalf("%+v", jar)
	}
	home, err := p.Home(loaded.Sites[0].API)
	if err != nil {
		t.Fatal(err)
	}
	classes, _ := home["class"].([]any)
	if len(classes) != 1 {
		t.Fatalf("%v", home)
	}
	detail, err := p.Detail(loaded.Sites[0].API, "7")
	if err != nil || len(detail.Lines) != 1 || len(detail.Lines[0].Episodes) != 2 {
		t.Fatalf("%v %+v", err, detail)
	}
	cached, err := p.StartCachedSource()
	if err != nil || cached.TrustRequired || len(cached.Sites) != 2 {
		t.Fatalf("cache %+v %v", cached, err)
	}
}

func TestTVBoxDepotPicksOneLine(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/house.json":
			fmt.Fprintf(w, `{"urls":[{"name":"线路甲","url":"%s/a.json"},{"name":"线路乙","url":"%s/b.json"}]}`, server.URL, server.URL)
		case "/a.json":
			fmt.Fprintf(w, `{"sites":[{"key":"a","name":"甲","type":1,"api":"%s/api.php"}]}`, server.URL)
		case "/b.json":
			fmt.Fprint(w, `{"urls":[{"name":"内层","url":"`+server.URL+`/a.json"}]}`)
		default:
			fmt.Fprint(w, `{"class":[{"type_id":"1","type_name":"电影"}]}`)
		}
	}))
	defer server.Close()
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	if _, err := p.AddSubscription("仓库", server.URL+"/house.json"); err != nil {
		t.Fatal(err)
	}
	pick, err := p.LoadSource()
	if err != nil || !pick.RepoPick || len(pick.RepoLines) != 2 || pick.RepoLines[0].Name != "线路甲" {
		t.Fatalf("%+v %v", pick, err)
	}
	chosen, err := p.SelectRepoLine("线路乙")
	if err != nil || !chosen.RepoPick || len(chosen.RepoLines) != 1 || chosen.RepoLines[0].Name != "内层" {
		t.Fatalf("nested %+v %v", chosen, err)
	}
	final, err := p.SelectRepoLine("内层")
	if err != nil || !final.TrustRequired {
		t.Fatalf("trust %+v %v", final, err)
	}
	if err := p.ApproveSource(); err != nil {
		t.Fatal(err)
	}
	loaded, err := p.LoadSource()
	if err != nil || len(loaded.Sites) != 1 || loaded.Sites[0].Name != "甲" {
		t.Fatalf("%+v %v", loaded, err)
	}
	if _, err := p.SelectRepoLine(""); err != nil {
		t.Fatal(err)
	}
	again, err := p.LoadSource()
	if err != nil || !again.RepoPick || len(again.RepoLines) != 2 {
		t.Fatalf("reset %+v %v", again, err)
	}
}

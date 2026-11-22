package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/freelxs/jstv-player/internal/kazumi"
)

func TestKazumiSubscriptionLifecycleAndPlayback(t *testing.T) {
	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"name":"DemoAnime","version":"2.0","useNativePlayer":true}]`))
		case "/DemoAnime.json":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(kazumi.Rule{
				API:           "9",
				Type:          "anime",
				Name:          "DemoAnime",
				Version:       "2.0",
				BaseURL:       srvURL,
				Referer:       srvURL + "/ref/",
				SearchURL:     srvURL + "/search?q=@keyword",
				SearchList:    "//div[@class='item']",
				SearchName:    "//a",
				SearchResult:  "//a",
				ChapterRoads:  "//ul[@class='eps']",
				ChapterResult: "//li/a",
			})
		case "/search":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<html><body><div class="item"><a href="/show/1">测试番剧</a></div></body></html>`))
		case "/show/1":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<html><body><div class="module-info-introduction-content">剧情简介：这是一部用于端到端验证的测试番剧介绍。</div><ul class="eps"><li><a href="/watch/1-1">第1集</a></li></ul></body></html>`))
		case "/watch/1-1":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			encoded := url.QueryEscape(srvURL + "/stream/ep1.m3u8")
			_, _ = w.Write([]byte(`<html><body><iframe src="` + srvURL + `/player?url=` + encoded + `"></iframe></body></html>`))
		case "/stream/ep1.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:5,\nseg.ts\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	p := newPlayer(t.TempDir())
	defer p.StopSource()
	if _, err := p.AddSubscriptionWithType("Kazumi 规则", srv.URL+"/index.json", "auto"); err != nil {
		t.Fatalf("AddSubscriptionWithType: %v", err)
	}
	loaded, err := p.LoadSource()
	if err != nil {
		t.Fatalf("LoadSource: %v", err)
	}
	if len(loaded.Sites) != 2 || loaded.Sites[0].Name != "番剧目录" || loaded.Sites[0].CanSearch() || loaded.Sites[1].Name != "DemoAnime" {
		t.Fatalf("unexpected sites: %+v", loaded.Sites)
	}
	site := loaded.Sites[1]

	home, err := p.Home(context.Background(), site.API)
	if err != nil {
		t.Fatalf("Home: %v", err)
	}
	if _, ok := home["kazumi"]; !ok || home["class"] == nil || home["filters"] == nil {
		t.Fatalf("expected kazumi metadata and catalog classes in Home response, got %+v", home)
	}

	searchRes, err := p.Search(context.Background(), site.API, "测试", 1)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	list, _ := searchRes["list"].([]any)
	if len(list) != 1 {
		t.Fatalf("unexpected search list: %+v", searchRes)
	}
	vod, _ := list[0].(map[string]any)
	vodID, _ := vod["vod_id"].(string)
	if vodID != "/show/1" {
		t.Fatalf("unexpected vod_id: %q", vodID)
	}

	detail, err := p.Detail(context.Background(), site.API, vodID)
	if err != nil {
		t.Fatalf("Detail: %v", err)
	}
	if len(detail.Lines) != 1 || len(detail.Lines[0].Episodes) != 1 {
		t.Fatalf("unexpected detail lines: %+v", detail.Lines)
	}
	if detail.Vod["vod_content"] != "这是一部用于端到端验证的测试番剧介绍。" {
		t.Fatalf("expected detail vod_content, got %+v", detail.Vod)
	}
	ep := detail.Lines[0].Episodes[0]
	if ep.Name != "第1集" || ep.ID != srv.URL+"/watch/1-1" {
		t.Fatalf("unexpected episode: %+v", ep)
	}

	streams, err := p.Play(context.Background(), site.API, detail.Lines[0].Flag, ep.ID)
	if err != nil {
		t.Fatalf("Play: %v", err)
	}
	if len(streams) != 1 || streams[0].URL != srv.URL+"/stream/ep1.m3u8" || streams[0].Headers["Referer"] != srv.URL+"/ref/" {
		t.Fatalf("unexpected resolved streams: %+v", streams)
	}

	// Verify StartCachedSource works even if index.json disappears.
	cachedLoaded, err := p.StartCachedSource()
	if err != nil || len(cachedLoaded.Sites) != 2 || !strings.EqualFold(cachedLoaded.Sites[1].Name, "DemoAnime") {
		t.Fatalf("StartCachedSource failed: %+v %v", cachedLoaded, err)
	}
}

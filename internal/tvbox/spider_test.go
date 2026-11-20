package tvbox

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/freelxs/jstv-player/internal/catvod"
)

func TestXBPQWalksCategoriesDetailAndPlay(t *testing.T) {
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/show/"):
			fmt.Fprint(w, `<div class="module-item-pic"><a href="/detail/7" title="示例节目"><img data-src="/pic.jpg"></a><div class="module-item-note">HD</div>`)
		case r.URL.Path == "/detail/7":
			fmt.Fprint(w, `<div class="module-tab-item"><span>线路甲</span></div><div class="module-play-list"><a href="/play/7-1">第1集</a><a href="https://cdn.example/a.m3u8">第2集</a></div>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	raw := `{
		"sites":[{"key":"x","name":"模板","type":3,"api":"csp_XBPQ","searchable":1,"ext":{
			"分类url":"` + upstream.URL + `/show/{cateId}/p/{catePg}",
			"分类":"电影$1#剧集$2",
			"数组":"module-item-pic&&</div>",
			"标题":"title=\"&&\"",
			"图片":"data-src=\"&&\"",
			"链接":"href=\"&&\"",
			"副标题":"module-item-note\">&&<",
			"线路数组":"module-tab-item&&</div>",
			"线路标题":"<span>&&</span>",
			"播放数组":"module-play-list&&</div>"
		}}],
		"lives":[]
	}`
	cfg, err := Parse([]byte(raw), "https://example.com/cfg.json")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sites[0].Kind != "xbpq" || cfg.Sites[0].Unsupported {
		t.Fatalf("%+v", cfg.Sites[0])
	}
	srv := NewServer(cfg, Options{})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	client := &catvod.Client{Base: srv.Base(), HTTP: &http.Client{Timeout: 5 * time.Second}}
	home, err := client.Home(srv.Sites()[0].API)
	if err != nil {
		t.Fatal(err)
	}
	class := home["class"].([]any)
	if len(class) != 2 || class[0].(map[string]any)["type_name"] != "电影" {
		t.Fatalf("%v", home)
	}
	list := home["list"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["vod_name"] != "示例节目" {
		t.Fatalf("%v", list)
	}
	id := list[0].(map[string]any)["vod_id"].(string)
	detail, err := client.Detail(srv.Sites()[0].API, id)
	if err != nil {
		t.Fatal(err)
	}
	vod := detail["list"].([]any)[0].(map[string]any)
	lines := catvod.ParseLines(vod["vod_play_from"].(string), vod["vod_play_url"].(string))
	if len(lines) != 1 || lines[0].Flag != "线路甲" || len(lines[0].Episodes) != 2 {
		t.Fatalf("%+v", lines)
	}
	play, err := client.Play(srv.Sites()[0].API, lines[0].Flag, lines[0].Episodes[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	streams := catvod.NormalizePlay(play)
	if streams[0].URL != "https://cdn.example/a.m3u8" || streams[0].Parse {
		t.Fatalf("%+v", streams)
	}
	pagePlay, err := client.Play(srv.Sites()[0].API, lines[0].Flag, lines[0].Episodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !catvod.NormalizePlay(pagePlay)[0].Parse {
		t.Fatal(pagePlay)
	}
}

func TestXYQJsoupAndAppYs(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/list":
			fmt.Fprint(w, `<div class="card"><a href="/v/3" title="合成片">名字</a><img data-src="/a.jpg"><span class="note">更新</span></div>`)
		case "/v/3":
			fmt.Fprint(w, `<div class="line">正片</div><div class="eps"><a href="https://cdn.example/ep.mp4">1</a></div>`)
		case "/api.php/app/nav":
			fmt.Fprint(w, `{"list":[{"type_id":"1","type_name":"电影"},{"vod_id":"9","vod_name":"推荐","vod_pic":"https://cdn.example/p.jpg"}]}`)
		case "/api.php/app/video":
			fmt.Fprint(w, `{"data":{"list":[{"vod_id":"9","vod_name":"推荐","vod_remarks":"HD"}]}}`)
		case "/api.php/app/detail":
			fmt.Fprint(w, `{"list":[{"vod_id":"9","vod_name":"推荐","vod_play_from":"主线","vod_play_url":"第1集$https://cdn.example/show.m3u8"}]}`)
		case "/api.php/app/search":
			fmt.Fprint(w, `{"list":[{"vod_id":"9","vod_name":"推荐"}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	raw := `{
		"sites":[
			{"key":"q","name":"规则","type":3,"api":"csp_XYQHiker","ext":{
				"分类链接":"` + upstream.URL + `/list?id={cateId}&p={catePg}",
				"分类名称":"电影&剧集",
				"分类名称替换词":"1&2",
				"分类列表数组规则":".card",
				"分类片单是否Jsoup写法":"1",
				"分类片单标题":"a&&title",
				"分类片单链接":"a&&href",
				"分类片单图片":"img&&data-src",
				"分类片单副标题":".note&&Text",
				"线路列表数组规则":".line",
				"播放列表数组规则":".eps"
			}},
			{"key":"y","name":"苹果","type":3,"api":"csp_AppYsV2","ext":"` + upstream.URL + `/api.php/app/"}
		]
	}`
	cfg, err := Parse([]byte(raw), "https://example.com/cfg.json")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sites[0].Kind != "xyq" || cfg.Sites[1].Kind != "appys" {
		t.Fatalf("%s %s", cfg.Sites[0].Kind, cfg.Sites[1].Kind)
	}
	srv := NewServer(cfg, Options{})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	client := &catvod.Client{Base: srv.Base(), HTTP: &http.Client{Timeout: 5 * time.Second}}
	home, err := client.Home(srv.Sites()[0].API)
	if err != nil {
		t.Fatal(err)
	}
	list := home["list"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["vod_name"] != "合成片" {
		t.Fatalf("%v", home)
	}
	detail, err := client.Detail(srv.Sites()[0].API, list[0].(map[string]any)["vod_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	vod := detail["list"].([]any)[0].(map[string]any)
	if !strings.Contains(vod["vod_play_url"].(string), "https://cdn.example/ep.mp4") {
		t.Fatal(vod)
	}
	appHome, err := client.Home(srv.Sites()[1].API)
	if err != nil {
		t.Fatal(err)
	}
	if len(appHome["class"].([]any)) != 1 || len(appHome["list"].([]any)) != 1 {
		t.Fatalf("%v", appHome)
	}
	appDetail, err := client.Detail(srv.Sites()[1].API, "9")
	if err != nil {
		t.Fatal(err)
	}
	av := appDetail["list"].([]any)[0].(map[string]any)
	lines := catvod.ParseLines(av["vod_play_from"].(string), av["vod_play_url"].(string))
	play, err := client.Play(srv.Sites()[1].API, lines[0].Flag, lines[0].Episodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if catvod.NormalizePlay(play)[0].URL != "https://cdn.example/show.m3u8" {
		t.Fatal(play)
	}
	found, err := client.Search(srv.Sites()[1].API, "推荐", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(found["list"].([]any)) != 1 {
		t.Fatal(found)
	}
	_ = context.Background()
}

func TestFetchPrefersTVClientAndRetriesHTML(t *testing.T) {
	var saw []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		saw = append(saw, r.Header.Get("User-Agent"))
		if strings.HasPrefix(r.Header.Get("User-Agent"), "okhttp") {
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, "<!DOCTYPE html><html><body>nav</body></html>")
			return
		}
		fmt.Fprint(w, `{"sites":[{"key":"a","name":"甲","type":1,"api":"https://cdn.example/api.php"}]}`)
	}))
	defer srv.Close()
	body, _, err := Fetch(srv.Client(), srv.URL+"/cfg", 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Parse(body, srv.URL+"/cfg")
	if err != nil || len(cfg.Sites) != 1 || cfg.Sites[0].Kind != "cms-json" {
		t.Fatalf("%v %+v", err, cfg)
	}
	if len(saw) != 2 || !strings.HasPrefix(saw[0], "okhttp") || !strings.Contains(saw[1], "JSTVPlayer") {
		t.Fatal(saw)
	}
}

func TestInlineLiveAndSingleHLS(t *testing.T) {
	var upstream *httptest.Server
	upstream = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/one":
			fmt.Fprint(w, "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nhi.m3u8\n")
		case "/list.txt":
			fmt.Fprint(w, "新闻,#genre#\n台二,https://cdn.example/b.m3u8\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	raw := `{
		"sites":[{"key":"cms","name":"采集","type":1,"api":"https://cdn.example/api.php"}],
		"lives":[
			{"name":"单路","url":"` + upstream.URL + `/one"},
			{"name":"内嵌","channels":[{"name":"台一","urls":["https://cdn.example/a.m3u8","https://cdn.example/a2.m3u8"]}]},
			{"name":"代理","channels":[{"name":"列表","urls":["proxy://do=live&type=txt&ext=` + upstream.URL + `/list.txt"]}]}
		]
	}`
	cfg, err := Parse([]byte(raw), "https://example.com/cfg.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Lives) != 3 {
		t.Fatalf("%d", len(cfg.Lives))
	}
	srv := NewServer(cfg, Options{HTTP: upstream.Client()})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	client := &catvod.Client{Base: srv.Base(), HTTP: &http.Client{Timeout: 5 * time.Second}}
	home, err := client.Home("/tvbox/live")
	if err != nil {
		t.Fatal(err)
	}
	for i, class := range home["class"].([]any) {
		cat, err := client.Category("/tvbox/live", class.(map[string]any)["type_id"].(string), 1, nil)
		if err != nil {
			t.Fatal(err)
		}
		list := cat["list"].([]any)
		switch i {
		case 0:
			if len(list) != 1 || list[0].(map[string]any)["vod_name"] != "单路" {
				t.Fatalf("hls %+v", list)
			}
			detail, err := client.Detail("/tvbox/live", list[0].(map[string]any)["vod_id"].(string))
			if err != nil {
				t.Fatal(err)
			}
			vod := detail["list"].([]any)[0].(map[string]any)
			lines := catvod.ParseLines(vod["vod_play_from"].(string), vod["vod_play_url"].(string))
			play, err := client.Play("/tvbox/live", lines[0].Flag, lines[0].Episodes[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			if catvod.NormalizePlay(play)[0].URL != upstream.URL+"/one" {
				t.Fatal(play)
			}
		case 1:
			names := []string{}
			for _, item := range list {
				names = append(names, item.(map[string]any)["vod_name"].(string))
			}
			if len(names) != 2 || names[0] != "台一" || names[1] != "台一 #2" {
				t.Fatal(names)
			}
		case 2:
			if len(list) != 1 || list[0].(map[string]any)["vod_name"] != "台二" {
				t.Fatalf("%+v", list)
			}
		}
	}
}

package tvbox

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBiliClientFlow(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/bili.json":
			fmt.Fprint(w, `{"class":[{"type_name":"登陆配置","type_id":"peizhi"},{"type_name":"纪录片","type_id":"纪录片超清"}]}`)
		case "/x/web-interface/search/type":
			if r.URL.Query().Get("keyword") != "纪录片超清" {
				http.Error(w, "bad kw", 400)
				return
			}
			fmt.Fprint(w, `{"code":0,"data":{"numPages":2,"result":[{"bvid":"BV1test123","title":"<em class=\"keyword\">纪录片</em>精选","pic":"//i0.hdslb.com/a.jpg","duration":"10:00","author":"UP"}]}}`)
		case "/x/web-interface/view":
			fmt.Fprint(w, `{"code":0,"data":{"bvid":"BV1test123","cid":101,"title":"纪录片精选","pic":"//i0.hdslb.com/a.jpg","desc":"简介","tname":"纪录片","owner":{"name":"UP"},"pages":[{"cid":101,"page":1,"part":"第一集"},{"cid":102,"page":2,"part":"第二集"}]}}`)
		case "/x/player/playurl":
			fmt.Fprint(w, `{"code":0,"data":{"durl":[{"url":"https://cdn.example/v.mp4"}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	raw := []byte(`{"sites":[{"key":"bili","name":"B站","type":3,"api":"csp_BiliGuard","ext":{"json":"` + srv.URL + `/bili.json"}}]}`)
	cfg, err := Parse(raw, srv.URL+"/cfg.json")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sites[0].Unsupported || cfg.Sites[0].Kind != "bili" {
		t.Fatalf("expected supported bili site, got %+v", cfg.Sites[0])
	}

	bc := &biliClient{src: cfg.Sites[0], http: srv.Client(), apiBase: srv.URL}
	ctx := context.Background()

	home, err := bc.home(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cls := home["class"].([]any)
	if len(cls) != 1 || asMap(cls[0])["type_id"] != "纪录片超清" {
		t.Fatalf("expected peizhi filtered out, got %+v", cls)
	}

	cat, err := bc.category(ctx, "纪录片超清", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	list := cat["list"].([]any)
	if len(list) != 1 || asMap(list[0])["vod_name"] != "纪录片精选" || asMap(list[0])["vod_pic"] != "https://i0.hdslb.com/a.jpg" {
		t.Fatalf("unexpected category result: %+v", list)
	}

	det, err := bc.detail(ctx, "BV1test123")
	if err != nil {
		t.Fatal(err)
	}
	vod := asMap(det["list"].([]any)[0])
	if !strings.Contains(scalar(vod["vod_play_url"]), "第一集$BV1test123_101#第二集$BV1test123_102") {
		t.Fatalf("unexpected episodes: %+v", vod)
	}

	play, err := bc.play(ctx, "BV1test123_101")
	if err != nil {
		t.Fatal(err)
	}
	if play["url"] != "https://cdn.example/v.mp4" {
		b, _ := json.Marshal(play)
		t.Fatalf("unexpected play result: %s", b)
	}
}

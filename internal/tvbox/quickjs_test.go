package tvbox

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/freelxs/jstv-player/internal/catvod"
)

func TestQuickJSRunsType3WithoutNode(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/spider.js":
			_, _ = w.Write([]byte(qjsSpider))
		case "/list":
			if r.Header.Get("X-Test") != "yes" {
				http.Error(w, "missing header", 500)
				return
			}
			_, _ = w.Write([]byte("https://cdn.example/ep.m3u8"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	raw := []byte(`{"sites":[{"key":"js","name":"脚本","type":3,"api":"` + srv.URL + `/spider.js","ext":{"name":"规则甲"}}]}`)
	cfg, err := Parse(raw, srv.URL+"/tv.json")
	if err != nil {
		t.Fatal(err)
	}
	engine := NewServer(cfg, Options{Dir: t.TempDir(), HTTP: srv.Client()})
	if err := engine.Start(); err != nil {
		t.Fatal(err)
	}
	defer engine.Stop()
	if engine.Sites()[0].Unsupported {
		t.Fatal(engine.Sites()[0].Reason)
	}
	client := &catvod.Client{Base: engine.Base(), HTTP: &http.Client{Timeout: 10 * time.Second}}
	home, err := client.Home(engine.Sites()[0].API)
	if err != nil {
		t.Fatal(err)
	}
	class := home["class"].([]any)[0].(map[string]any)
	if class["type_name"] != "电影" {
		t.Fatalf("%v", home)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	eng, err := engine.engine(ctx, engine.sites[0])
	if err != nil {
		t.Fatal(err)
	}
	play, err := eng.call(ctx, "play", map[string]any{"flag": "线", "id": srv.URL + "/list"})
	if err != nil {
		t.Fatal(err)
	}
	got := asMap(play)
	if got["url"] != "https://cdn.example/ep.m3u8" {
		t.Fatalf("%v", got)
	}
	if pdfh(`<div class="row"><a href="/a">标题</a></div>`, ".row&&a&&Text") != "标题" {
		t.Fatal(pdfh(`<div class="row"><a href="/a">标题</a></div>`, ".row&&a&&Text"))
	}
	if pd(`<a href="/play">x</a>`, "a") != "/play" {
		t.Fatal(pd(`<a href="/play">x</a>`, "a"))
	}
	if joinURL("https://example.com/a/", "b.js") != "https://example.com/a/b.js" {
		t.Fatal(joinURL("https://example.com/a/", "b.js"))
	}
	if strings.Contains(adaptSpider("import x from './x.js';\nexport default {init}\n"), "import ") {
		t.Fatal("import kept")
	}
}

const qjsSpider = `
function init(ext) { local.set('name', ext && ext.name ? ext.name : ''); }
function home() {
  const title = pdfh('<h1 class="t">电影</h1>', '.t&&Text');
  return JSON.stringify({class:[{type_id:'1', type_name:title}]});
}
function homeVod() { return JSON.stringify({list:[]}); }
function category() { return JSON.stringify({page:1, pagecount:1, list:[]}); }
function detail(id) { return JSON.stringify({list:[{vod_id:id, vod_name:local.get('name','甲'), vod_play_from:'线', vod_play_url:'第1集$'+id}]}); }
function search(wd) { return JSON.stringify({list:[{vod_id:'s', vod_name:wd}]}); }
function play(flag, id) {
  const res = req(id, {method:'GET', headers:{'X-Test':'yes'}});
  if (res.code !== 200) throw new Error('upstream ' + res.code + ' ' + res.content);
  return JSON.stringify({parse:0, url:res.content});
}
export default {init, home, homeVod, category, detail, search, play};
`

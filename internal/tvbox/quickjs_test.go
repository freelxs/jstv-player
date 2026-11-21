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

func TestQuickJSLoadsCoreImport(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/lib/drpy2.min.js":
			fmt.Fprint(w, "import {\n  cheerio\n} from \"./drpy-core-lite.min.js\";\nfunction init(){}\nfunction home(){return JSON.stringify({class:[{type_id:\"1\",type_name:cheerio.tag}]})}\nfunction homeVod(){return \"\"}\nfunction category(){return \"\"}\nfunction detail(){return \"\"}\nfunction search(){return \"\"}\nfunction play(){return \"\"}\nexport default {init,home,homeVod,category,detail,search,play};\n")
		case "/lib/drpy-core-lite.min.js":
			fmt.Fprint(w, "var y={tag:\"电影\"};export{y as cheerio};")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	eng, err := startQuickJS(ctx, Options{Dir: t.TempDir(), HTTP: srv.Client()}, nil, Source{API: srv.URL + "/lib/drpy2.min.js"})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.stop()
	home, err := eng.call(ctx, "home", map[string]any{"filter": true})
	if err != nil {
		t.Fatal(err)
	}
	if asMap(home)["class"] == nil {
		t.Fatal(home)
	}
}

func TestQuickJSDefaultExport(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/lib/drpy2.min.js":
			fmt.Fprint(w, "import 模板 from \"./模板.js\";\nfunction init(){}\nfunction home(){return JSON.stringify({class:[{type_id:\"1\",type_name:模板.getMubans().name}]})}\nfunction homeVod(){return \"\"}\nfunction category(){return \"\"}\nfunction detail(){return \"\"}\nfunction search(){return \"\"}\nfunction play(){return \"\"}\nexport default {init,home,homeVod,category,detail,search,play};\n")
		case "/lib/模板.js":
			fmt.Fprint(w, "function getMubans(){return {name:\"儿歌\"}}\nexport default {getMubans};\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	eng, err := startQuickJS(ctx, Options{Dir: t.TempDir(), HTTP: srv.Client()}, nil, Source{API: srv.URL + "/lib/drpy2.min.js"})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.stop()
	home, err := eng.call(ctx, "home", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	class := asMap(home)["class"].([]any)[0].(map[string]any)
	if class["type_name"] != "儿歌" {
		t.Fatalf("%v", home)
	}
}

func TestCutImportKeepsMultilineClause(t *testing.T) {
	src := "import {\n  cheerio,\n  模板\n} from \"./drpy-core-lite.min.js\";\nlet vercode = 1;\n"
	spec, names, rest, ok := cutImport(src)
	if !ok || spec != "./drpy-core-lite.min.js" || !strings.Contains(names, "cheerio") || !strings.Contains(names, "模板") {
		t.Fatalf("%v %q %q", ok, spec, names)
	}
	if !strings.Contains(rest, "let vercode") || strings.Contains(rest, "from") {
		t.Fatalf("%q", rest)
	}
}

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

func TestQuickJSBlocksCrossPortLoopbackAndPrivateRequests(t *testing.T) {
	secretHit := false
	secret := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secretHit = true
		_, _ = w.Write([]byte("secret"))
	}))
	defer secret.Close()

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(qjsSpider))
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	eng, err := startQuickJS(ctx, Options{Dir: t.TempDir()}, nil, Source{API: srv.URL + "/spider.js"})
	if err != nil {
		t.Fatal(err)
	}
	defer eng.stop()

	for _, blockedURL := range []string{
		secret.URL + "/admin",
		"http://localhost/admin",
		"http://169.254.169.254/latest/meta-data/",
		"http://192.168.1.1/admin",
	} {
		if _, err := eng.call(ctx, "play", map[string]any{"flag": "线", "id": blockedURL}); err == nil {
			t.Fatalf("expected QuickJS __req to block %s", blockedURL)
		}
	}
	if secretHit {
		t.Fatal("QuickJS __req reached cross-port loopback server")
	}
}

func TestServerDisablesSilentNodeFallbackByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("this is not valid quickjs spider syntax {{{"))
	}))
	defer srv.Close()
	cfg, err := Parse([]byte(`{"sites":[{"key":"bad","name":"坏脚本","type":3,"api":"`+srv.URL+`/bad.js"}]}`), srv.URL+"/tv.json")
	if err != nil {
		t.Fatal(err)
	}
	s := NewServer(cfg, Options{Dir: t.TempDir(), Node: "node"})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = s.startScript(ctx, cfg.Sites[0])
	if err == nil || !strings.Contains(err.Error(), "已禁用自动回退到 Node.js") {
		t.Fatalf("expected silent Node fallback to be rejected, got: %v", err)
	}
}

func TestParseHTMLDocumentCapsLargeInput(t *testing.T) {
	big := `<div class="ok">标题</div>` + strings.Repeat("a</p>", (maxHTMLParseBytes/4)+1000)
	if got := pdfh(big, ".ok&&Text"); got != "标题" {
		t.Fatalf("unexpected pdfh output: %q", got)
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

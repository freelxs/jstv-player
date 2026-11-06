package tvbox

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/freelxs/jstv-player/internal/catvod"
)

func TestSampleConfig(t *testing.T) {
	body, err := os.ReadFile("testdata/config.json")
	if err != nil {
		t.Fatal(err)
	}
	origin := "https://example.com/config/config.json"
	cfg, err := Parse(body, origin)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sites) != 8 {
		t.Fatalf("sites = %d", len(cfg.Sites))
	}
	kinds := map[string]int{}
	var drpy []Source
	for _, s := range cfg.Sites {
		kinds[s.Kind]++
		if s.Type != 3 {
			t.Fatalf("%s type %d", s.Key, s.Type)
		}
		if s.Kind == "drpy" {
			if s.Unsupported {
				t.Fatalf("drpy %s marked unsupported", s.Key)
			}
			drpy = append(drpy, s)
		}
		if s.Kind == "jar" && (!s.Unsupported || !strings.Contains(s.Reason, "JAR")) {
			t.Fatalf("jar site not explained: %+v", s)
		}
	}
	if kinds["drpy"] != 3 || kinds["jar"] != 5 {
		t.Fatalf("kinds %+v", kinds)
	}
	if cfg.SpiderURL != "https://example.com/config/jar/bundle.txt" {
		t.Fatal(cfg.SpiderURL)
	}
	byKey := map[string]Source{}
	for _, s := range drpy {
		byKey[s.Key] = s
	}
	script := byKey["script-a"]
	if script.API != "https://example.com/config/engine/engine.min.js" {
		t.Fatal(script.API)
	}
	var ext string
	if err := json.Unmarshal(script.Ext, &ext); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ext, "/engine/") || !strings.HasSuffix(ext, ".js") {
		t.Fatal(ext)
	}
	if len(cfg.Lives) != 3 {
		t.Fatalf("lives %d", len(cfg.Lives))
	}
	if cfg.Lives[0].Name != "示例列表" || cfg.Hosts["img.example.net"] != "cdn.example.org" || len(cfg.Rules) != 1 {
		t.Fatalf("lives/hosts/rules %+v %+v %d", cfg.Lives[0], cfg.Hosts, len(cfg.Rules))
	}
	if !strings.Contains(cfg.Lives[1].URL, "127.0.0.1:9978") {
		t.Fatal(cfg.Lives[1].URL)
	}
}

func TestLooseCommentsAndControlCharacters(t *testing.T) {
	text := "\uFEFF// introduction\n{\n  # sample configuration\n  \"sites\": [\n    {\"key\":\"中文\", \"type\":3, \"api\":\"csp_Test\", /* comment */\n     \"ext\":{\"url\":\"https://example.com/a//b#tag\", \"literal\":\"/*keep*/ #keep ,}\", \"quote\":\"\\\"//keep\",},\n    }, // last source\n  ],\n  \"lives\":[{\"name\":\"直播\",\"url\":\"./直播.m3u\",}],\n}\n"
	cfg, err := Parse([]byte(text), "https://example.com/config/tv.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sites) != 1 || cfg.Sites[0].Key != "中文" || cfg.Sites[0].Kind != "jar" {
		t.Fatalf("%+v", cfg.Sites)
	}
	var ext map[string]string
	if err := json.Unmarshal(cfg.Sites[0].Ext, &ext); err != nil {
		t.Fatal(err)
	}
	if ext["url"] != "https://example.com/a//b#tag" || ext["literal"] != "/*keep*/ #keep ,}" || ext["quote"] != "\"//keep" {
		t.Fatalf("%+v", ext)
	}
	if got, err := url.PathUnescape(cfg.Lives[0].URL); err != nil || got != "https://example.com/config/直播.m3u" {
		t.Fatal(cfg.Lives[0].URL)
	}
	raw := "{\"sites\":[{\"key\":\"a\",\"name\":\"甲\n乙\t\",\"type\":3,\"api\":\"csp_A\"}]}"
	cfg, err = Parse([]byte(raw), "https://example.com/config.json")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sites[0].Name != "甲\n乙\t" {
		t.Fatalf("%q", cfg.Sites[0].Name)
	}
}

func TestImageAndBase64Envelopes(t *testing.T) {
	payload := []byte("// config\n{\"sites\":[{\"key\":\"a\",\"type\":3,\"api\":\"csp_A\"}]}")
	wrapped := append([]byte{0xFF, 0xD8, 0xFF, 0xD9}, []byte("Abcd1234**\n"+base64.StdEncoding.EncodeToString(payload)+"\r\n")...)
	cfg, err := Parse(wrapped, "https://example.com/tv")
	if err != nil || cfg.Sites[0].Key != "a" {
		t.Fatalf("%v %+v", err, cfg)
	}
	encoded := base64.StdEncoding.EncodeToString([]byte(`{"sites":[{"key":"b","type":1,"api":"https://example.com/api.php"}]}`))
	cfg, err = Parse([]byte(encoded), "https://example.com/config.json")
	if err != nil || cfg.Sites[0].Key != "b" || cfg.Sites[0].Kind != "cms-json" {
		t.Fatalf("%v %+v", err, cfg)
	}
	kept := `{"sites":[{"key":"a","type":3,"api":"csp_A","ext":"abcdefgh**not-base64"}]}`
	cfg, err = Parse([]byte(kept), "https://example.com/config.json")
	if err != nil {
		t.Fatal(err)
	}
	var ext string
	if json.Unmarshal(cfg.Sites[0].Ext, &ext) != nil || ext != "abcdefgh**not-base64" {
		t.Fatal(ext)
	}
}

func TestEncryptedConfigRoundTrip(t *testing.T) {
	plain := []byte(`{"sites":[{"key":"a","name":"甲","type":3,"api":"csp_A"}]}`)
	key := []byte("samplekey0000000")
	iv := []byte("abcdefghijklm000")
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	padded := pkcs7Pad(plain)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, iv).CryptBlocks(ct, padded)
	blob := append([]byte("$#SampleKey#$"), ct...)
	blob = append(blob, []byte("AbcDefGhiJklM")...)
	cfg, err := Parse([]byte(hex.EncodeToString(blob)), "https://example.com/config.json")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Sites[0].Key != "a" || cfg.Sites[0].Name != "甲" {
		t.Fatalf("%+v", cfg.Sites[0])
	}
	bad := "242358" + hex.EncodeToString(blob)[6:]
	if _, err := Parse([]byte(bad), "https://example.com/config.json"); err == nil {
		t.Fatal("wrong key decrypted")
	}
}

func TestRejectsForeignDocuments(t *testing.T) {
	origin := "https://example.com/config.json"
	for _, text := range []string{
		`{"sites":[{"key":"a","type":3,"api":"csp_A",ext:{}}]}`,
		`{"sites":[{"key":"a","type":3,"api":"csp_A"}]}<html>injected</html>`,
		`{"sites":[{"key":"a","type":3,"api":"csp_A"}]}/* unclosed`,
		"<!doctype html><html>landing page</html>",
		"24233234abcdef",
		`{"urls":[{"name":"仓库","url":"https://example.com/a"}]}`,
		`{"storeHouse":[{"sourceUrl":"https://example.com/a"}]}`,
		"hello,world\n",
		"分组,#genre#\n",
	} {
		if _, err := Parse([]byte(text), origin); err == nil {
			t.Fatalf("accepted %s", text)
		}
	}
}

func TestBarePlaylists(t *testing.T) {
	origin := "https://example.com/output/result.txt"
	txt := "\uFEFF更新,#genre#\n2026-10-14,http://example.com/a.m3u8\n\n新闻,#genre#\n一台,http://example.com/one.m3u8\n"
	cfg, err := Parse([]byte(txt), origin)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Sites) != 0 || len(cfg.Lives) != 1 || cfg.Lives[0].URL != origin {
		t.Fatalf("%+v", cfg)
	}
	channels, err := ParsePlaylist(txt, origin, nil)
	if err != nil || len(channels) != 2 || channels[1].Group != "新闻" || channels[1].Name != "一台" {
		t.Fatalf("%v %+v", err, channels)
	}
	m3u := "#EXTM3U\n#EXTINF:-1 group-title=\"新闻\" tvg-logo=\"https://example.com/a.png\",一台\nhttps://example.com/1.m3u8|User-Agent=Test\n"
	channels, err = ParsePlaylist(m3u, origin, nil)
	if err != nil || channels[0].Name != "一台" || channels[0].Group != "新闻" || channels[0].Headers["User-Agent"] != "Test" {
		t.Fatalf("%v %+v", err, channels)
	}
}

func TestPlaylistRulesAndDial(t *testing.T) {
	body := ApplyPlaylistRules([]Rule{{Hosts: []string{"cdn.example.net"}, Regex: []string{`ad\.ts\n`}}}, "edge.cdn.example.net", []byte("#EXTM3U\nad.ts\nseg.ts\n"))
	if string(body) != "#EXTM3U\nseg.ts\n" {
		t.Fatalf("%q", body)
	}
	if got := RewriteDial("videos.example.com:8080", map[string]string{"videos.example.com": "127.0.0.1"}); got != "127.0.0.1:8080" {
		t.Fatal(got)
	}
}

func TestCMSXMLJSONAndLive(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/live.m3u":
			w.Write([]byte("#EXTM3U\n#EXTINF:-1 group-title=\"新闻\",一台\nhttps://example.com/one.m3u8\n"))
			return
		case "/xml":
			w.Header().Set("Content-Type", "text/xml")
			w.Write([]byte(`<rss><class><ty id="1">电影</ty></class><list page="1" pagecount="3" pagesize="20" recordcount="40"><video><id>9</id><name>测试片</name><pic>https://example.com/p.jpg</pic><note>HD</note><dl><dd flag="线路1"><![CDATA[第1集$https://example.com/a.m3u8#第2集$https://example.com/b.m3u8]]></dd></dl></video></list></rss>`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Query().Get("ac") {
		case "list":
			w.Write([]byte(`{"class":[{"type_id":1,"type_name":"电影"}],"page":1,"pagecount":1,"list":[]}`))
		case "videolist", "detail":
			w.Write([]byte(`{"page":1,"pagecount":2,"limit":20,"total":21,"list":[{"vod_id":7,"vod_name":"示例","vod_pic":"https://example.com/a.jpg","vod_remarks":"更新","vod_play_from":"线路","vod_play_url":"第1集$https://cdn.example.com/a.m3u8"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()
	raw := []byte(`{"sites":[
		{"key":"cms","name":"采集","type":1,"api":"` + upstream.URL + `/api.php/provide/vod/","searchable":1,"header":{"Referer":"https://example.com"}},
		{"key":"xml","name":"XML","type":0,"api":"` + upstream.URL + `/xml","searchable":1},
		{"key":"jar","name":"安卓站点","type":3,"api":"csp_X","searchable":1}
	],"lives":[{"name":"本地","type":0,"url":"` + upstream.URL + `/live.m3u","ua":"LiveUA"}]}`)
	cfg, err := Parse(raw, upstream.URL+"/tv.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, Options{})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	client := &catvod.Client{Base: srv.Base(), HTTP: &http.Client{Timeout: 5 * time.Second}}
	got, err := client.Config()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Video.Sites) != 4 {
		t.Fatalf("%d sites", len(got.Video.Sites))
	}
	var jar catvod.Site
	for _, s := range got.Video.Sites {
		if s.Key == "jar" {
			jar = s
		}
		if s.Key == "jar" && (!s.Unsupported || s.CanSearch()) {
			t.Fatalf("%+v", s)
		}
	}
	if !strings.Contains(jar.Reason, "JAR") {
		t.Fatal(jar.Reason)
	}
	home, err := client.Home(got.Video.Sites[0].API)
	if err != nil {
		t.Fatal(err)
	}
	classes, _ := home["class"].([]any)
	if len(classes) != 1 {
		t.Fatalf("%v", home)
	}
	cat, err := client.Category(got.Video.Sites[0].API, "1", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	list, _ := cat["list"].([]any)
	if len(list) != 1 {
		t.Fatal(cat)
	}
	detail, err := client.Detail(got.Video.Sites[0].API, "7")
	if err != nil {
		t.Fatal(err)
	}
	vods, _ := detail["list"].([]any)
	vod, _ := vods[0].(map[string]any)
	lines := catvod.ParseLines(vod["vod_play_from"].(string), vod["vod_play_url"].(string))
	if len(lines) != 1 || lines[0].Episodes[0].ID != "https://cdn.example.com/a.m3u8" {
		t.Fatalf("%+v", lines)
	}
	play, err := client.Play(got.Video.Sites[0].API, lines[0].Flag, lines[0].Episodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	streams := catvod.NormalizePlay(play)
	if len(streams) != 1 || streams[0].URL != "https://cdn.example.com/a.m3u8" || streams[0].Headers["Referer"] != "https://example.com" {
		t.Fatalf("%+v", streams)
	}
	xmlHome, err := client.Home(got.Video.Sites[1].API)
	if err != nil {
		t.Fatal(err)
	}
	xmlDetail, err := client.Detail(got.Video.Sites[1].API, "9")
	if err != nil {
		t.Fatal(err)
	}
	if xmlHome["class"] == nil {
		t.Fatal(xmlHome)
	}
	xv := xmlDetail["list"].([]any)[0].(map[string]any)
	if xv["vod_name"] != "测试片" || !strings.Contains(xv["vod_play_url"].(string), "https://example.com/b.m3u8") {
		t.Fatal(xv)
	}
	liveHome, err := client.Home("/tvbox/live")
	if err != nil {
		t.Fatal(err)
	}
	liveClass := liveHome["class"].([]any)[0].(map[string]any)
	liveCat, err := client.Category("/tvbox/live", liveClass["type_id"].(string), 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	ch := liveCat["list"].([]any)[0].(map[string]any)
	liveDetail, err := client.Detail("/tvbox/live", ch["vod_id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	lv := liveDetail["list"].([]any)[0].(map[string]any)
	lns := catvod.ParseLines(lv["vod_play_from"].(string), lv["vod_play_url"].(string))
	livePlay, err := client.Play("/tvbox/live", lns[0].Flag, lns[0].Episodes[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if catvod.NormalizePlay(livePlay)[0].URL != "https://example.com/one.m3u8" {
		t.Fatal(livePlay)
	}
	if catvod.NormalizePlay(livePlay)[0].Headers["User-Agent"] != "LiveUA" {
		t.Fatal(livePlay)
	}
}

func TestHostOverrideReachesCMS(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Host, "videos.example.com:") {
			http.Error(w, r.Host, 500)
			return
		}
		w.Write([]byte(`{"class":[{"type_id":"1","type_name":"电影"}]}`))
	}))
	defer upstream.Close()
	u, _ := url.Parse(upstream.URL)
	api := "http://videos.example.com:" + u.Port() + "/provide"
	cfg, err := Parse([]byte(`{"sites":[{"key":"a","name":"a","type":1,"api":"`+api+`"}],"hosts":["videos.example.com=127.0.0.1"]}`), "https://example.com/c.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := NewServer(cfg, Options{HTTP: HTTPClient(cfg.Hosts, 5*time.Second)})
	if err := srv.Start(); err != nil {
		t.Fatal(err)
	}
	defer srv.Stop()
	home, err := (&catvod.Client{Base: srv.Base(), HTTP: &http.Client{Timeout: 5 * time.Second}}).Home(srv.Sites()[0].API)
	if err != nil {
		t.Fatal(err)
	}
	if home["class"] == nil {
		t.Fatal(home)
	}
}

func TestDrpyBootSpeaksSpiderAPI(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "yes" {
			http.Error(w, "missing header", 500)
			return
		}
		w.Write([]byte("https://cdn.example.com/ep.m3u8"))
	}))
	defer upstream.Close()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{\"type\":\"module\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	script := strings.ReplaceAll(fakeSpider, "SERVER", upstream.URL)
	if err := os.WriteFile(filepath.Join(dir, "spider.mjs"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	eng, err := startDrpyModule(ctx, Options{Node: node, Dir: dir}, nil, filepath.Join(dir, "spider.mjs"), "", "", "")
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
	play, err := eng.call(ctx, "play", map[string]any{"flag": "线", "id": upstream.URL + "/media", "flags": []any{}})
	if err != nil {
		t.Fatal(err)
	}
	got := asMap(play)
	if got["url"] != "https://cdn.example.com/ep.m3u8" {
		t.Fatalf("%v", got)
	}
}

const fakeSpider = `
function init() {}
function home() { return JSON.stringify({class:[{type_id:"1",type_name:"电影"}]}); }
function homeVod() { return JSON.stringify({list:[]}); }
function category(tid, pg) { return JSON.stringify({page:Number(pg)||1,pagecount:1,list:[{vod_id:String(tid),vod_name:"乙"}]}); }
function detail(id) { return JSON.stringify({list:[{vod_id:id,vod_name:"甲",vod_play_from:"线",vod_play_url:"第1集$"+id}]}); }
function search(wd) { return JSON.stringify({list:[{vod_id:"s",vod_name:wd}]}); }
function play(flag, id) {
  const res = req(id, {method:"GET", headers:{"X-Test":"yes"}});
  if (res.code !== 200) throw new Error("upstream " + res.code + " " + res.content);
  return JSON.stringify({parse:0, url:res.content, header:{Referer:"http://example.com/"}});
}
export default {init, home, homeVod, category, detail, search, play};
`

func TestDirectMediaClearsParseFlag(t *testing.T) {
	media := coercePlay(map[string]any{"parse": 1, "url": "https://cdn.example.com/a.mp4"}, map[string]string{"Referer": "https://example.com"})
	page := coercePlay(map[string]any{"parse": 1, "url": "https://example.com/play/1"}, nil)
	if media["parse"] != 0 || page["parse"] != 1 {
		t.Fatalf("media %#v page %#v", media["parse"], page["parse"])
	}
	if media["header"].(map[string]any)["Referer"] != "https://example.com" {
		t.Fatal(media["header"])
	}
}

func pkcs7Pad(b []byte) []byte {
	n := aes.BlockSize - len(b)%aes.BlockSize
	out := append([]byte(nil), b...)
	for i := 0; i < n; i++ {
		out = append(out, byte(n))
	}
	return out
}

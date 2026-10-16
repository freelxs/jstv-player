package catvod

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScriptURLs(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.com/b/index.js.md5": "https://example.com/b/index.js",
		"https://example.com/b/index.js":     "https://example.com/b/index.js",
		"https://example.com/b":              "https://example.com/b/index.js",
	} {
		if got, _ := ScriptURLs(in); got != want {
			t.Errorf("%s: got %s", in, got)
		}
	}
}

func TestParseLines(t *testing.T) {
	l := ParseLines("A$$$B", "第1集$e1#第2集$e2$$$HD$e3")
	if len(l) != 2 || l[0].Flag != "A" || len(l[0].Episodes) != 2 || l[1].Episodes[0].ID != "e3" {
		t.Fatalf("%+v", l)
	}
	if l := ParseLines("", "http://example.com/a.m3u8"); l[0].Episodes[0].ID != "http://example.com/a.m3u8" || l[0].Flag != "线路1" {
		t.Fatalf("%+v", l)
	}
}

func TestNormalizePlay(t *testing.T) {
	s := NormalizePlay(M{"parse": float64(1), "url": "http://example.com/p"})
	if len(s) != 1 || !s[0].Parse {
		t.Fatal(s)
	}
	s = NormalizePlay(M{"parse": float64(0), "url": []any{"a", "u1", "b", "u2"}, "header": map[string]any{"Referer": "r"}})
	if len(s) != 2 || s[1].URL != "u2" || s[0].Headers["Referer"] != "r" {
		t.Fatal(s)
	}
}

func TestFetchMD5(t *testing.T) {
	js, _ := os.ReadFile("testdata/mockspider.js")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.js":
			w.Write(js)
		case "/index.js.md5":
			w.Write([]byte("0123456789abcdef0123456789abcdef\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	src, err := Fetch(nil, srv.URL+"/index.js.md5", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !src.MD5Mismatch() || src.Expected != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("%+v", src)
	}
}

func TestHostWithMockSpider(t *testing.T) {
	node, err := FindNode("")
	if err != nil {
		t.Skip(err)
	}
	if exec.Command(node, "-v").Run() != nil {
		t.Skip("node not runnable")
	}
	dir := t.TempDir()
	script, _ := filepath.Abs("testdata/mockspider.js")
	h := &Host{Node: node, Dir: dir, Script: script, LogFunc: func(s string) { t.Log(s) }}
	if err := h.Start(20 * time.Second); err != nil {
		t.Fatal(err)
	}
	defer h.Stop()
	c := &Client{Base: h.Base()}
	cfg, err := c.Config()
	if err != nil || len(cfg.Video.Sites) != 1 {
		t.Fatal(cfg, err)
	}
	api := cfg.Video.Sites[0].API
	if err := c.Init(api); err != nil {
		t.Fatal(err)
	}
	home, err := c.Home(api)
	if err != nil || len(home["class"].([]any)) != 1 {
		t.Fatal(home, err)
	}
	cat, err := c.Category(api, "1", 2, nil)
	if err != nil || cat["list"].([]any)[0].(M)["vod_id"] != "v1-2" {
		t.Fatal(cat, err)
	}
	d, err := c.Detail(api, "v1")
	if err != nil {
		t.Fatal(err)
	}
	vod := d["list"].([]any)[0].(M)
	lines := ParseLines(vod["vod_play_from"].(string), vod["vod_play_url"].(string))
	p, err := c.Play(api, lines[0].Flag, lines[0].Episodes[1].ID)
	if err != nil {
		t.Fatal(err)
	}
	st := NormalizePlay(p)
	if len(st) != 1 || !strings.HasPrefix(st[0].URL, h.Base()+"/v/A/e2") {
		t.Fatal(st)
	}
	s, err := c.Search(api, "hello", 1)
	if err != nil || s["list"].([]any)[0].(M)["vod_name"] != "hello" {
		t.Fatal(s, err)
	}
	h.Stop()
	if h.Base() != "" {
		t.Fatal("not stopped")
	}
}

func TestHeaderProxy(t *testing.T) {
	var seg string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Referer") != "https://example.com/" {
			http.Error(w, "no referer", 403)
			return
		}
		switch r.URL.Path {
		case "/a/index.m3u8":
			w.Write([]byte("#EXTM3U\n#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\nseg1.ts\n/abs/seg2.ts\n"))
		default:
			seg = r.URL.Path
			w.Write([]byte("DATA"))
		}
	}))
	defer up.Close()
	p := &HeaderProxy{}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	u := p.Wrap(up.URL+"/a/index.m3u8", map[string]string{"Referer": "https://example.com/"})
	resp, err := http.Get(u)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 4 || !strings.Contains(lines[1], url.QueryEscape(up.URL+"/a/key.bin")) || !strings.Contains(lines[3], url.QueryEscape(up.URL+"/abs/seg2.ts")) {
		t.Fatal(string(b))
	}
	r2, err := http.Get(lines[2])
	if err != nil {
		t.Fatal(err)
	}
	d, _ := io.ReadAll(r2.Body)
	if string(d) != "DATA" || seg != "/a/seg1.ts" {
		t.Fatal(string(d), seg)
	}
}

func TestWrapAlwaysWithCommaUA(t *testing.T) {
	ua := "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_14_6) AppleWebKit/537.36 (KHTML, like Gecko)"
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != ua {
			http.Error(w, "ua", 403)
			return
		}
		w.Write([]byte("OK"))
	}))
	defer up.Close()
	p := &HeaderProxy{}
	p.Start()
	if p.Wrap(up.URL, nil) != up.URL {
		t.Fatal("Wrap without headers should not proxy")
	}
	if u := p.WrapAlways(up.URL+"/plain", nil); u == up.URL+"/plain" {
		t.Fatal("WrapAlways did not proxy")
	}
	resp, err := http.Get(p.Wrap(up.URL, map[string]string{"User-Agent": ua}))
	if err != nil || resp.StatusCode != 200 {
		t.Fatal(err, resp.StatusCode)
	}
}

func TestCheckLocalStreams(t *testing.T) {
	base := "http://127.0.0.1:55226"
	ok := []Stream{{URL: "http://127.0.0.1:55226/spider/mock/3/video/abc"}, {URL: "http://localhost:55226/x"}, {URL: "https://cdn.example.com/a.m3u8"}}
	if err := CheckLocalStreams(ok, base); err != nil {
		t.Fatal(err)
	}
	bad := []Stream{{URL: "http://127.0.0.1:9979/play?vid=1234567890"}}
	if err := CheckLocalStreams(bad, base); err == nil || !strings.Contains(err.Error(), "9979") {
		t.Fatal(err)
	}
	if err := CheckLocalStreams(bad, base, "127.0.0.1:9979"); err != nil {
		t.Fatal(err)
	}
}

func TestBridge(t *testing.T) {
	var gotRange string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/media/1234567890/720p.mp4" {
			http.NotFound(w, r)
			return
		}
		gotRange = r.Header.Get("Range")
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Content-Range", "bytes 0-3/100")
		w.WriteHeader(206)
		w.Write([]byte("ftyp"))
	}))
	defer up.Close()
	b := &Bridge{Template: up.URL + "/media/{vid}/{res}.mp4"}
	if err := b.Start(0); err != nil {
		t.Fatal(err)
	}
	src := "http://127.0.0.1:9979/play?vid=1234567890"
	u := b.Rewrite(src)
	if !strings.Contains(u, b.Addr()) || !strings.HasSuffix(u, "/play?vid=1234567890") {
		t.Fatal(u)
	}
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Range", "bytes=0-3")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 206 || string(body) != "ftyp" || gotRange != "bytes=0-3" {
		t.Fatal(resp.StatusCode, string(body), gotRange)
	}
	if CheckLocalStreams([]Stream{{URL: u}}, "http://127.0.0.1:1", b.Addr()) != nil {
		t.Fatal("bridge URL rejected")
	}
	if r, _ := http.Get(strings.Replace(u, "1234567890", "1", 1)); r.StatusCode != 400 {
		t.Fatal(r.StatusCode)
	}
}

func TestBridgeDisabledByDefault(t *testing.T) {
	if DefaultBridgeTemplate != "" {
		t.Fatal("default template must be empty")
	}
	b := &Bridge{}
	if b.Enabled() {
		t.Fatal("enabled without template")
	}
	if _, err := b.Resolve("1234567890"); err == nil {
		t.Fatal("resolved without template")
	}
	if got := LocalizeURL("http://127.0.0.1:9979/play?vid=1", "http://127.0.0.1:60444", ""); got != "http://127.0.0.1:9979/play?vid=1" {
		t.Fatal(got)
	}
	if CheckLocalStreams([]Stream{{URL: "http://127.0.0.1:9979/play?vid=1"}}, "http://127.0.0.1:60444") == nil {
		t.Fatal("unconfigured bridge URL accepted")
	}
}

func TestPreflightHTML(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()
	if html, err := Preflight(nil, srv.URL+"/s/abc", nil); err != nil || !html {
		t.Fatal(html, err)
	}
}

func TestProxyRangeAndExtensions(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v/index.m3u8":
			// a server that honours Range on playlists (FFmpeg sends one)
			if r.Header.Get("Range") != "" {
				w.Header().Set("Content-Range", "bytes 0-40/41")
				w.WriteHeader(206)
			}
			w.Write([]byte("#EXTM3U\n#EXTINF:10,\nseg-00000.ts\n#EXTINF:10,\nimg/a1.jpg\n"))
		default:
			w.Write([]byte("TS"))
		}
	}))
	defer up.Close()
	p := &HeaderProxy{}
	p.Start()
	u := p.Wrap(up.URL+"/v/index.m3u8", map[string]string{"Referer": "x"})
	if !strings.Contains(u, "/p/index.m3u8?") {
		t.Fatal(u)
	}
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("Range", "bytes=0-")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(lines) != 5 || !strings.Contains(lines[2], "/p/seg-00000.ts?") || !strings.Contains(lines[4], "/p/a1.ts?") {
		t.Fatal(string(b))
	}
	if !strings.Contains(p.Wrap(up.URL+"/play", map[string]string{"a": "b"}), "/p/play.bin?") {
		t.Fatal("no extension")
	}
}

func TestIsWebPage(t *testing.T) {
	for u, want := range map[string]bool{
		"https://example.com/play/1-1.html": true,
		"https://example.com/x/index.m3u8":  false,
		"http://127.0.0.1:9979/play?vid=1":  false,
	} {
		if IsWebPage(u) != want {
			t.Error(u)
		}
	}
}

func TestLocalizeURL(t *testing.T) {
	src := "http://127.0.0.1:60444"
	for in, want := range map[string]string{
		"http://127.0.0.1:60444/spider/mock/3/proxy/x.m3u8": "http://127.0.0.1:60444/spider/mock/3/proxy/x.m3u8",
		"http://127.0.0.1:59100/spider/mock/3/proxy/x.m3u8": "http://127.0.0.1:60444/spider/mock/3/proxy/x.m3u8",
		"http://localhost:9988/spider/a/3/proxy/1.m3u8":     "http://127.0.0.1:60444/spider/a/3/proxy/1.m3u8",
		"http://127.0.0.1:9979/play?vid=1":                  "http://127.0.0.1:5555/play?vid=1",
		"https://cdn.example.com/a.m3u8":                    "https://cdn.example.com/a.m3u8",
		"https://127.0.0.1:59100/website/":                  "https://127.0.0.1:59100/website/",
		"data:text/html,hello":                              "data:text/html,hello",
		"javascript:alert(1)":                               "javascript:alert(1)",
		"http://127.0.0.1:59100/other-service/":             "http://127.0.0.1:59100/other-service/",
	} {
		if got := LocalizeURL(in, src, "127.0.0.1:5555"); got != want {
			t.Errorf("%s -> %s", in, got)
		}
	}
	for in, want := range map[string]string{
		"/website/?mode=edit#qr":                       "http://127.0.0.1:60444/website/?mode=edit#qr",
		"website/?mode=edit#qr":                        "http://127.0.0.1:60444/website/?mode=edit#qr",
		"http://127.0.0.1:59100/website/?mode=edit#qr": "http://127.0.0.1:60444/website/?mode=edit#qr",
		"http://[::1]:59100/website/?mode=edit#qr":     "http://127.0.0.1:60444/website/?mode=edit#qr",
		"//127.0.0.1:59100/website/?mode=edit#qr":      "http://127.0.0.1:60444/website/?mode=edit#qr",
	} {
		if got := LocalizeURL(in, src, "127.0.0.1:5555"); got != want {
			t.Errorf("%s -> %s; want %s", in, got, want)
		}
	}
}

func TestLocalizeDoesNotRewriteNonLocalPrivateIP(t *testing.T) {
	for _, candidate := range []string{"10.254.254.253", "172.31.255.254", "192.168.250.251"} {
		if isInterfaceIP(candidate) {
			continue
		}
		if !net.ParseIP(candidate).IsPrivate() {
			t.Fatalf("test address %s is not private", candidate)
		}
		in := "http://" + net.JoinHostPort(candidate, "60444") + "/website/"
		if got := LocalizeURL(in, "http://127.0.0.1:60444", ""); got != in {
			t.Fatalf("non-local private host was rewritten: %s -> %s", in, got)
		}
		return
	}
	t.Fatal("could not choose a non-local private test address")
}

func TestConfigPageURLIsNavigationOnly(t *testing.T) {
	for _, in := range []string{"http://127.0.0.1:60444/website/", "http://127.0.0.1:60444/website?mode=edit#qr"} {
		got, ok := ConfigPageURL(in, "http://127.0.0.1:60444")
		if !ok || !strings.Contains(got, "/website/index.html") {
			t.Fatalf("ConfigPageURL(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"http://127.0.0.1:60444/website/api", "http://127.0.0.1:60443/website/", "https://127.0.0.1:60444/website/"} {
		if got, ok := ConfigPageURL(in, "http://127.0.0.1:60444"); ok || got != in {
			t.Fatalf("unexpected navigation rewrite: %q -> %q", in, got)
		}
	}
}

func TestLocalizeWebsiteFixtureHTTP(t *testing.T) {
	requested := map[string]bool{}
	lanIP := firstNonLoopbackIPv4(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/spider/mock/play":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"parse":0,"url":"http://%s:%s/website/?mode=edit#qr"}`, lanIP, r.Host[strings.LastIndex(r.Host, ":")+1:])
		case "/website/":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			fmt.Fprint(w, `{"code":0,"data":{"sites":[],"jableActors":[],"missavActors":[]}}`)
		case "/website/index.html":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<!doctype html><html><head><title>Plugin config fixture</title><link rel="stylesheet" href="./assets/site.css"></head><body><img src="./assets/qr.png"><script src="./assets/app.js"></script></body></html>`)
		case "/website/assets/site.css", "/website/assets/app.js", "/website/assets/qr.png":
			requested[r.URL.Path] = true
			if strings.HasSuffix(r.URL.Path, ".css") {
				w.Header().Set("Content-Type", "text/css")
			}
			if strings.HasSuffix(r.URL.Path, ".js") {
				w.Header().Set("Content-Type", "application/javascript")
			}
			if strings.HasSuffix(r.URL.Path, ".png") {
				w.Header().Set("Content-Type", "image/png")
			}
			fmt.Fprint(w, "fixture-asset")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	base, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	lanAPI := "http://" + net.JoinHostPort(lanIP, base.Port()) + "/website/?mode=edit#qr"
	localized := LocalizeURL(lanAPI, srv.URL, "")
	if localized != srv.URL+"/website/?mode=edit#qr" {
		t.Fatalf("LAN URL localized to %q", localized)
	}
	staleLAN := "http://" + net.JoinHostPort(lanIP, "59100") + "/website/index.html"
	if got := LocalizeURL(staleLAN, srv.URL, ""); got != srv.URL+"/website/index.html" {
		t.Fatalf("owned stale LAN route localized to %q", got)
	}
	api, err := http.Get(localized)
	if err != nil {
		t.Fatal(err)
	}
	if api.StatusCode != http.StatusOK || !strings.HasPrefix(api.Header.Get("Content-Type"), "application/json") {
		api.Body.Close()
		t.Fatalf("website API content type/status mismatch")
	}
	apiShape, err := io.ReadAll(api.Body)
	api.Body.Close()
	if err != nil || !strings.Contains(string(apiShape), `"jableActors"`) {
		t.Fatalf("website API fixture shape missing")
	}
	isHTML, err := Preflight(nil, localized, nil)
	if err != nil || isHTML {
		t.Fatalf("JSON configuration API was misclassified as HTML: html=%v err=%v", isHTML, err)
	}

	pageURL, ok := ConfigPageURL(localized, srv.URL)
	if !ok {
		t.Fatal("configuration navigation target not recognized")
	}
	isHTML, err = Preflight(nil, pageURL, nil)
	if err != nil || !isHTML {
		t.Fatalf("HTML parse: html=%v err=%v", isHTML, err)
	}
	page, err := http.Get(pageURL)
	if err != nil {
		t.Fatal(err)
	}
	if page.StatusCode != http.StatusOK || !strings.HasPrefix(page.Header.Get("Content-Type"), "text/html") {
		page.Body.Close()
		t.Fatalf("website page response mismatch")
	}
	body, err := io.ReadAll(page.Body)
	page.Body.Close()
	if err != nil || !strings.Contains(string(body), "<title>Plugin config fixture</title>") {
		t.Fatal("website HTML fixture missing expected title")
	}
	parsedPage, _ := url.Parse(pageURL)
	for _, ref := range []string{"./assets/site.css", "./assets/app.js", "./assets/qr.png"} {
		resourceURL := parsedPage.ResolveReference(&url.URL{Path: ref}).String()
		resp, err := http.Get(resourceURL)
		if err != nil {
			t.Fatalf("relative resource %s: %v", ref, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("relative resource %s: HTTP %d", ref, resp.StatusCode)
		}
	}
	if len(requested) != 3 {
		t.Fatalf("relative resources requested: %v", requested)
	}

	// Exercise the real protocol client and the exact Play result transformation.
	client := &Client{Base: srv.URL}
	play, err := client.Play("/spider/mock", "配置", "entry")
	if err != nil {
		t.Fatal(err)
	}
	streams := NormalizePlay(play)
	LocalizePlayStreams(streams, srv.URL, "")
	if len(streams) != 1 || !streams[0].Parse || streams[0].URL != srv.URL+"/website/index.html?mode=edit#qr" {
		t.Fatalf("play config navigation not localized: %+v", streams)
	}
}

func firstNonLoopbackIPv4(t *testing.T) string {
	t.Helper()
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		t.Fatal(err)
	}
	for _, addr := range addrs {
		var ip net.IP
		switch a := addr.(type) {
		case *net.IPNet:
			ip = a.IP
		case *net.IPAddr:
			ip = a.IP
		}
		if ip != nil && !ip.IsLoopback() && ip.To4() != nil {
			return ip.String()
		}
	}
	t.Skip("no non-loopback IPv4 interface available")
	return ""
}

func TestLocalizeOnlyDocumentedVODPosterURL(t *testing.T) {
	vod := map[string]any{"vod_pic": "http://127.0.0.1:59100/website/qr.png", "vod_name": "http://127.0.0.1:59100/website/"}
	result := M{"list": []any{vod}, "message": "http://127.0.0.1:59100/website/"}
	LocalizeVODURLs(result, "http://127.0.0.1:60444", "")
	if vod["vod_pic"] != "http://127.0.0.1:60444/website/qr.png" {
		t.Fatalf("poster was not localized: %#v", vod)
	}
	if vod["vod_name"] != "http://127.0.0.1:59100/website/" || result["message"] != "http://127.0.0.1:59100/website/" {
		t.Fatal("non-URL text was rewritten")
	}
}

func TestClientLocalizesVODPostersOnBrowseEndpoints(t *testing.T) {
	lanIP := firstNonLoopbackIPv4(t)
	var sourceBase string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		base, _ := url.Parse(sourceBase)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"list":[{"vod_pic":"http://%s:%s/website/qr.png","vod_name":"http://%s:%s/website/"}]}`, lanIP, base.Port(), lanIP, base.Port())
	}))
	defer srv.Close()
	sourceBase = srv.URL
	c := &Client{Base: sourceBase}
	requests := []struct {
		name string
		call func() (M, error)
	}{
		{"Home", func() (M, error) { return c.Home("/spider/mock") }},
		{"Category", func() (M, error) { return c.Category("/spider/mock", "1", 1, nil) }},
		{"Detail", func() (M, error) { return c.Detail("/spider/mock", "1") }},
		{"Search", func() (M, error) { return c.Search("/spider/mock", "q", 1) }},
	}
	for _, request := range requests {
		t.Run(request.name, func(t *testing.T) {
			result, err := request.call()
			if err != nil {
				t.Fatal(err)
			}
			vod := result["list"].([]any)[0].(map[string]any)
			if vod["vod_pic"] != sourceBase+"/website/qr.png" {
				t.Fatalf("poster was not localized: %#v", vod)
			}
			if vod["vod_name"] != "http://"+net.JoinHostPort(lanIP, mustPort(t, sourceBase))+"/website/" {
				t.Fatalf("ordinary text was rewritten: %#v", vod["vod_name"])
			}
		})
	}
}

func mustPort(t *testing.T, base string) string {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	return u.Port()
}

func TestPreflight(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "dead") {
			w.WriteHeader(502)
			w.Write([]byte("no resource"))
			return
		}
		w.Write([]byte("#EXTM3U"))
	}))
	defer srv.Close()
	if _, err := Preflight(nil, srv.URL+"/spider/mock/3/proxy/dead.m3u8", nil); err == nil || !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "no resource") {
		t.Fatal(err)
	}
	if html, err := Preflight(nil, srv.URL+"/ok.m3u8", nil); err != nil || html {
		t.Fatal(err)
	}
}

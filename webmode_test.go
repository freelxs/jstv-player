package main

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/freelxs/jstv-player/internal/catvod"
	"golang.org/x/crypto/bcrypt"
)

func init() { bcryptCost = bcrypt.MinCost }

func TestPublicMediaPath(t *testing.T) {
	got, ok := publicMediaPath("http://127.0.0.1:9", "http://127.0.0.1:9/p/abc/stream.m3u8")
	if !ok || got != "/media/p/abc/stream.m3u8" {
		t.Fatalf("%q %v", got, ok)
	}
	if _, ok := publicMediaPath("http://127.0.0.1:9", "http://127.0.0.1:8/p/abc/a.ts"); ok {
		t.Fatal("accepted a different proxy origin")
	}
	if _, ok := publicMediaPath("http://127.0.0.1:9", "http://127.0.0.1:9/ep/1"); ok {
		t.Fatal("accepted a non-proxy path")
	}
	body := rewriteMediaBody("http://127.0.0.1:9", []byte("#EXTM3U\nhttp://127.0.0.1:9/p/abc/seg.ts\n"))
	if string(body) != "#EXTM3U\n/media/p/abc/seg.ts\n" {
		t.Fatalf("%s", body)
	}
}

func TestWebModeDoesNotOpenDesktopPlayer(t *testing.T) {
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	p.webMode.Store(true)
	if err := p.Open(catvod.Stream{URL: "https://example.com/a.mp4"}, "t"); err == nil || !strings.Contains(err.Error(), "网页") {
		t.Fatal(err)
	}
	if _, err := p.PlayEpisodes("/api", "v", "show", "line", []catvod.Episode{{Name: "1", ID: "e"}}, 0); err == nil {
		t.Fatal("PlayEpisodes launched from web mode")
	}
	p.webMode.Store(false)
	if _, err := p.WebPlay("/api", "v", "show", "line", []catvod.Episode{{Name: "1", ID: "e"}}, 0); err == nil || !strings.Contains(err.Error(), "仅网页") {
		t.Fatal(err)
	}
}

func TestInjectWebScriptsBeforeApp(t *testing.T) {
	html := injectWebScripts([]byte(`<script src="request-token.js"></script><script src="app.js"></script>`))
	text := string(html)
	bridge := strings.Index(text, "/bridge.js")
	token := strings.Index(text, "request-token.js")
	app := strings.Index(text, `src="app.js"`)
	player := strings.Index(text, "/web-player.js")
	if bridge < 0 || token < 0 || app < 0 || player < 0 || !(bridge < token && app < player) {
		t.Fatalf("%s", text)
	}
}

func TestWebAuthAndMediaProxy(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, ".m3u8"):
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = io.WriteString(w, "#EXTM3U\n#EXTINF:1,a\nseg.ts\n")
		case strings.HasSuffix(r.URL.Path, ".mp4"):
			if r.Header.Get("Range") != "bytes=0-1" {
				http.Error(w, "expected range", http.StatusBadRequest)
				return
			}
			w.Header().Set("Content-Type", "video/mp4")
			w.Header().Set("Content-Range", "bytes 0-1/4")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write([]byte("ab"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	dir := t.TempDir()
	auth, err := openAuth(dir, "admin", "test-password", false, false)
	if err != nil {
		t.Fatal(err)
	}
	again, err := openAuth(dir, "admin", "different-password", false, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := again.login("203.0.113.5", "admin", "different-password"); err == nil {
		t.Fatal("env password replaced a saved hash without reset")
	}
	p := newPlayer(dir)
	defer p.StopSource()
	p.webMode.Store(true)
	srv := httptest.NewServer(newWebHandler(p, auth))
	defer srv.Close()

	if resp, err := http.Get(srv.URL + "/healthz"); err != nil || resp.StatusCode != 200 {
		t.Fatalf("healthz %v %v", resp, err)
	} else {
		resp.Body.Close()
	}
	resp, err := http.Get(srv.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "登录") {
		t.Fatalf("login page %d %s", resp.StatusCode, body)
	}
	resp, err = http.Get(srv.URL + "/favicon.ico")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("favicon %d", resp.StatusCode)
	}

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Header.Set("Accept", "text/html")
	resp, err = noRedirect.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "/login") {
		t.Fatalf("unauthenticated UI %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	resp, err = http.Post(srv.URL+"/api/call", "application/json", strings.NewReader(`{"method":"GetSettings","args":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated API %d", resp.StatusCode)
	}
	resp, err = http.Get(srv.URL + "/media/p/nope/file.mp4")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated media %d", resp.StatusCode)
	}

	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar}
	if resp := jsonReq(t, client, srv.URL+"/api/login", `{"username":"admin","password":"wrong-password"}`); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("wrong password %d", resp.StatusCode)
	} else {
		resp.Body.Close()
		if len(jar.Cookies(mustURL(srv.URL))) != 0 {
			t.Fatal("wrong password set a session cookie")
		}
	}
	resp = jsonReq(t, client, srv.URL+"/api/login", `{"username":"admin","password":"test-password"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login %d", resp.StatusCode)
	}
	resp = jsonReq(t, client, srv.URL+"/api/call", `{"method":"GetSettings","args":[]}`)
	logged, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(logged), `"result"`) {
		t.Fatalf("settings %d %s", resp.StatusCode, logged)
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	req.Header.Set("Accept", "text/html")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(page), "/bridge.js") || !strings.Contains(string(page), "/web-player.js") {
		t.Fatalf("index missing web scripts: %s", page)
	}

	playlist := upstream.URL + "/a.m3u8"
	if err := p.proxy.Grant(playlist, nil, false); err != nil {
		t.Fatal(err)
	}
	wrapped, err := p.proxy.WrapGranted(playlist, nil)
	if err != nil {
		t.Fatal(err)
	}
	pub, ok := publicMediaPath(p.proxy.BaseURL(), wrapped)
	if !ok {
		t.Fatal(wrapped)
	}
	resp, err = client.Get(srv.URL + pub)
	if err != nil {
		t.Fatal(err)
	}
	media, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(media), "/media/p/") || strings.Contains(string(media), upstream.URL) || strings.Contains(string(media), p.proxy.BaseURL()) {
		t.Fatalf("playlist rewrite %d %s", resp.StatusCode, media)
	}

	file := upstream.URL + "/a.mp4"
	if err := p.proxy.Grant(file, nil, false); err != nil {
		t.Fatal(err)
	}
	wrapped, err = p.proxy.WrapGranted(file, nil)
	if err != nil {
		t.Fatal(err)
	}
	pub, ok = publicMediaPath(p.proxy.BaseURL(), wrapped)
	if !ok {
		t.Fatal(wrapped)
	}
	req, _ = http.NewRequest(http.MethodGet, srv.URL+pub, nil)
	req.Header.Set("Range", "bytes=0-1")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	part, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusPartialContent || string(part) != "ab" {
		t.Fatalf("range %d %q", resp.StatusCode, part)
	}

	for _, raw := range []string{"http://127.0.0.1:9/a.jpg", "http://169.254.169.254/latest", "http://10.1.2.3/a.png", "http://localhost/a.png"} {
		resp, err = client.Get(srv.URL + "/poster?u=" + urlQuery(raw))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("poster %s status %d", raw, resp.StatusCode)
		}
	}

	resp = jsonReq(t, client, srv.URL+"/api/logout", `{}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("logout %d", resp.StatusCode)
	}
	resp = jsonReq(t, client, srv.URL+"/api/call", `{"method":"GetSettings","args":[]}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("call after logout %d", resp.StatusCode)
	}
	saved, err := os.ReadFile(dir + "/auth.json")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(saved), "test-password") {
		t.Fatal("auth file stored the password")
	}
}

func TestLoginRateLimit(t *testing.T) {
	dir := t.TempDir()
	auth, err := openAuth(dir, "admin", "test-password", false, false)
	if err != nil {
		t.Fatal(err)
	}
	p := newPlayer(dir)
	defer p.StopSource()
	p.webMode.Store(true)
	srv := httptest.NewServer(newWebHandler(p, auth))
	defer srv.Close()
	client := &http.Client{}
	for i := 0; i < 10; i++ {
		resp := jsonReq(t, client, srv.URL+"/api/login", `{"username":"admin","password":"nope-nope"}`)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d status %d", i, resp.StatusCode)
		}
	}
	resp := jsonReq(t, client, srv.URL+"/api/login", `{"username":"admin","password":"nope-nope"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("rate limit %d", resp.StatusCode)
	}
}

func jsonReq(t *testing.T, client *http.Client, url, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-JSTV-Request", "1")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func mustURL(raw string) *url.URL {
	u, err := url.Parse(raw)
	if err != nil {
		panic(err)
	}
	return u
}

func urlQuery(raw string) string { return url.QueryEscape(raw) }

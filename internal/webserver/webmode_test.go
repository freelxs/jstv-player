package webserver

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/freelxs/jstv-player/internal/app"
	"golang.org/x/crypto/bcrypt"
)

func init() { bcryptCost = bcrypt.MinCost }

func TestRewriteMediaBody(t *testing.T) {
	body := rewriteMediaBody("http://127.0.0.1:9", []byte("#EXTM3U\nhttp://127.0.0.1:9/p/abc/seg.ts\n"))
	if string(body) != "#EXTM3U\n/media/p/abc/seg.ts\n" {
		t.Fatalf("%s", body)
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
	runtime := app.New(dir, os.DirFS("../.."), true)
	p := runtime.Player
	defer p.StopSource()
	srv := httptest.NewServer(newWebHandler(runtime, auth, os.DirFS("../..")))
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
	if err := runtime.MediaProxy.Grant(playlist, nil, false); err != nil {
		t.Fatal(err)
	}
	wrapped, err := runtime.MediaProxy.WrapGranted(playlist, nil)
	if err != nil {
		t.Fatal(err)
	}
	pub, ok := testPublicMediaPath(runtime.MediaProxy.BaseURL(), wrapped)
	if !ok {
		t.Fatal(wrapped)
	}
	resp, err = client.Get(srv.URL + pub)
	if err != nil {
		t.Fatal(err)
	}
	media, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(media), "/media/p/") || strings.Contains(string(media), upstream.URL) || strings.Contains(string(media), runtime.MediaProxy.BaseURL()) {
		t.Fatalf("playlist rewrite %d %s", resp.StatusCode, media)
	}

	file := upstream.URL + "/a.mp4"
	if err := runtime.MediaProxy.Grant(file, nil, false); err != nil {
		t.Fatal(err)
	}
	wrapped, err = runtime.MediaProxy.WrapGranted(file, nil)
	if err != nil {
		t.Fatal(err)
	}
	pub, ok = testPublicMediaPath(runtime.MediaProxy.BaseURL(), wrapped)
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
	runtime := app.New(dir, os.DirFS("../.."), true)
	p := runtime.Player
	defer p.StopSource()
	srv := httptest.NewServer(newWebHandler(runtime, auth, os.DirFS("../..")))
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

func TestLoginRateLimitBehindTrustedProxyAndBoundedFails(t *testing.T) {
	dir := t.TempDir()
	auth, err := openAuth(dir, "admin", "test-password", false, false)
	if err != nil {
		t.Fatal(err)
	}
	auth.setTrustedProxies("127.0.0.1/8, ::1")
	runtime := app.New(dir, os.DirFS("../.."), true)
	defer runtime.Player.StopSource()
	srv := httptest.NewServer(newWebHandler(runtime, auth, os.DirFS("../..")))
	defer srv.Close()
	client := &http.Client{}

	for i := 0; i < 10; i++ {
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/login", strings.NewReader(`{"username":"admin","password":"wrong-password"}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-JSTV-Request", "1")
		// Spoof a rotating leftmost IP while the trusted proxy appends the true attacker IP 198.51.100.77.
		req.Header.Set("X-Forwarded-For", "203.0.113."+strconv.Itoa(i+1)+", 198.51.100.77")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d status %d", i, resp.StatusCode)
		}
	}

	// 11th attempt from attacker IP 198.51.100.77 must be rate-limited even with a new spoofed leftmost XFF entry.
	attackerReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/login", strings.NewReader(`{"username":"admin","password":"test-password"}`))
	attackerReq.Header.Set("Content-Type", "application/json")
	attackerReq.Header.Set("X-JSTV-Request", "1")
	attackerReq.Header.Set("X-Forwarded-For", "192.0.2.99, 198.51.100.77")
	attackerResp, err := client.Do(attackerReq)
	if err != nil {
		t.Fatal(err)
	}
	attackerResp.Body.Close()
	if attackerResp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected attacker IP to be rate limited, got %d", attackerResp.StatusCode)
	}

	// Legitimate admin behind the same reverse proxy with a different client IP must NOT be locked out.
	adminReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/login", strings.NewReader(`{"username":"admin","password":"test-password"}`))
	adminReq.Header.Set("Content-Type", "application/json")
	adminReq.Header.Set("X-JSTV-Request", "1")
	adminReq.Header.Set("X-Forwarded-For", "203.0.113.200")
	adminResp, err := client.Do(adminReq)
	if err != nil {
		t.Fatal(err)
	}
	adminResp.Body.Close()
	if adminResp.StatusCode != http.StatusOK {
		t.Fatalf("expected legitimate admin behind proxy to succeed, got %d", adminResp.StatusCode)
	}

	// Verify fails table stays bounded by maxFailEntries.
	for i := 0; i < maxFailEntries+200; i++ {
		auth.noteFail("198.18." + strconv.Itoa(i/256) + "." + strconv.Itoa(i%256))
	}
	auth.mu.Lock()
	count := len(auth.fails)
	auth.mu.Unlock()
	if count > maxFailEntries {
		t.Fatalf("fails map grew beyond maxFailEntries: %d > %d", count, maxFailEntries)
	}
}

func TestDecodeJSONEnforcesReadDeadline(t *testing.T) {
	origTimeout := jsonReadTimeout
	jsonReadTimeout = 40 * time.Millisecond
	defer func() { jsonReadTimeout = origTimeout }()

	dir := t.TempDir()
	auth, err := openAuth(dir, "admin", "test-password", false, false)
	if err != nil {
		t.Fatal(err)
	}
	runtime := app.New(dir, os.DirFS("../.."), true)
	defer runtime.Player.StopSource()
	srv := httptest.NewServer(newWebHandler(runtime, auth, os.DirFS("../..")))
	defer srv.Close()

	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte(`{"username":"admin"`))
		time.Sleep(120 * time.Millisecond)
		_ = pw.Close()
	}()
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/login", pr)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-JSTV-Request", "1")
	resp, err := (&http.Client{Timeout: 2 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected slow JSON body to fail with 400, got %d", resp.StatusCode)
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

func testPublicMediaPath(base, wrapped string) (string, bool) {
	rest, ok := strings.CutPrefix(wrapped, base+"/p/")
	return "/media/p/" + rest, ok
}

package catvod

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStrictMD5RejectsBeforeReplacingScript(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "source")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(sourceDir, "index.js")
	if err := os.WriteFile(path, []byte("previous trusted script"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.js.md5":
			_, _ = w.Write([]byte("00000000000000000000000000000000"))
		case "/index.js":
			_, _ = w.Write([]byte("new script body with enough bytes"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	if _, err := FetchWithOptions(nil, server.URL+"/index.js.md5", dir, true); err == nil || !strings.Contains(err.Error(), "md5 校验失败") {
		t.Fatalf("expected strict mismatch, got %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "previous trusted script" {
		t.Fatalf("script replaced on strict failure: %q", b)
	}
}

func TestDownloadTempRejectsLimitPlusOneAndCleansFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("123456")) }))
	defer server.Close()
	dir := t.TempDir()
	_, _, _, _, err := downloadTemp(http.DefaultClient, server.URL, dir, 5, false)
	if !errors.Is(err, errTooLarge) {
		t.Fatalf("expected size error, got %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("temporary download leaked: %v", entries)
	}
}

func TestFetchPublishesBothDigestsAndConfigAtomically(t *testing.T) {
	script := []byte("const stable = 'valid plugin bundle';")
	sum := md5.Sum(script)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.js.md5":
			_, _ = w.Write([]byte(hex.EncodeToString(sum[:])))
		case "/index.js":
			_, _ = w.Write(script)
		case "/index.config.js":
			_, _ = w.Write([]byte("module.exports = {}"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	src, err := FetchWithOptions(nil, server.URL+"/index.js.md5", t.TempDir(), true)
	if err != nil {
		t.Fatal(err)
	}
	if src.MD5 != hex.EncodeToString(sum[:]) || len(src.SHA256) != 64 {
		t.Fatalf("digests missing: %+v", src)
	}
	if src.ConfigPath == "" {
		t.Fatal("config was not atomically published")
	}
	b, err := os.ReadFile(src.Path)
	if err != nil || string(b) != string(script) {
		t.Fatalf("script mismatch: %v", err)
	}
}

func TestConfigFailureRetainsCompletePublishedVersion(t *testing.T) {
	dir := t.TempDir()
	script := "module.exports = { start: function() {} };"
	config := "module.exports = {version:1};"
	fail := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/index.js":
			_, _ = w.Write([]byte(script))
		case "/index.config.js":
			if fail {
				http.Error(w, "offline", 500)
				return
			}
			_, _ = w.Write([]byte(config))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	old, err := Fetch(nil, server.URL+"/index.js", dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := PublishSource(dir, old); err != nil {
		t.Fatal(err)
	}
	script += "// new script"
	fail = true
	if _, err := Fetch(nil, server.URL+"/index.js", dir); err == nil {
		t.Fatal("failed config download accepted")
	}
	cached, err := CachedSource(dir)
	if err != nil || cached.BundleSHA256 != old.BundleSHA256 {
		t.Fatalf("published version changed: %+v %v", cached, err)
	}
	b, err := os.ReadFile(cached.ConfigPath)
	if err != nil || string(b) != config {
		t.Fatalf("old configuration lost: %q %v", b, err)
	}
}

func TestConfigURLPreservesQueryAndCaseInsensitiveExtension(t *testing.T) {
	for _, path := range []string{"index.js", "Index.JS"} {
		var got string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasSuffix(strings.ToLower(r.URL.Path), ".config.js") {
				got = r.URL.RequestURI()
				_, _ = w.Write([]byte("module.exports={};"))
				return
			}
			if strings.HasSuffix(strings.ToLower(r.URL.Path), ".js") {
				_, _ = w.Write([]byte("module.exports={start:()=>{}};"))
				return
			}
			http.NotFound(w, r)
		}))
		_, err := Fetch(nil, server.URL+"/"+path+"?token=a%2Bb&v=2", t.TempDir())
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		expected := "/" + strings.TrimSuffix(strings.TrimSuffix(path, ".js"), ".JS") + ".config.js?token=a%2Bb&v=2"
		if got != expected {
			t.Fatalf("config path %q want %q", got, expected)
		}
	}
}

func TestCorruptCacheIsRejectedAndRefreshRepairsIntoNewVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.js" {
			w.Write([]byte("module.exports={start:function(){}};"))
		} else {
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	src, err := Fetch(nil, server.URL+"/index.js", dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := PublishSource(dir, src); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src.Path, []byte("tampered cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CachedSource(dir); err == nil {
		t.Fatal("tampered bytes accepted")
	}
	repaired, err := Fetch(nil, server.URL+"/index.js", dir)
	if err != nil {
		t.Fatal(err)
	}
	if repaired.Path == src.Path || repaired.BundleSHA256 != src.BundleSHA256 {
		t.Fatal("refresh overwrote the immutable corrupt version")
	}
	if err := PublishSource(dir, repaired); err != nil {
		t.Fatal(err)
	}
	if _, err := CachedSource(dir); err != nil {
		t.Fatal(err)
	}
}

func TestConditionalRefreshChecksConfigAndForceBypassesValidators(t *testing.T) {
	script := []byte("module.exports={start:function(){}};")
	config := []byte("module.exports={version:1};")
	configETag := "config-one"
	fullScript, fullConfig, notModified := 0, 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body []byte
		var tag string
		switch r.URL.Path {
		case "/index.js":
			body, tag = script, "script-one"
		case "/index.config.js":
			body, tag = config, configETag
		default:
			http.NotFound(w, r)
			return
		}
		w.Header().Set("ETag", tag)
		if r.Header.Get("If-None-Match") == tag {
			notModified++
			w.WriteHeader(304)
			return
		}
		if r.URL.Path == "/index.js" {
			fullScript++
		} else {
			fullConfig++
		}
		w.Write(body)
	}))
	defer server.Close()
	dir := t.TempDir()
	address := server.URL + "/index.js"
	first, err := Fetch(nil, address, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := PublishSource(dir, first); err != nil {
		t.Fatal(err)
	}
	second, err := Fetch(nil, address, dir)
	if err != nil {
		t.Fatal(err)
	}
	if second.BundleSHA256 != first.BundleSHA256 || fullScript != 1 || fullConfig != 1 || notModified != 2 {
		t.Fatalf("conditional refresh: full=%d/%d unchanged=%d", fullScript, fullConfig, notModified)
	}
	config = []byte("module.exports={version:2};")
	configETag = "config-two"
	third, err := Fetch(nil, address, dir)
	if err != nil {
		t.Fatal(err)
	}
	if third.SHA256 != first.SHA256 || third.BundleSHA256 == first.BundleSHA256 || fullConfig != 2 {
		t.Fatal("script 304 hid an executable config update")
	}
	if _, err := FetchRefreshContext(context.Background(), nil, address, dir, false, true); err != nil {
		t.Fatal(err)
	}
	if fullScript != 2 || fullConfig != 3 {
		t.Fatal("force refresh still used validators")
	}
}

package catvod

import (
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

func TestScriptPublishFailureDoesNotDeleteCachedConfig(t *testing.T) {
	dir := t.TempDir()
	sourceDir := filepath.Join(dir, "source")
	if err := os.MkdirAll(filepath.Join(sourceDir, "index.js"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(sourceDir, "index.config.js")
	if err := os.WriteFile(configPath, []byte("previous config"), 0o644); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.js.md5" {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/index.js" {
			_, _ = w.Write([]byte("replacement script body is long enough"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	if _, err := FetchWithOptions(nil, server.URL+"/index.js", dir, false); err == nil {
		t.Fatal("expected script publication failure")
	}
	b, err := os.ReadFile(configPath)
	if err != nil || string(b) != "previous config" {
		t.Fatalf("cached config was removed before failed script publication: %q, %v", b, err)
	}
}

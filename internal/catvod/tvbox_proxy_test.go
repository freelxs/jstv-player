package catvod

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
)

func TestPlaylistRewriteDropsAdSegment(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = w.Write([]byte("#EXTM3U\nad.ts\nseg.ts\n"))
	}))
	defer up.Close()
	p := &HeaderProxy{}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	p.SetUpstream(nil, func(host string, body []byte) []byte {
		if host == "" {
			t.Fatal("missing host")
		}
		return []byte(strings.ReplaceAll(string(body), "ad.ts\n", ""))
	})
	resp, err := http.Get(p.WrapAlways(up.URL+"/live.m3u8", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if strings.Contains(string(b), "ad.ts") || !strings.Contains(string(b), "seg.ts") {
		t.Fatalf("%s", b)
	}
}

func TestFLVWithoutFFmpegExplainsTheGap(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err == nil {
		t.Skip("ffmpeg is installed; the missing-tool message is covered when it is not")
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("FLV"))
	}))
	defer up.Close()
	p := &HeaderProxy{}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(p.WrapAlways(up.URL+"/stream.flv", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusBadGateway || !strings.Contains(string(b), "ffmpeg") {
		t.Fatalf("%d %s", resp.StatusCode, b)
	}
}

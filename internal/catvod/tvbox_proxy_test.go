package catvod

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
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

func TestFLVRemuxesToFragmentedMP4WhenFFmpegExists(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	flv := filepath.Join(dir, "clip.flv")
	cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc=size=160x120:rate=5", "-t", "0.4", "-c:v", "libx264", "-pix_fmt", "yuv420p", "-f", "flv", flv)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("make flv: %v %s", err, out)
	}
	body, err := os.ReadFile(flv)
	if err != nil {
		t.Fatal(err)
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "video/x-flv")
		_, _ = w.Write(body)
	}))
	defer up.Close()
	p := &HeaderProxy{}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(p.WrapAlways(up.URL+"/stream.flv", map[string]string{"User-Agent": "fixture"}))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK || !bytes.Contains(got, []byte("ftyp")) {
		t.Fatalf("status %d bytes %d", resp.StatusCode, len(got))
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

func TestProxyBlocksDNSRebindingAndDialRewriteToRestrictedIPs(t *testing.T) {
	secretHit := false
	secret := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secretHit = true
		_, _ = w.Write([]byte("loopback-secret"))
	}))
	defer secret.Close()
	_, port, err := net.SplitHostPort(strings.TrimPrefix(secret.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}

	origLookup := lookupDialIPs
	lookupDialIPs = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if host == "rebind.example.com" {
			return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
		}
		if host == "metadata.example.com" {
			return []net.IPAddr{{IP: net.ParseIP("169.254.169.254")}}, nil
		}
		return origLookup(ctx, host)
	}
	defer func() { lookupDialIPs = origLookup }()

	p := &HeaderProxy{}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{
		"http://rebind.example.com:" + port + "/seg.ts",
		"http://metadata.example.com:" + port + "/seg.ts",
	} {
		resp, err := http.Get(p.WrapAlways(target, nil))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadGateway || secretHit {
			t.Fatalf("DNS rebinding to %s was not blocked: status=%d secretHit=%v", target, resp.StatusCode, secretHit)
		}
	}

	p.SetDialRewrite(func(addr string) string {
		if strings.HasPrefix(addr, "cdn.example.com:") {
			return "127.0.0.1:" + port
		}
		return addr
	})
	resp, err := http.Get(p.WrapAlways("http://cdn.example.com:"+port+"/seg.ts", nil))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway || secretHit {
		t.Fatalf("dial rewrite to loopback was not blocked: status=%d secretHit=%v", resp.StatusCode, secretHit)
	}
}

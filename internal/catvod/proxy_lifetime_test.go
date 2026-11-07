package catvod

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func proxyID(raw string) string {
	u, _ := url.Parse(raw)
	return strings.Split(strings.TrimPrefix(u.Path, "/p/"), "/")[0]
}

func TestLiveLeaseRenewsButOrdinaryLeaseDoesNot(t *testing.T) {
	p := &HeaderProxy{}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	if err := p.GrantLive("https://example.com/live.m3u8", nil, false); err != nil {
		t.Fatal(err)
	}
	live, err := p.WrapGranted("https://example.com/live.m3u8", nil)
	if err != nil {
		t.Fatal(err)
	}
	normal := p.WrapAlways("https://example.com/vod.m3u8", nil)
	near := time.Now().Add(time.Minute)
	for _, raw := range []string{live, normal} {
		id := proxyID(raw)
		p.mu.Lock()
		entry := p.resources[id]
		entry.Expires = near
		p.resources[id] = entry
		p.mu.Unlock()
	}
	liveEntry, ok := p.resource(proxyID(live))
	if !ok || liveEntry.Expires.Before(time.Now().Add(time.Hour)) {
		t.Fatal("active live lease did not renew")
	}
	entry, ok := p.resource(proxyID(normal))
	if !ok || !entry.Expires.Equal(near) {
		t.Fatal("ordinary lease unexpectedly renewed")
	}
	p.Reset()
	if _, ok := p.resource(proxyID(live)); ok {
		t.Fatal("stop did not revoke renewable lease")
	}
	if result := p.wrapSessionAuthorized("https://example.com/segment.ts", nil, liveEntry.Session, "seg.ts", false, true); result != "" {
		t.Fatal("revoked session minted new resources")
	}
}

func TestRepeatedLivePlaylistsReuseResourcesAndResetBlocksInflightRewrite(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("block") == "1" {
			close(entered)
			<-release
		}
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		fmt.Fprint(w, "#EXTM3U\n#EXTINF:6,\nsegment.ts\n")
	}))
	defer upstream.Close()
	p := &HeaderProxy{}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	root := p.WrapAlways(upstream.URL+"/live.m3u8", nil)
	for i := 0; i < 20; i++ {
		resp, err := http.Get(root)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatal(resp.StatusCode)
		}
	}
	p.mu.Lock()
	count := len(p.resources)
	p.mu.Unlock()
	if count != 2 {
		t.Fatalf("repeated playlist minted %d resources, want root and one segment", count)
	}
	blocked := p.WrapAlways(upstream.URL+"/live.m3u8?block=1", nil)
	result := make(chan int, 1)
	go func() {
		resp, err := http.Get(blocked)
		if err != nil {
			result <- 0
			return
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		result <- resp.StatusCode
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("upstream never entered")
	}
	p.Reset()
	close(release)
	if status := <-result; status == 200 {
		t.Fatal("inflight request republished resources after reset")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.resources) != 0 {
		t.Fatal("revoked resources reappeared")
	}
}

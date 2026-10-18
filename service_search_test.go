package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/freelxs/jstv-player/internal/catvod"
)

func TestSearchAllStopsWhenContextEnds(t *testing.T) {
	node, err := catvod.FindNode("")
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	notify := filepath.Join(dir, "search-started")
	script := filepath.Join(dir, "hangsearch.js")
	code := `const fs = require('fs');
module.exports = { start() {
  const s = globalThis.catServerFactory(async (req, res) => {
    const p = req.url.split('?')[0];
    const send = (o) => { res.setHeader('content-type', 'application/json'); res.end(JSON.stringify(o)); };
    if (p === '/config') return send({ video: { sites: [{ key: 'h', name: 'H', type: 3, api: '/spider/h/3', searchable: 1 }] } });
    if (p === '/spider/h/3/init') return send({});
    if (p === '/spider/h/3/search') {
      fs.writeFileSync(` + "`" + notify + "`" + `, '1');
      await new Promise(() => {});
      return;
    }
    res.statusCode = 404; res.end('no');
  });
  s.listen();
}, stop() {} };
`
	if err := os.WriteFile(script, []byte(code), 0o600); err != nil {
		t.Fatal(err)
	}
	p := newPlayer(dir)
	defer p.StopSource()
	h := &catvod.Host{Node: node, Dir: filepath.Join(dir, "runtime"), Script: script}
	if err := h.Start(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	defer h.Stop()
	p.mu.Lock()
	p.host = h
	p.sites = []catvod.Site{{Key: "h", Name: "H", API: "/spider/h/3", Type: 3}}
	p.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- p.SearchAll(ctx, "万物生灵", &mygo.Channel[SiteResults]{})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(notify); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("search never reached the plugin")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("SearchAll kept waiting after cancellation")
	}
}

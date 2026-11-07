package app

import (
	"context"
	"encoding/json"
	"github.com/freelxs/jstv-player/internal/catvod"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRecentlyFailedSearchSiteRunsAfterHealthySitesAndRecovers(t *testing.T) {
	if _, err := catvod.FindNode(""); err != nil {
		t.Skip(err)
	}
	script := `let server;let events=[];
exports.start=()=>{server=catServerFactory(async(req,res)=>{
 const p=req.url.split('?')[0],send=o=>{res.setHeader('Content-Type','application/json');res.end(JSON.stringify(o))};
 if(p==='/config')return send({video:{sites:['bad',1,2,3,4,5,6].map(id=>({key:String(id),name:String(id),type:3,api:'/site/'+id}))}});
 if(p==='/events')return send(events);
 if(p.endsWith('/init'))return send({});
 if(p.endsWith('/search')){events.push(p);await new Promise(r=>setTimeout(r,100));return send({list:[]})}
 res.statusCode=404;res.end('no');});server.listen(0)};exports.stop=()=>server&&server.close();`
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/index.js" {
			io.WriteString(w, script)
		} else {
			http.NotFound(w, r)
		}
	}))
	defer feed.Close()
	p := newPlayer(t.TempDir())
	defer p.StopSource()
	settings, err := p.AddSubscription("health", feed.URL+"/index.js")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadSource(); err != nil {
		t.Fatal(err)
	}
	if err := p.ApproveSource(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadSource(); err != nil {
		t.Fatal(err)
	}
	key := settings.Active + "\x00/site/bad"
	p.mu.Lock()
	p.searchHealth = map[string]siteHealth{key: {failures: 5, lastFailure: time.Now()}}
	p.mu.Unlock()
	results := 0
	if err := p.streamSearch(context.Background(), "fixture", func(result SiteResults) error { results++; return nil }); err != nil {
		t.Fatal(err)
	}
	if results != 7 {
		t.Fatalf("empty successful sites must still report status, got %d", results)
	}
	resp, err := http.Get(p.host.Base() + "/events")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var events []string
	if err := json.NewDecoder(resp.Body).Decode(&events); err != nil {
		t.Fatal(err)
	}
	if len(events) != 7 || events[6] != "/site/bad/search" {
		t.Fatalf("failed source was not scheduled last: %v", events)
	}
	p.mu.Lock()
	score := p.searchHealth[key].score()
	p.mu.Unlock()
	if score != 0 {
		t.Fatal("successful retry did not recover health")
	}
}

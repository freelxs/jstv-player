package main

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/freelxs/jstv-player/internal/catvod"
)

func TestInitFailureRetriesAndConcurrentSuccessIsSingleFlight(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/spider/mock/3/init" {
			http.NotFound(w, r)
			return
		}
		if calls.Add(1) == 1 {
			http.Error(w, "temporary", http.StatusServiceUnavailable)
			return
		}
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	p := &Player{inited: map[string]bool{}}
	c := &catvod.Client{Base: srv.URL}
	if err := p.initSite("/spider/mock/3", c, nil); err == nil {
		t.Fatal("first init should fail")
	}
	if p.inited["/spider/mock/3"] {
		t.Fatal("failed init was marked ready")
	}
	var errs atomic.Int32
	done := make(chan struct{}, 2)
	for i := 0; i < 2; i++ {
		go func() {
			if err := p.initSite("/spider/mock/3", c, nil); err != nil {
				errs.Add(1)
			}
			done <- struct{}{}
		}()
	}
	<-done
	<-done
	if errs.Load() != 0 || calls.Load() != 2 {
		t.Fatalf("errs=%d calls=%d", errs.Load(), calls.Load())
	}
}

func TestOpenRejectsUnregisteredProxyTarget(t *testing.T) {
	p := &Player{proxy: &catvod.HeaderProxy{}, settings: Settings{Player: "builtin"}}
	err := p.Open(catvod.Stream{URL: "http://127.0.0.1:9/arbitrary"}, "test")
	if err == nil {
		t.Fatal("Open accepted a URL not returned by an authorized play response")
	}
}

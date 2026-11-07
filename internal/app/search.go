package app

import (
	"context"
	"sync"

	"github.com/egoist/mygo"
	"github.com/freelxs/jstv-player/internal/catvod"
)

// SearchAll searches every searchable site at once, streaming each site's
// answer to the page as it arrives.
func (p *Player) SearchAll(ctx context.Context, wd string, out *mygo.Channel[SiteResults]) error {
	return p.streamSearch(ctx, wd, func(r SiteResults) error { return out.Send(r) })
}

// streamSearch is SearchAll without a desktop channel, so the web server can
// write each result as it arrives. Send errors cancel the rest.
func (p *Player) streamSearch(ctx context.Context, wd string, send func(SiteResults) error) error {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p.mu.Lock()
	sites := append([]catvod.Site(nil), p.sites...)
	p.mu.Unlock()
	var wg sync.WaitGroup
	var sendMu sync.Mutex
	var sendErr error
	sem := make(chan struct{}, 6)
	for _, s := range sites {
		if !s.CanSearch() {
			continue
		}
		wg.Add(1)
		go func(s catvod.Site) {
			defer wg.Done()
			select {
			case <-ctx.Done():
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			r := SiteResults{Site: s}
			c, err := p.clientWithContext(ctx, s.API)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				r.Error = err.Error()
			} else {
				var m catvod.M
				m, err = c.Search(s.API, wd, 1)
				if ctx.Err() != nil {
					return
				}
				if err != nil {
					r.Error = err.Error()
				} else if l, ok := m["list"].([]any); ok {
					r.List = l
				}
			}
			if len(r.List) == 0 && r.Error == "" {
				return
			}
			sendMu.Lock()
			defer sendMu.Unlock()
			if sendErr != nil || ctx.Err() != nil {
				return
			}
			if err := send(r); err != nil {
				sendErr = err
				cancel()
			}
		}(s)
	}
	wg.Wait()
	if sendErr != nil {
		return sendErr
	}
	return ctx.Err()
}

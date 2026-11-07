package app

import (
	"context"
	"sort"
	"sync"
	"time"

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
	generation, sourceCtx, subscription := p.sourceGeneration, p.sourceContext, p.settings.Active
	sort.SliceStable(sites, func(i, j int) bool {
		return p.searchHealth[subscription+"\x00"+sites[i].API].score() < p.searchHealth[subscription+"\x00"+sites[j].API].score()
	})
	p.mu.Unlock()
	if sourceCtx != nil {
		stop := context.AfterFunc(sourceCtx, cancel)
		defer stop()
	}
	var wg sync.WaitGroup
	var sendMu sync.Mutex
	var sendErr error
	sem := make(chan struct{}, 6)
	for _, s := range sites {
		if !s.CanSearch() {
			continue
		}
		select {
		case <-ctx.Done():
			break
		case sem <- struct{}{}:
		}
		if ctx.Err() != nil {
			break
		}
		wg.Add(1)
		go func(s catvod.Site) {
			defer wg.Done()
			defer func() { <-sem }()
			if ctx.Err() != nil {
				return
			}
			r := SiteResults{Site: s}
			if catalog, err, native := p.nativeCatalog(s.API); native {
				if err != nil {
					r.Error = err.Error()
				} else {
					r.List = liveCategory(catalog, "", wd, 1)["list"].([]any)
				}
			} else {
				c, err := p.clientWithContext(ctx, s.API)
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					r.Error = err.Error()
				} else {
					m, err := c.Search(s.API, wd, 1)
					if ctx.Err() != nil {
						return
					}
					if err != nil {
						r.Error = err.Error()
					} else if list, ok := m["list"].([]any); ok {
						r.List = list
					}
				}
			}
			p.mu.Lock()
			if generation != p.sourceGeneration {
				p.mu.Unlock()
				return
			}
			if p.searchHealth == nil {
				p.searchHealth = map[string]siteHealth{}
			}
			key := subscription + "\x00" + s.API
			health := p.searchHealth[key]
			if r.Error != "" {
				health.failures = min(health.failures+1, 10)
				health.lastFailure = time.Now()
			} else {
				health = siteHealth{}
			}
			p.searchHealth[key] = health
			p.mu.Unlock()
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

type siteHealth struct {
	failures    int
	lastFailure time.Time
}

func (h siteHealth) score() int {
	if time.Since(h.lastFailure) > 10*time.Minute {
		return 0
	}
	return h.failures
}

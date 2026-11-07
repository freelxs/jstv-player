package app

import (
	"encoding/json"
	"time"

	"github.com/freelxs/jstv-player/internal/catvod"
)

type catalogEntry struct {
	data    []byte
	expires time.Time
}

// cachedCatalog owns copies of successful values. Entries are bounded both in
// count and size and are invalidated by source changes and configuration saves.
func (p *Player) cachedCatalog(api, action string, params any, fetch func() (catvod.M, error)) (catvod.M, error) {
	keyBytes, _ := json.Marshal([]any{api, action, params})
	key := string(keyBytes)
	p.mu.Lock()
	generation, epoch := p.sourceGeneration, p.catalogEpoch
	entry, ok := p.catalogCache[key]
	p.mu.Unlock()
	if ok && time.Now().Before(entry.expires) {
		var value catvod.M
		if json.Unmarshal(entry.data, &value) == nil {
			return value, nil
		}
	}
	value, err := fetch()
	if err != nil {
		return nil, err
	}
	data, marshalErr := json.Marshal(value)
	p.mu.Lock()
	defer p.mu.Unlock()
	if generation != p.sourceGeneration || epoch != p.catalogEpoch {
		return nil, errSourceChanged
	}
	if marshalErr == nil && len(data) <= 256<<10 {
		if p.catalogCache == nil {
			p.catalogCache = map[string]catalogEntry{}
		}
		if len(p.catalogCache) >= 32 {
			for k := range p.catalogCache {
				delete(p.catalogCache, k)
				break
			}
		}
		ttl := 30 * time.Second
		if action == "home" {
			ttl = time.Minute
		}
		p.catalogCache[key] = catalogEntry{data: data, expires: time.Now().Add(ttl)}
	}
	return value, nil
}

func (p *Player) invalidateCatalogLocked() { p.catalogEpoch++; p.catalogCache = nil }

// InvalidateConfiguration discards values obtained before a configuration save.
func (r *Runtime) InvalidateConfiguration(generation uint64) {
	p := r.Player
	p.mu.Lock()
	defer p.mu.Unlock()
	if generation == p.sourceGeneration {
		p.invalidateCatalogLocked()
	}
}

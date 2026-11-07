package app

import (
	"context"
	"io/fs"

	"github.com/freelxs/jstv-player/internal/catvod"
)

// Runtime provides deployment adapters access to application infrastructure
// without adding methods to the Player interface exposed through MyGo.
type Runtime struct {
	Player     *Player
	MediaProxy *catvod.HeaderProxy
}

// New constructs the application using the data directory and embedded assets.
func New(dir string, assets fs.FS, webMode bool) *Runtime {
	p := newConfiguredPlayer(dir, assets)
	p.webMode.Store(webMode)
	return &Runtime{Player: p, MediaProxy: p.proxy}
}

// StreamSearch sends each site's result as it arrives.
func (r *Runtime) StreamSearch(ctx context.Context, word string, send func(SiteResults) error) error {
	return r.Player.streamSearch(ctx, word, send)
}

// PosterHost returns the plugin host permitted for poster requests.
func (r *Runtime) PosterHost() string { return r.Player.pluginPosterHost() }

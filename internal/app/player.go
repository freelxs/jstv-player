package app

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/freelxs/jstv-player/internal/catvod"
	"github.com/freelxs/jstv-player/internal/externalplayer"
)

func newConfiguredPlayer(dir string, assets fs.FS) *Player {
	p := &Player{assets: assets, dir: dir, inited: map[string]bool{}, pendingTrust: map[string]sourcePin{}, proxy: &catvod.HeaderProxy{}}
	if err := p.proxy.Start(); err != nil {
		p.proxy = nil
	}
	_ = p.startEpisodeServer()
	if b, err := os.ReadFile(filepath.Join(dir, "settings.json")); err == nil {
		_ = json.Unmarshal(b, &p.settings)
	}
	_ = p.cleanupPersistentLogs(time.Now())
	legacy := p.settings.SourceURL != ""
	p.settings.migrate()
	if legacy {
		_ = p.writeSettings(p.settings)
	}
	p.startBridge(p.settings.BridgeTemplate)
	if p.settings.Player == "" {
		p.settings.Player = externalplayer.DefaultPlayer()
	}
	return p
}

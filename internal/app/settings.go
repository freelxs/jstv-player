package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/freelxs/jstv-player/internal/externalplayer"
)

// GetSettings returns the saved settings.
func (p *Player) GetSettings() Settings {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.settings
}

func (p *Player) writeSettings(s Settings) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(filepath.Join(p.dir, "settings.json"), b, 0o600)
}

// beginSettingsWrite runs after a settings snapshot is taken and before that
// snapshot is written. Tests hold this window open; production leaves it empty.
var beginSettingsWrite = func() {}

// SaveSettings stores the general settings (player, node, md5, bridge).
// Subscriptions are managed by the Subscription methods and kept as they are.
func (p *Player) SaveSettings(s Settings) error {
	if s.Player == "" {
		s.Player = externalplayer.DefaultPlayer()
	}
	if err := normalizeCustomPlayer(&s); err != nil {
		return err
	}
	p.settingsMu.Lock()
	p.logMu.Lock()
	p.mu.Lock()
	oldLAN := p.settings.AllowLAN
	s.Subscriptions, s.Active, s.SourceURL = p.settings.Subscriptions, p.settings.Active, ""
	p.mu.Unlock()
	beginSettingsWrite()
	if err := p.writeSettings(s); err != nil {
		p.logMu.Unlock()
		p.settingsMu.Unlock()
		return err
	}
	p.mu.Lock()
	p.settings = s
	p.mu.Unlock()
	p.logMu.Unlock()
	p.settingsMu.Unlock()
	p.startBridge(s.BridgeTemplate)
	// The plugin server binds its listen address at start; rebind on toggle.
	if oldLAN != s.AllowLAN && s.Active != "" {
		// Stop synchronously before the UI attempts a reload. In particular,
		// serialize with LoadSource so an in-flight start cannot publish a stale
		// all-interface listener after this stop has observed no host yet.
		p.loadMu.Lock()
		p.StopSource()
		p.loadMu.Unlock()
	}
	return nil
}

// editSubs applies f to a copy of the settings and saves the result.
func (p *Player) editSubs(f func(s *Settings) error) (Settings, error) {
	p.settingsMu.Lock()
	defer p.settingsMu.Unlock()
	p.mu.Lock()
	s := p.settings
	s.Subscriptions = append([]Subscription(nil), s.Subscriptions...)
	p.mu.Unlock()
	if err := f(&s); err != nil {
		return Settings{}, err
	}
	beginSettingsWrite()
	if err := p.writeSettings(s); err != nil {
		return Settings{}, err
	}
	p.mu.Lock()
	p.settings = s
	p.mu.Unlock()
	return s, nil
}

// AddSubscription adds a plugin address. The first one becomes active.
func (p *Player) AddSubscription(name, url string) (Settings, error) {
	return p.editSubs(func(s *Settings) error { _, err := s.add(name, url); return err })
}

// UpdateSubscription renames a subscription or changes its address.
func (p *Player) UpdateSubscription(id, name, url string) (Settings, error) {
	return p.editSubs(func(s *Settings) error { return s.update(id, name, url) })
}

// RemoveSubscription deletes a subscription and its downloaded files. When
// it was the active one, its plugin process is stopped.
func (p *Player) RemoveSubscription(id string) (Settings, error) {
	wasActive := p.GetSettings().Active == id
	s, err := p.editSubs(func(s *Settings) error { return s.remove(id) })
	if err != nil {
		return s, err
	}
	if wasActive {
		p.loadMu.Lock()
		p.StopSource()
		p.loadMu.Unlock()
	}
	if id != "" && !strings.ContainsAny(id, `/\.`) {
		_ = os.RemoveAll(filepath.Join(p.dir, "subs", id))
	}
	return s, nil
}

// SwitchSubscription makes id the active subscription and reloads it.
func (p *Player) SwitchSubscription(id string) (*LoadResult, error) {
	if _, err := p.editSubs(func(s *Settings) error {
		if s.find(id) < 0 {
			return errNoSub
		}
		s.Active = id
		return nil
	}); err != nil {
		return nil, err
	}
	return p.LoadSource()
}

// startBridge runs the play bridge only when the user configured a
// template; with an empty template it stays off.

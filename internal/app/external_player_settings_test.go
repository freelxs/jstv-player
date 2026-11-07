package app

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCustomPlayerSettingsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"player":"builtin","sourceUrl":"http://example.com/a/index.js"}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	p := newPlayer(dir)
	if got := p.GetSettings(); got.Player != "builtin" || got.CustomPlayer != "" || got.CustomArgs != "" {
		t.Fatalf("saved player changed or custom fields invented: %+v", got)
	}
	name := "player"
	if runtime.GOOS == "windows" {
		name = "player.exe"
	}
	script := filepath.Join(dir, name)
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := p.SaveSettings(Settings{Player: "custom", CustomPlayer: script, CustomArgs: "--fs {url}"}); err != nil {
		t.Fatal(err)
	}
	again := newPlayer(dir)
	got := again.GetSettings()
	if got.Player != "custom" || got.CustomPlayer != script || got.CustomArgs != "--fs {url}" {
		t.Fatalf("settings not persisted: %+v", got)
	}
	if err := p.SaveSettings(Settings{Player: "custom", CustomPlayer: filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("missing player saved")
	}
	if err := p.SaveSettings(Settings{Player: "custom", CustomPlayer: script, CustomArgs: `"broken`}); err == nil {
		t.Fatal("broken arguments saved")
	}
	if still := p.GetSettings(); still.Player != "custom" || still.CustomArgs != "--fs {url}" {
		t.Fatalf("failed save changed settings: %+v", still)
	}
	plain := filepath.Join(dir, "not-executable")
	if err := os.WriteFile(plain, []byte("no"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.SaveSettings(Settings{Player: "custom", CustomPlayer: plain}); err == nil {
		t.Fatal("non-executable saved")
	}
}

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSenPlayerOpenArgs(t *testing.T) {
	media := "http://127.0.0.1:9/p/a?x=1&y=2"
	args := senPlayerOpenArgs(media)
	if len(args) != 3 || args[0] != "-b" || args[1] != senPlayerBundleID {
		t.Fatal(args)
	}
	want := "senplayer://x-callback-url/play?url=http%3A%2F%2F127.0.0.1%3A9%2Fp%2Fa%3Fx%3D1%26y%3D2"
	if args[2] != want {
		t.Fatalf("scheme = %s", args[2])
	}
	spaced := senPlayerOpenArgs("http://127.0.0.1/a b")
	if strings.Contains(spaced[2], "+") || !strings.Contains(spaced[2], "%20") {
		t.Fatalf("space must be %%20 inside one argument: %v", spaced)
	}
	if len(spaced) != 3 {
		t.Fatal(spaced)
	}
}

func TestSenPlayerBundleCandidates(t *testing.T) {
	got := senPlayerBundleCandidates(`/Users/me`)
	if len(got) < 2 || got[0] != `/Applications/SenPlayer.app` || got[1] != filepath.Join(`/Users/me`, "Applications", "SenPlayer.app") {
		t.Fatal(got)
	}
	if strings.Contains(strings.Join(senPlayerBundleCandidates(""), "\n"), "Applications/SenPlayer.app\n") && senPlayerBundleCandidates("")[0] != `/Applications/SenPlayer.app` {
		t.Fatal(senPlayerBundleCandidates(""))
	}
}

func TestParseBundlePaths(t *testing.T) {
	got := parseBundlePaths("/Applications/SenPlayer.app\n\n/tmp/Other.app\n")
	if len(got) != 2 || got[0] != "/Applications/SenPlayer.app" || got[1] != "/tmp/Other.app" {
		t.Fatal(got)
	}
}

func TestChooseSenPlayer(t *testing.T) {
	root := t.TempDir()
	app := filepath.Join(root, "SenPlayer.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte("<plist></plist>"), 0o644); err != nil {
		t.Fatal(err)
	}
	empty := filepath.Join(root, "Empty.app")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := chooseSenPlayer([]string{empty, app}, nil); got != app {
		t.Fatalf("got %s", got)
	}
	if got := chooseSenPlayer(nil, func() []string { return []string{empty, app} }); got != app {
		t.Fatalf("spotlight got %s", got)
	}
	if got := chooseSenPlayer([]string{empty}, func() []string { return nil }); got != "" {
		t.Fatalf("incomplete bundle returned %s", got)
	}
}

func TestFindSenPlayerOnlyOnDarwin(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("installed apps on this Mac are not a unit fixture")
	}
	if got := findSenPlayer(); got != "" {
		t.Fatal(got)
	}
}

func TestPlayerUsesPlaylist(t *testing.T) {
	if playerUsesPlaylist("senplayer") || playerUsesPlaylist("system") {
		t.Fatal("senplayer and the system player do not take the episode playlist")
	}
	for _, player := range []string{"", "builtin", "mpv", "iina", "potplayer", "custom"} {
		if !playerUsesPlaylist(player) {
			t.Fatalf("%s should receive the playlist when continuous play is on", player)
		}
	}
}

func TestCustomPlayerArgv(t *testing.T) {
	media := "http://127.0.0.1:9/p/a b;rm -rf"
	args, err := customPlayerArgv("", media)
	if err != nil || len(args) != 1 || args[0] != media {
		t.Fatalf("%v %v", args, err)
	}
	args, err = customPlayerArgv("--fs --title={url}", media)
	if err != nil || len(args) != 2 || args[0] != "--fs" || args[1] != "--title="+media {
		t.Fatalf("%v %v", args, err)
	}
	args, err = customPlayerArgv(`--input={url} 'quoted arg'`, media)
	if err != nil || len(args) != 2 || args[0] != "--input="+media || args[1] != "quoted arg" {
		t.Fatalf("%v %v", args, err)
	}
	args, err = customPlayerArgv(`"{url}"`, media)
	if err != nil || len(args) != 1 || args[0] != media {
		t.Fatalf("quoted placeholder: %v %v", args, err)
	}
	args, err = customPlayerArgv("$(whoami) `id`", media)
	if err != nil || len(args) != 3 || args[0] != "$(whoami)" || args[1] != "`id`" || args[2] != media {
		t.Fatalf("shell text must stay literal argv: %v %v", args, err)
	}
	if _, err := customPlayerArgv(`--title "unterminated`, media); err == nil {
		t.Fatal("unclosed quote accepted")
	}
	if _, err := customPlayerArgv("", ""); err == nil {
		t.Fatal("empty media accepted")
	}
}

func TestValidateCustomPlayer(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "player")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateCustomPlayerOS(runtime.GOOS, script); err == nil {
		t.Fatal("file without execute permission accepted")
	}
	if err := os.Chmod(script, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateCustomPlayerOS("linux", script); err != nil {
		t.Fatal(err)
	}
	if err := validateCustomPlayerOS("windows", script); err == nil {
		t.Fatal("windows accepted a file that is not .exe")
	}
	exe := filepath.Join(dir, "player.exe")
	if err := os.WriteFile(exe, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateCustomPlayerOS("windows", exe); err != nil {
		t.Fatal(err)
	}
	if err := validateCustomPlayerOS("linux", "player"); err == nil {
		t.Fatal("relative path accepted")
	}
	if err := validateCustomPlayerOS("linux", filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing file accepted")
	}
	if err := validateCustomPlayerOS("linux", dir); err == nil {
		t.Fatal("directory accepted")
	}

	app := filepath.Join(dir, "Player.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := validateCustomPlayerOS("darwin", app); err == nil {
		t.Fatal("bundle without Info.plist accepted")
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte("<plist></plist>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := validateCustomPlayerOS("darwin", app); err != nil {
		t.Fatal(err)
	}
	if err := validateCustomPlayerOS("linux", app); err == nil {
		t.Fatal("linux accepted a macOS app bundle")
	}
}

func TestCustomLaunchPlan(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "player")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	media := "http://127.0.0.1:9/playlist/x.m3u"
	name, args, err := customLaunchPlan("linux", script, "--foo", media)
	if err != nil || name != script || len(args) != 2 || args[0] != "--foo" || args[1] != media {
		t.Fatalf("%s %v %v", name, args, err)
	}
	app := filepath.Join(dir, "My Player.app")
	if err := os.MkdirAll(filepath.Join(app, "Contents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(app, "Contents", "Info.plist"), []byte("<plist></plist>"), 0o644); err != nil {
		t.Fatal(err)
	}
	name, args, err = customLaunchPlan("darwin", app, "", media)
	if err != nil || name != "open" || len(args) != 3 || args[0] != "-a" || args[1] != app || args[2] != media {
		t.Fatalf("document open: %s %v %v", name, args, err)
	}
	name, args, err = customLaunchPlan("darwin", app, "--user-agent=x {url}", media)
	if err != nil || name != "open" || strings.Join(args, "\x00") != strings.Join([]string{"-a", app, "--args", "--user-agent=x", media}, "\x00") {
		t.Fatalf("argv open: %s %v %v", name, args, err)
	}
}

func TestCustomPlayerLaunchesStub(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "args.txt")
	script := filepath.Join(dir, "player")
	body := "#!/bin/sh\nprintf '%s\\n' \"$@\" > '" + strings.ReplaceAll(out, "'", `'"'"'`) + "'\n"
	if err := os.WriteFile(script, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	media := "http://127.0.0.1:9/p/a b;rm"
	name, args, err := customLaunch(script, "--title={url}", media)
	if err != nil {
		t.Fatal(err)
	}
	if name != script || len(args) != 1 || args[0] != "--title="+media {
		t.Fatalf("launch plan is not a direct argv: %s %v", name, args)
	}
	if err := runExternal(name, args); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var got string
	for {
		b, err := os.ReadFile(out)
		if err == nil {
			got = string(b)
			break
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got != "--title="+media+"\n" {
		t.Fatalf("stub received %q", got)
	}
}

func TestCustomPlayerDialogOptions(t *testing.T) {
	win := customPlayerDialogOptions("windows")
	if len(win.Filters) != 2 || win.Filters[0].Extensions[0] != "exe" || win.Filters[1].Extensions[0] != "*" {
		t.Fatalf("%+v", win.Filters)
	}
	if mac := customPlayerDialogOptions("darwin"); len(mac.Filters) != 0 || mac.TreatPackagesAsDirectories {
		t.Fatalf("macOS picker must be able to select .app bundles: %+v", mac)
	}
	if linux := customPlayerDialogOptions("linux"); len(linux.Filters) != 0 {
		t.Fatalf("linux picker must show extensionless executables: %+v", linux.Filters)
	}
}

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
	script := filepath.Join(dir, "player")
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

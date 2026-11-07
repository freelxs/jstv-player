package externalplayer

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// IinaArgs: options before the URL, and --no-stdin. Without it iina-cli
// probes stdin, which is not a terminal when launched from an app, adds
// --stdin, and IINA shows "Cannot open file or stream!".
func IinaArgs(u string) []string {
	// prefetch-playlist: open the next playlist entry before this one
	// ends, so 连播 switches without a pause.
	// IINA's CLI forwards single-dash key/value pairs to NSArgumentDomain.
	// Keep the override scoped to this launch and avoid an empty mpv value:
	// IINA's CLI parser drops empty values after '='.
	return []string{"--no-stdin", "-disableOSDFileStartMsg", "YES", "--mpv-demuxer-lavf-o=extension_picky=0", "--mpv-prefetch-playlist=yes", u}
}

func MpvArgs(u, title string) []string {
	// extension_picky=0: accept HLS segments disguised as .jpg/.png.
	// No --title for playlists: mpv then shows each entry's #EXTINF title.
	// prefetch-playlist: open the next 连播 entry before this one ends.
	// osd-playing-msg / term-playing-msg empty: never print the entry URL.
	args := []string{"--force-window=immediate", "--demuxer-lavf-o=extension_picky=0", "--prefetch-playlist=yes", "--osd-playing-msg=", "--term-playing-msg="}
	if title != "" {
		args = append(args, "--title="+title)
	}
	if runtime.GOOS == "darwin" {
		args = append(args, "--macos-app-activation-policy=regular")
	}
	return append(args, "--", u)
}

// DefaultPlayer prefers an installed external player on Windows and Linux.
// The built-in player can play HLS there too (hls.js, when the web view has
// Media Source Extensions and codecs), but mpv or PotPlayer does not depend
// on that. macOS keeps the built-in player, which can play HLS natively.
// SenPlayer and a custom player are opt-in. This runs only when no player
// has been saved.
func DefaultPlayer() string {
	return PickDefaultPlayer(runtime.GOOS, FindPotPlayer() != "", FindExe(MpvCandidates()...) != "")
}

func PickDefaultPlayer(goos string, hasPot, hasMPV bool) string {
	switch {
	case goos == "darwin":
		return "builtin"
	case goos == "windows" && hasPot:
		return "potplayer"
	case hasMPV:
		return "mpv"
	}
	return "builtin"
}

// MpvCandidates lists where mpv usually lives on each platform; PATH is
// checked first. GUI apps on macOS start with a minimal PATH.
func MpvCandidates() []string {
	home, _ := os.UserHomeDir()
	switch runtime.GOOS {
	case "darwin":
		return []string{"mpv", "/opt/homebrew/bin/mpv", "/usr/local/bin/mpv", "/Applications/mpv.app/Contents/MacOS/mpv"}
	case "windows":
		c := []string{"mpv.exe", "mpv"}
		for _, env := range []string{"ProgramFiles", "ProgramFiles(x86)", "LOCALAPPDATA"} {
			if d := os.Getenv(env); d != "" {
				c = append(c, filepath.Join(d, "mpv", "mpv.exe"), filepath.Join(d, "Programs", "mpv", "mpv.exe"))
			}
		}
		if home != "" {
			c = append(c, filepath.Join(home, "scoop", "apps", "mpv", "current", "mpv.exe"))
		}
		if d := os.Getenv("ChocolateyInstall"); d != "" {
			c = append(c, filepath.Join(d, "bin", "mpv.exe"))
		}
		return c
	default:
		return []string{"mpv", "/usr/bin/mpv", "/usr/local/bin/mpv", "/snap/bin/mpv", "/var/lib/flatpak/exports/bin/io.mpv.Mpv"}
	}
}

func MpvHint() string {
	switch runtime.GOOS {
	case "darwin":
		return "未找到 mpv（brew install mpv）"
	case "windows":
		return "未找到 mpv：请安装 mpv（如 scoop install mpv）并加入 PATH"
	default:
		return "未找到 mpv：请安装 mpv（如 sudo apt install mpv）"
	}
}

func FindExe(names ...string) string {
	for _, n := range names {
		if filepath.IsAbs(n) {
			if _, err := os.Stat(n); err == nil {
				return n
			}
		} else if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

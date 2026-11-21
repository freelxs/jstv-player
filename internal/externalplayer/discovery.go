package externalplayer

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
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
	args = append(args, mpvOSDFontArgs(runtime.GOOS)...)
	if title != "" {
		args = append(args, "--title="+title)
	}
	if runtime.GOOS == "darwin" {
		args = append(args, "--macos-app-activation-policy=regular")
	}
	return append(args, "--", u)
}

// mpvOSDFontArgs pins an installed font with CJK glyphs. On macOS mpv's
// default sans-serif resolves to Helvetica, while libass's CoreText fallback
// points at the private PingFangUI.ttc that it cannot open. Playlist titles
// and the OSC title therefore render Chinese, Japanese, and Korean as boxes.
// Windows and Linux are checked for an installed CJK font before overriding
// mpv's default.
func mpvOSDFontArgs(goos string) []string {
	var font string
	switch goos {
	case "darwin":
		font = "Hiragino Sans GB"
	case "windows":
		font = windowsCJKFont(os.Getenv("WINDIR"))
	case "linux":
		font = fontconfigCJKFont()
	}
	if font == "" {
		return nil
	}
	return []string{"--osd-font=" + font}
}

// windowsCJKFont checks the font files shipped with Windows. The first
// available Chinese font wins; Japanese and Korean fallbacks cover systems
// where the Chinese supplemental fonts are not installed.
func windowsCJKFont(windir string) string {
	if windir == "" {
		windir = `C:\Windows`
	}
	const yaHei = "\u5fae\u8f6f\u96c5\u9ed1"
	fonts := []struct {
		file, family string
	}{
		{"msyh.ttc", yaHei},
		{"msyhbd.ttc", yaHei},
		{"msyhl.ttc", yaHei},
		{"simhei.ttf", "SimHei"},
		{"simsun.ttc", "SimSun"},
		{"Deng.ttf", "DengXian"},
		{"msjh.ttc", "Microsoft JhengHei"},
		{"YuGothM.ttc", "Yu Gothic"},
		{"meiryo.ttc", "Meiryo"},
		{"malgun.ttf", "Malgun Gothic"},
	}
	dir := filepath.Join(windir, "Fonts")
	for _, font := range fonts {
		if _, err := os.Stat(filepath.Join(dir, font.file)); err == nil {
			return font.family
		}
	}
	return ""
}

// fontconfigCJKFont asks fontconfig for installed CJK families and prefers a
// sans-serif family before falling back to whatever it lists first.
func fontconfigCJKFont() string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "fc-list", ":lang=zh", "family").Output()
	if err != nil {
		return ""
	}
	return pickFontconfigCJKFont(string(out))
}

func pickFontconfigCJKFont(out string) string {
	var first string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		family := strings.TrimSpace(strings.SplitN(line, ",", 2)[0])
		if family == "" {
			continue
		}
		lower := strings.ToLower(family)
		for _, hint := range []string{"noto sans cjk", "noto sans sc", "source han sans", "wenquanyi", "droid sans fallback", "ar pl sans"} {
			if strings.Contains(lower, hint) {
				return family
			}
		}
		if first == "" {
			first = family
		}
	}
	return first
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

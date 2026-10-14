package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// potPlayerExes are the executable names PotPlayer installs, 64-bit first.
var potPlayerExes = []string{"PotPlayerMini64.exe", "PotPlayerMini.exe"}

// potPlayerCandidates lists the usual install folders. env looks up
// environment variables (os.Getenv in the app, a map in tests).
func potPlayerCandidates(env func(string) string) []string {
	var c []string
	for _, v := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)", "LOCALAPPDATA"} {
		d := env(v)
		if d == "" {
			continue
		}
		for _, sub := range []string{filepath.Join("DAUM", "PotPlayer"), "PotPlayer", filepath.Join("Programs", "PotPlayer")} {
			for _, exe := range potPlayerExes {
				c = append(c, filepath.Join(d, sub, exe))
			}
		}
	}
	return c
}

// regQueries are the `reg query` lookups that may name PotPlayer: the
// App Paths entries (default value) and the player's own ProgramPath.
var regQueries = [][]string{
	{`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\PotPlayerMini64.exe`, "/ve"},
	{`HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\PotPlayerMini64.exe`, "/ve"},
	{`HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\PotPlayerMini.exe`, "/ve"},
	{`HKCU\SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\PotPlayerMini.exe`, "/ve"},
	{`HKCU\SOFTWARE\DAUM\PotPlayer64`, "/v", "ProgramPath"},
	{`HKCU\SOFTWARE\DAUM\PotPlayer`, "/v", "ProgramPath"},
}

// parseRegValue extracts the string data from `reg query` output, e.g.
//
//	(Default)    REG_SZ    C:\Program Files\DAUM\PotPlayer\PotPlayerMini64.exe
func parseRegValue(out string) string {
	for _, line := range strings.Split(out, "\n") {
		for _, typ := range []string{"REG_SZ", "REG_EXPAND_SZ"} {
			if i := strings.Index(line, typ); i >= 0 {
				v := strings.Trim(strings.TrimSpace(line[i+len(typ):]), `"`)
				if typ == "REG_EXPAND_SZ" {
					v = os.ExpandEnv(strings.NewReplacer("%", "$").Replace(v))
				}
				if v != "" {
					return v
				}
			}
		}
	}
	return ""
}

func regLookup() []string {
	var out []string
	for _, q := range regQueries {
		b, err := hiddenCommand("reg", append([]string{"query"}, q...)...).Output()
		if err != nil {
			continue
		}
		if v := parseRegValue(string(b)); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// findPotPlayer returns the PotPlayer executable, or "" (always "" outside
// Windows). Order: PATH, registry, usual install folders.
func findPotPlayer() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	names := append([]string{}, potPlayerExes...)
	names = append(names, regLookup()...)
	names = append(names, potPlayerCandidates(os.Getenv)...)
	return findExe(names...)
}

// potPlayerArgs builds the arguments: the URL, then /new so it never
// replaces what an already open window plays, and /title.
func potPlayerArgs(u, title string) []string {
	args := []string{u, "/new"}
	if t := cleanTitle(title); t != "" {
		args = append(args, "/title="+t)
	}
	return args
}

func cleanTitle(t string) string {
	t = strings.Map(func(r rune) rune {
		if r == '"' || r < 0x20 {
			return ' '
		}
		return r
	}, t)
	return strings.TrimSpace(t)
}

// potPlayerCmdLine is the raw Windows command line. PotPlayer reads its
// switches itself and expects /title="a b", which Go's default argument
// quoting would turn into "/title=a b".
func potPlayerCmdLine(exe, u, title string) string {
	s := `"` + exe + `" "` + strings.ReplaceAll(u, `"`, "%22") + `" /new`
	if t := cleanTitle(title); t != "" {
		s += ` /title="` + t + `"`
	}
	return s
}

func potPlayerCommand(exe, u, title string) *exec.Cmd {
	cmd := exec.Command(exe, potPlayerArgs(u, title)...)
	setRawCmdLine(cmd, potPlayerCmdLine(exe, u, title))
	return cmd
}

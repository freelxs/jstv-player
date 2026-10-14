package main

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestPotPlayerCandidates(t *testing.T) {
	env := map[string]string{"ProgramFiles": `C:\PF`, "ProgramFiles(x86)": `C:\PF86`}
	c := potPlayerCandidates(func(k string) string { return env[k] })
	want := []string{
		filepath.Join(`C:\PF`, "DAUM", "PotPlayer", "PotPlayerMini64.exe"),
		filepath.Join(`C:\PF86`, "DAUM", "PotPlayer", "PotPlayerMini.exe"),
		filepath.Join(`C:\PF`, "PotPlayer", "PotPlayerMini64.exe"),
	}
	for _, w := range want {
		if !slices.Contains(c, w) {
			t.Errorf("missing %s in %v", w, c)
		}
	}
	if c[0] != filepath.Join(`C:\PF`, "DAUM", "PotPlayer", "PotPlayerMini64.exe") {
		t.Errorf("64-bit default folder should come first: %s", c[0])
	}
}

func TestParseRegValue(t *testing.T) {
	out := "\r\nHKEY_LOCAL_MACHINE\\SOFTWARE\\Microsoft\\Windows\\CurrentVersion\\App Paths\\PotPlayerMini64.exe\r\n    (Default)    REG_SZ    C:\\Program Files\\DAUM\\PotPlayer\\PotPlayerMini64.exe\r\n\r\n"
	if got := parseRegValue(out); got != `C:\Program Files\DAUM\PotPlayer\PotPlayerMini64.exe` {
		t.Fatalf("%q", got)
	}
	if got := parseRegValue("    ProgramPath    REG_SZ    \"D:\\Apps\\PotPlayer\\PotPlayerMini64.exe\"\r\n"); got != `D:\Apps\PotPlayer\PotPlayerMini64.exe` {
		t.Fatalf("%q", got)
	}
	if parseRegValue("ERROR: The system was unable to find the specified registry key or value.") != "" {
		t.Fatal("error text parsed as a value")
	}
}

func TestPotPlayerArgs(t *testing.T) {
	u := "http://127.0.0.1:5000/p?u=http%3A%2F%2Fexample.com%2Fa.m3u8"
	a := potPlayerArgs(u, `Ep "1"`)
	if a[0] != u || !slices.Contains(a, "/new") || a[len(a)-1] != "/title=Ep  1" {
		t.Fatal(a)
	}
	if a := potPlayerArgs(u, ""); len(a) != 2 {
		t.Fatal(a)
	}
	line := potPlayerCmdLine(`C:\P F\PotPlayerMini64.exe`, u, "A B - 第1集")
	if line != `"C:\P F\PotPlayerMini64.exe" "`+u+`" /new /title="A B - 第1集"` {
		t.Fatal(line)
	}
	if l := potPlayerCmdLine(`x.exe`, `http://example.com/a"b`, "t\"x"); strings.Count(l, `"`) != 6 {
		t.Fatal("quotes leaked into the command line:", l)
	}
}

func TestPickDefaultPlayer(t *testing.T) {
	cases := []struct {
		goos     string
		pot, mpv bool
		want     string
	}{
		{"darwin", false, true, "builtin"},
		{"windows", true, true, "potplayer"},
		{"windows", false, true, "mpv"},
		{"windows", false, false, "builtin"},
		{"linux", true, true, "mpv"},
		{"linux", false, false, "builtin"},
	}
	for _, c := range cases {
		if got := pickDefaultPlayer(c.goos, c.pot, c.mpv); got != c.want {
			t.Errorf("%+v: got %s", c, got)
		}
	}
}

func TestFindPotPlayerOnlyOnWindows(t *testing.T) {
	if got := findPotPlayer(); got != "" && !strings.HasSuffix(strings.ToLower(got), ".exe") {
		t.Fatal(got)
	}
}

package main

import (
	"slices"
	"strings"
	"testing"
)

func TestIINAArgs(t *testing.T) {
	a := iinaArgs("http://example.com/a.m3u8")
	if a[0] != "--no-stdin" || !slices.Contains(a, "--mpv-demuxer-lavf-o=extension_picky=0") || a[len(a)-1] != "http://example.com/a.m3u8" {
		t.Fatal(a)
	}
	key := slices.Index(a, "-disableOSDFileStartMsg")
	if key < 0 || key+1 >= len(a) || a[key+1] != "YES" {
		t.Fatalf("IINA NSArgumentDomain key/value must be adjacent: %v", a)
	}
	for _, arg := range a {
		if strings.HasPrefix(arg, "--mpv-") && strings.HasSuffix(arg, "=") {
			t.Fatalf("empty mpv OSD argument is not valid for IINA CLI: %v", a)
		}
	}
	if slices.Contains(a, "--mpv-osd-playing-msg") {
		t.Fatalf("invalid IINA mpv OSD argument present: %v", a)
	}
}

func TestMPVArgs(t *testing.T) {
	a := mpvArgs("http://example.com/a.m3u8", "t")
	if i := slices.Index(a, "--"); i < 0 || a[i+1] != "http://example.com/a.m3u8" || i+2 != len(a) {
		t.Fatal(a)
	}
	for _, s := range a {
		if len(s) > 2 && s[:2] == "--" && slices.Contains([]string{"--http-header-fields"}, s) {
			t.Fatal("header list passed to mpv")
		}
	}
}

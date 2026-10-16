package main

import (
	"slices"
	"testing"
)

func TestIINAArgs(t *testing.T) {
	a := iinaArgs("http://example.com/a.m3u8")
	if a[0] != "--no-stdin" || !slices.Contains(a, "-default") || !slices.Contains(a, "disableOSDFileStartMsg=YES") || !slices.Contains(a, "--mpv-demuxer-lavf-o=extension_picky=0") || !slices.Contains(a, "--mpv-osd-playing-msg=") || a[len(a)-1] != "http://example.com/a.m3u8" {
		t.Fatal(a)
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

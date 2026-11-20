package netx

import "testing"

func TestASCIIURLPunycodesHost(t *testing.T) {
	got, err := ASCIIURL("http://bücher.example:8080/a%20b?q=1#f")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://xn--bcher-kva.example:8080/a%20b?q=1#f" {
		t.Fatal(got)
	}
	plain, err := ASCIIURL("https://cdn.example/live.m3u8")
	if err != nil || plain != "https://cdn.example/live.m3u8" {
		t.Fatalf("%q %v", plain, err)
	}
	rel, err := ASCIIURL("../list.m3u")
	if err != nil || rel != "../list.m3u" {
		t.Fatalf("%q %v", rel, err)
	}
}

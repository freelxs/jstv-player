package catvod

import "testing"

func TestListenHost(t *testing.T) {
	if got := listenHost(false); got != "127.0.0.1" {
		t.Fatalf("default listen host = %q, want loopback", got)
	}
	if got := listenHost(true); got != "0.0.0.0" {
		t.Fatalf("LAN listen host = %q, want all interfaces", got)
	}
}

func TestConfigPageURLRespectsLAN(t *testing.T) {
	ip := firstNonLoopbackIPv4(t)
	base := "http://127.0.0.1:50512"
	lanInput := "http://" + ip + ":50512/website/"

	// Loopback mode: rewrite the machine's LAN address to loopback.
	if got, ok := ConfigPageURL(lanInput, base, false); !ok || got != base+"/website/index.html" {
		t.Fatalf("loopback mode = %q, %v", got, ok)
	}
	// LAN mode: keep the machine's LAN address (other devices can reach it).
	want := "http://" + ip + ":50512/website/index.html"
	if got, ok := ConfigPageURL(lanInput, base, true); !ok || got != want {
		t.Fatalf("LAN mode = %q, %v", got, ok)
	}
	// LAN mode also upgrades a loopback label to the LAN address.
	if got, ok := ConfigPageURL(base+"/website/", base, true); !ok || got != want {
		t.Fatalf("loopback->LAN = %q, %v", got, ok)
	}
	// A different local service port is never claimed, in either mode.
	if got, ok := ConfigPageURL("http://"+ip+":50513/website/", base, true); ok || got != "http://"+ip+":50513/website/" {
		t.Fatalf("other port claimed: %q, %v", got, ok)
	}
}

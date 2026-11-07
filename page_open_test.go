package main

import (
	"strings"
	"testing"

	"github.com/egoist/mygo"
)

func TestInAppNavigationKeepsLoopbackConfigurationPages(t *testing.T) {
	for _, raw := range []string{
		"http://127.0.0.1:9/website/index.html?mode=edit#qr",
		"http://localhost:9/website/",
		"http://[::1]:9/website/index.html",
	} {
		if !inAppNavigationURL(raw) {
			t.Fatalf("expected in-app navigation for %s", raw)
		}
	}
	if inAppNavigationURL("https://example.com/website/index.html") {
		t.Fatal("a public page must stay outside the app window")
	}
	if externalNavigationURL("http://127.0.0.1:9/website/index.html") {
		t.Fatal("loopback configuration page was treated as an external browser link")
	}
	if !externalNavigationURL("https://example.com/watch/1") {
		t.Fatal("public http page should still be openable outside the app")
	}
}

func TestConfigPageURLUsesTheLoadedSource(t *testing.T) {
	got, err := configPageURL("http://127.0.0.1:9/spider/demo")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://127.0.0.1:9/website/index.html" {
		t.Fatalf("config page = %s", got)
	}
	if _, err := configPageURL("not a url"); err == nil {
		t.Fatal("accepted a source that is not an http address")
	}
}

func TestConfigPageWindowOpenKeepsTheExistingAddressRule(t *testing.T) {
	for _, userInitiated := range []bool{true, false} {
		inApp, external := configPageWindowOptions("配置", mygo.WindowOpenRequest{
			URL:           "http://127.0.0.1:9/website/index.html?mode=edit#qr",
			UserInitiated: userInitiated,
		})
		if external || inApp == nil || inApp.Title != "配置" || inApp.Width != 1100 || inApp.Height != 700 {
			t.Fatalf("userInitiated=%v in-app=%+v external=%v", userInitiated, inApp, external)
		}
		inApp, external = configPageWindowOptions("配置", mygo.WindowOpenRequest{
			URL:           "https://example.com/watch/1",
			UserInitiated: userInitiated,
		})
		if inApp != nil || !external {
			t.Fatalf("userInitiated=%v public page in-app=%+v external=%v", userInitiated, inApp, external)
		}
		inApp, external = configPageWindowOptions("配置", mygo.WindowOpenRequest{
			URL:           "file:///tmp/local.html",
			UserInitiated: userInitiated,
		})
		if inApp != nil || external {
			t.Fatalf("userInitiated=%v non-http page in-app=%+v external=%v", userInitiated, inApp, external)
		}
	}
}

func TestConfigPageFitLetsTheCodeScrollIntoView(t *testing.T) {
	for _, part := range []string{"overflow:auto", "object-fit:contain", "max-width:100%"} {
		if !strings.Contains(configPageFitJS, part) {
			t.Fatalf("fit script missing %s: %s", part, configPageFitJS)
		}
	}
	for _, part := range []string{"overflow:hidden", "object-fit:cover"} {
		if strings.Contains(configPageFitJS, part) {
			t.Fatalf("fit script still crops with %s", part)
		}
	}
}

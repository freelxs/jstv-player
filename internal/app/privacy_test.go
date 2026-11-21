package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/egoist/mygo"
	"github.com/freelxs/jstv-player/internal/catvod"
)

func TestDefaultContentBlockerRulesWebKitCompliance(t *testing.T) {
	var rules []struct {
		Trigger struct {
			URLFilter    string   `json:"url-filter"`
			UnlessDomain []string `json:"unless-domain"`
			LoadType     []string `json:"load-type"`
			ResourceType []string `json:"resource-type"`
		} `json:"trigger"`
		Action struct {
			Type     string `json:"type"`
			Selector string `json:"selector"`
		} `json:"action"`
	}
	if err := json.Unmarshal([]byte(defaultContentBlockerRulesJSON), &rules); err != nil {
		t.Fatalf("invalid content blocker JSON: %v", err)
	}
	if len(rules) < 20 {
		t.Fatalf("expected substantial rule list, got %d", len(rules))
	}
	hasCSSRule := false
	for idx, rule := range rules {
		if rule.Trigger.URLFilter == "" {
			t.Fatalf("rule %d missing url-filter", idx)
		}
		for _, r := range rule.Trigger.URLFilter {
			if r > 127 {
				t.Fatalf("rule %d url-filter contains non-ASCII char %q", idx, r)
			}
		}
		for _, forbidden := range []string{"|", "{", "}", `\d`, `\w`, `\s`} {
			if strings.Contains(rule.Trigger.URLFilter, forbidden) {
				t.Fatalf("rule %d url-filter %q uses unsupported WebKit regex token %q", idx, rule.Trigger.URLFilter, forbidden)
			}
		}
		switch rule.Action.Type {
		case "block", "block-cookies", "make-https", "ignore-previous-rules":
		case "css-display-none":
			hasCSSRule = true
			if strings.TrimSpace(rule.Action.Selector) == "" {
				t.Fatalf("rule %d css-display-none missing selector", idx)
			}
			excluded := strings.Join(rule.Trigger.UnlessDomain, ",")
			if !strings.Contains(excluded, "localhost") || !strings.Contains(excluded, "127.0.0.1") {
				t.Fatalf("rule %d css-display-none must exclude loopback hosts, got %v", idx, rule.Trigger.UnlessDomain)
			}
		default:
			t.Fatalf("rule %d has unknown action type %q", idx, rule.Action.Type)
		}
	}
	if !hasCSSRule {
		t.Fatal("expected at least one css-display-none rule")
	}
}

func TestPrivacyRulesHookToggleAndSniffContext(t *testing.T) {
	p := newPlayer(t.TempDir())
	defer p.StopSource()

	sentinel := &mygo.ContentRules{}
	compiledID := ""
	p.desktop.CompileContentRules = func(id, rules string) (*mygo.ContentRules, error) {
		compiledID = id
		if !strings.Contains(rules, "google-analytics") {
			t.Fatalf("unexpected rules payload")
		}
		return sentinel, nil
	}
	p.initPrivacyRules()
	if compiledID != contentRulesID {
		t.Fatalf("compiledID = %q", compiledID)
	}
	if got := p.activeContentRules(); len(got) != 1 || got[0] != sentinel {
		t.Fatalf("activeContentRules when enabled = %+v", got)
	}

	prev := sniffPage
	var captured []*mygo.ContentRules
	sniffPage = func(ctx context.Context, page string, headers map[string]string) (sniffed, error) {
		captured = contentRulesFromContext(ctx)
		return sniffed{URL: "https://cdn.example/video.m3u8"}, nil
	}
	defer func() { sniffPage = prev }()

	if _, err := p.sniffInto(catvod.Stream{URL: "https://example.com/play", Parse: true}); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 1 || captured[0] != sentinel {
		t.Fatalf("sniffInto did not propagate content rules: %+v", captured)
	}

	s := p.GetSettings()
	s.DisableAdBlock = true
	if err := p.SaveSettings(s); err != nil {
		t.Fatal(err)
	}
	if got := p.activeContentRules(); got != nil {
		t.Fatalf("activeContentRules should be nil when disabled, got %+v", got)
	}
	if _, err := p.sniffInto(catvod.Stream{URL: "https://example.com/play", Parse: true}); err != nil {
		t.Fatal(err)
	}
	if len(captured) != 0 {
		t.Fatalf("sniffInto propagated content rules while disabled: %+v", captured)
	}
}

func TestThirdPartyPageWindowOptions(t *testing.T) {
	reqUser := mygo.WindowOpenRequest{URL: "https://ads.example/pop", UserInitiated: true}
	reqScript := mygo.WindowOpenRequest{URL: "https://ads.example/pop", UserInitiated: false}
	if inApp, ext := thirdPartyPageWindowOptions(true, reqUser); inApp != nil || ext {
		t.Fatalf("adBlock enabled must block popups, got inApp=%+v ext=%v", inApp, ext)
	}
	if inApp, ext := thirdPartyPageWindowOptions(false, reqScript); inApp != nil || ext {
		t.Fatalf("script-initiated popup must stay blocked, got inApp=%+v ext=%v", inApp, ext)
	}
	if inApp, ext := thirdPartyPageWindowOptions(false, reqUser); inApp != nil || !ext {
		t.Fatalf("user-initiated link with adBlock disabled should open externally, got inApp=%+v ext=%v", inApp, ext)
	}
}

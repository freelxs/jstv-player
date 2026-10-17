package main

import (
	"strings"
	"testing"
)

func TestSanitizeLogLineRedactsCommonSecrets(t *testing.T) {
	line := "fetch https://user:pass@example.test/a?token=abc&lang=zh headers=map[Cookie:session-secret Authorization:Bearer secret]"
	out := sanitizeLogLine(line)
	for _, secret := range []string{"user:pass", "token=abc", "session-secret", "Bearer secret"} {
		if strings.Contains(out, secret) {
			t.Fatalf("secret %q leaked in %q", secret, out)
		}
	}
	if !strings.Contains(out, "REDACTED") {
		t.Fatalf("expected redaction: %s", out)
	}
}

func TestSanitizeLogLineBoundsUnknownPluginOutput(t *testing.T) {
	out := sanitizeLogLine(strings.Repeat("x", 9000))
	if len(out) > 8220 || !strings.Contains(out, "截断") {
		t.Fatalf("long log not bounded: %d", len(out))
	}
}

func TestNodePermissionFlagsOnlyReportsAdvertisedFlags(t *testing.T) {
	got := nodePermissionFlags("--experimental-permission  --permission")
	if len(got) != 2 || got[0] != "--permission" || got[1] != "--experimental-permission" {
		t.Fatal(got)
	}
	if len(nodePermissionFlags("Node.js 18 help")) != 0 {
		t.Fatal("unexpected flag")
	}
}

package catvod

import "testing"

func TestNodeEnvironmentDoesNotInheritSecretsByDefault(t *testing.T) {
	t.Setenv("JSTV_TEST_SECRET", "do-not-pass")
	t.Setenv("HTTPS_PROXY", "http://user:secret@proxy.invalid")
	for _, entry := range nodeEnvironment(false) {
		if entry == "JSTV_TEST_SECRET=do-not-pass" || entry == "HTTPS_PROXY=http://user:secret@proxy.invalid" {
			t.Fatalf("secret leaked into default environment: %s", entry)
		}
	}
	foundSecret, foundProxy := false, false
	for _, entry := range nodeEnvironment(true) {
		if entry == "JSTV_TEST_SECRET=do-not-pass" {
			foundSecret = true
		}
		if entry == "HTTPS_PROXY=http://user:secret@proxy.invalid" {
			foundProxy = true
		}
	}
	if !foundSecret || !foundProxy {
		t.Fatal("explicit compatibility mode did not inherit full environment")
	}
}

func TestProxyOnlyInheritanceDoesNotIncludeOtherSecrets(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://user:secret@proxy.invalid")
	t.Setenv("http_proxy", "http://localhost:7890")
	t.Setenv("JSTV_TEST_SECRET", "do-not-pass")
	seen := map[string]bool{}
	for _, entry := range nodeEnvironment(false, true) {
		seen[entry] = true
	}
	if !seen["HTTPS_PROXY=http://user:secret@proxy.invalid"] || !seen["http_proxy=http://localhost:7890"] || seen["JSTV_TEST_SECRET=do-not-pass"] {
		t.Fatal("proxy-only environment was not scoped")
	}
}

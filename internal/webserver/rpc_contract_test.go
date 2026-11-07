package webserver

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestFrontendLiteralCallsHaveWebAdapters(t *testing.T) {
	methods := playerMethods()
	exempt := map[string]bool{"SearchAll": true} // explicit NDJSON stream adapter
	call := regexp.MustCompile(`(?:^|[^.A-Za-z0-9_])(?:call|rpc)\(['"](?:Player\.)?([A-Za-z0-9_]+)['"]`)
	for _, directory := range []string{"web", "webassets"} {
		entries, err := os.ReadDir(filepath.Join("../..", directory))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if !strings.HasSuffix(entry.Name(), ".js") || entry.Name() == "hls.min.js" {
				continue
			}
			data, err := os.ReadFile(filepath.Join("../..", directory, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			for _, match := range call.FindAllStringSubmatch(string(data), -1) {
				if _, ok := methods[match[1]]; !ok && !exempt[match[1]] {
					t.Errorf("%s/%s invokes %s without a web adapter", directory, entry.Name(), match[1])
				}
			}
		}
	}
}

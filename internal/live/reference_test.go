package live

import (
	"context"
	"os"
	"testing"
	"time"
)

// Opt-in network smoke: the default test suite never depends on a public CDN.
func TestPublicReferenceSubscription(t *testing.T) {
	address := os.Getenv("JSTV_LIVE_REFERENCE_URL")
	if address == "" {
		t.Skip("set JSTV_LIVE_REFERENCE_URL for an explicit public feed smoke test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	catalog, err := (Loader{}).Load(ctx, address)
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string]bool{}
	for _, channel := range catalog.Channels {
		groups[channel.Group] = true
	}
	t.Logf("parsed %d channels in %d groups; %d optional live sources unavailable", len(catalog.Channels), len(groups), len(catalog.Warnings))
}

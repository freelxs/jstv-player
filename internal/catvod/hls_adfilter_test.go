package catvod

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFilterHLSAdsSkipsMasterAndLiveDiscontinuity(t *testing.T) {
	master := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=800000\nhigh.m3u8\n#EXT-X-STREAM-INF:BANDWIDTH=400000\nlow.m3u8\n"
	if got := string(FilterHLSAds([]byte(master))); got != master {
		t.Fatalf("master playlist modified:\n%s", got)
	}

	live := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-TARGETDURATION:10",
		"#EXTINF:10.0,",
		"main/001.ts",
		"#EXTINF:10.0,",
		"main/002.ts",
		"#EXTINF:10.0,",
		"main/003.ts",
		"#EXT-X-DISCONTINUITY",
		"#EXTINF:6.0,",
		"other/001.ts",
	}, "\n") + "\n"
	if got := string(FilterHLSAds([]byte(live))); got != live {
		t.Fatalf("live playlist without ENDLIST should not strip discontinuity:\n%s", got)
	}
}

func TestFilterHLSAdsRemovesExplicitAdURIsWithoutFalsePositives(t *testing.T) {
	playlist := strings.Join([]string{
		"#EXTM3U",
		"#EXT-X-TARGETDURATION:10",
		"#EXTINF:10.0,",
		"road001.ts",
		"#EXTINF:10.0,",
		"download_002.ts",
		"#EXTINF:5.0,",
		"https://cdn.example/adjump/clip01.ts",
		"#EXTINF:5.0,",
		"https://cdn.example/ads/promo.ts",
		"#EXTINF:5.0,",
		"ad001.ts",
		"#EXTINF:10.0,",
		"shadow_003.ts",
		"#EXT-X-ENDLIST",
	}, "\n") + "\n"

	got := string(FilterHLSAds([]byte(playlist)))
	for _, keep := range []string{"road001.ts", "download_002.ts", "shadow_003.ts"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("legitimate segment %q stripped:\n%s", keep, got)
		}
	}
	for _, drop := range []string{"adjump/clip01.ts", "ads/promo.ts", "ad001.ts"} {
		if strings.Contains(got, "\n"+drop+"\n") || strings.Contains(got, "/"+drop+"\n") {
			t.Fatalf("explicit ad segment %q kept:\n%s", drop, got)
		}
	}
}

func TestFilterHLSAdsStripsVODDiscontinuityForeignAndUnencryptedAdBlock(t *testing.T) {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:10\n")
	b.WriteString("#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n")
	for idx := 0; idx < 10; idx++ {
		b.WriteString("#EXTINF:10.0,\nmain/seg0")
		b.WriteByte(byte('0' + idx))
		b.WriteString(".ts\n")
	}
	b.WriteString("#EXT-X-DISCONTINUITY\n")
	b.WriteString("#EXT-X-KEY:METHOD=NONE\n")
	b.WriteString("#EXTINF:6.0,\npromo/spot1.ts\n")
	b.WriteString("#EXTINF:6.0,\npromo/spot2.ts\n")
	b.WriteString("#EXT-X-DISCONTINUITY\n")
	b.WriteString("#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"\n")
	for idx := 0; idx < 10; idx++ {
		b.WriteString("#EXTINF:10.0,\nmain/seg1")
		b.WriteByte(byte('0' + idx))
		b.WriteString(".ts\n")
	}
	b.WriteString("#EXT-X-ENDLIST\n")

	got := string(FilterHLSAds([]byte(b.String())))
	if strings.Contains(got, "promo/spot1.ts") || strings.Contains(got, "promo/spot2.ts") {
		t.Fatalf("discontinuity ad block was not removed:\n%s", got)
	}
	if strings.Contains(got, "METHOD=NONE") {
		t.Fatalf("stale METHOD=NONE leaked after removing ad block:\n%s", got)
	}
	if !strings.Contains(got, "main/seg00.ts") || !strings.Contains(got, "main/seg19.ts") {
		t.Fatalf("main segments missing:\n%s", got)
	}
	if !strings.Contains(got, "#EXT-X-KEY:METHOD=AES-128,URI=\"key.bin\"") {
		t.Fatalf("encryption key lost:\n%s", got)
	}
}

func TestFilterHLSAdsPreservesTwoLongLegitimateParts(t *testing.T) {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-TARGETDURATION:10\n")
	for idx := 0; idx < 12; idx++ {
		b.WriteString("#EXTINF:10.0,\npart1/seg.ts\n")
	}
	b.WriteString("#EXT-X-DISCONTINUITY\n")
	for idx := 0; idx < 12; idx++ {
		b.WriteString("#EXTINF:10.0,\npart2/seg.ts\n")
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	orig := b.String()
	if got := string(FilterHLSAds([]byte(orig))); got != orig {
		t.Fatalf("two equal-length parts should not be filtered:\n%s", got)
	}
}

func TestHeaderProxyAppliesHLSAdFilterToggle(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		_, _ = io.WriteString(w, "#EXTM3U\n#EXTINF:10,\na.ts\n#EXTINF:5,\nadjump/ad.ts\n#EXTINF:10,\nb.ts\n#EXT-X-ENDLIST\n")
	}))
	defer up.Close()
	p := &HeaderProxy{}
	if err := p.Start(); err != nil {
		t.Fatal(err)
	}
	if err := p.Grant(up.URL+"/index.m3u8", nil, true); err != nil {
		t.Fatal(err)
	}
	wrapped, err := p.WrapGranted(up.URL+"/index.m3u8", nil)
	if err != nil {
		t.Fatal(err)
	}

	p.SetHLSAdFilter(true)
	resp, err := http.Get(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	bodyOn, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(bodyOn), "ad.ts") {
		t.Fatalf("expected ad segment stripped when filter enabled:\n%s", bodyOn)
	}

	p.SetHLSAdFilter(false)
	resp, err = http.Get(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	bodyOff, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(bodyOff), "ad.ts") {
		t.Fatalf("expected ad segment preserved when filter disabled:\n%s", bodyOff)
	}
}

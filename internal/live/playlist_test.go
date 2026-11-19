package live

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPlaylistMetadataHeadersAndRelativeURLs(t *testing.T) {
	base, _ := url.Parse("https://example.com/lists/list.m3u")
	data := "\ufeff#EXTM3U\r\n#EXTINF:-1 group-title=\"新闻,中文\" tvg-logo=\"../logo.png\" tvg-id='cctv1',CCTV 1\r\n#EXTVLCOPT:http-user-agent=player\r\n#EXTVLCOPT:http-referrer=https://example.com/\r\n../play?id=a$b%23c|Origin=https%3A%2F%2Fexample.com\r\n"
	channels, err := Playlist([]byte(data), base)
	if err != nil || len(channels) != 1 {
		t.Fatalf("parse: %+v %v", channels, err)
	}
	c := channels[0]
	if c.Group != "新闻,中文" || c.Name != "CCTV 1" || c.Logo != "https://example.com/logo.png" || c.TVGID != "cctv1" {
		t.Fatalf("metadata: %+v", c)
	}
	if c.URL != "https://example.com/play?id=a$b%23c" || c.Headers["User-Agent"] != "player" || c.Headers["Origin"] != "https://example.com" {
		t.Fatalf("url/header: %+v", c)
	}
}

func TestPlaylistRejectsHLSAndPreservesHeaderVariants(t *testing.T) {
	for _, tag := range []string{"#EXT-X-TARGETDURATION:6", "#EXT-X-STREAM-INF:BANDWIDTH=100"} {
		if _, err := Playlist([]byte("#EXTM3U\n"+tag+"\n#EXTINF:6,part\nhttps://example.com/seg.ts"), nil); err == nil {
			t.Fatal("HLS interpreted as channel subscription")
		}
	}
	data := "新闻,#genre#\n台一,https://example.com/one|User-Agent=A\n台一,https://example.com/one|User-Agent=A\n台一,https://example.com/one|User-Agent=B\n"
	channels, err := Playlist([]byte(data), nil)
	if err != nil || len(channels) != 1 || len(channels[0].Lines) != 2 {
		t.Fatalf("same-name lines: %+v %v", channels, err)
	}
	if channels[0].Lines[0].Headers["User-Agent"] != "A" || channels[0].Lines[1].Headers["User-Agent"] != "B" {
		t.Fatalf("header lines: %+v", channels[0].Lines)
	}
	if _, err := Playlist([]byte("name,https://example.com/one|Cookie=x%0D%0AX-Test%3Abad"), nil); err == nil {
		t.Fatal("header injection accepted")
	}
}

func TestTVBoxWrapperCommentsHeadersAndFinalBase(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tv":
			text := `{"spider":"will-not-execute.jar","sites":[],// comment
"lives":[{"name":"新闻","url":"list","ua":"fixture-player","header":{"Referer":"https://fixture.example/"}},]}`
			w.Write(append([]byte{0xff, 0xd8, 0, 0xff, 0xd9}, []byte("marker**"+base64.StdEncoding.EncodeToString([]byte(text)))...))
		case "/list":
			if r.UserAgent() != "fixture-player" || r.Referer() != "https://fixture.example/" {
				t.Error("feed headers not forwarded")
			}
			http.Redirect(w, r, "/redirected/list.m3u", 302)
		case "/redirected/list.m3u":
			fmt.Fprint(w, "#EXTM3U\n#EXTINF:-1,台一\n../live.php?id=a$b%23c\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	catalog, err := (Loader{AllowLAN: true}).Load(context.Background(), server.URL+"/tv")
	if err != nil || len(catalog.Channels) != 1 {
		t.Fatalf("wrapper parse: %+v %v", catalog, err)
	}
	ch := catalog.Channels[0]
	if ch.URL != server.URL+"/live.php?id=a$b%23c" || ch.Headers["User-Agent"] != "fixture-player" || ch.Group != "新闻" {
		t.Fatalf("lost final URL/header metadata: %+v", ch)
	}
}

func TestExtHTTPProxyAndNestedGroups(t *testing.T) {
	base, _ := url.Parse("https://example.com/list.m3u")
	encoded := base64.StdEncoding.EncodeToString([]byte("https://cdn.example/alt.m3u8"))
	text := "#EXTM3U\n#EXTINF:-1,台一\n#EXTHTTP:{\"User-Agent\":\"exthttp\",\"X-Trace\":\"1\"}\nhttps://cdn.example/a.m3u8\n#EXTINF:-1,台一\nproxy://do=live&url=" + encoded + "\n"
	channels, err := Playlist([]byte(text), base)
	if err != nil || len(channels) != 1 || len(channels[0].Lines) != 2 {
		t.Fatalf("%v %+v", err, channels)
	}
	if channels[0].Lines[0].Headers["User-Agent"] != "exthttp" || channels[0].Lines[0].Headers["X-Trace"] != "1" {
		t.Fatalf("exthttp %+v", channels[0].Lines[0].Headers)
	}
	if channels[0].Lines[1].URL != "https://cdn.example/alt.m3u8" {
		t.Fatal(channels[0].Lines[1].URL)
	}
	body := `{"lives":[{"name":"总","channels":[{"name":"新闻","channels":[{"name":"台二","urls":["https://cdn.example/b.m3u8","https://cdn.example/c.m3u8"]}]},{"name":"台三","group":"体育","url":"https://cdn.example/d.m3u8"}]}]}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) }))
	defer server.Close()
	catalog, err := (Loader{AllowLAN: true}).Load(context.Background(), server.URL)
	if err != nil || len(catalog.Channels) != 2 {
		t.Fatalf("%v %+v", err, catalog)
	}
	if catalog.Channels[0].Name != "台二" || catalog.Channels[0].Group != "新闻" || len(catalog.Channels[0].Lines) != 2 {
		t.Fatalf("nested %+v", catalog.Channels[0])
	}
	if catalog.Channels[1].Group != "体育" || catalog.Channels[1].URL != "https://cdn.example/d.m3u8" {
		t.Fatalf("group %+v", catalog.Channels[1])
	}
}

func TestProxyLiveListExpandsToChannels(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/tv.json":
			ext := base64.StdEncoding.EncodeToString([]byte(srv.URL + "/live.txt"))
			fmt.Fprintf(w, `{"lives":[{"name":"新闻","channels":[{"name":"台一","urls":["proxy://do=live&type=txt&ext=%s"]}]}]}`, ext)
		case "/live.txt":
			fmt.Fprint(w, "新闻,#genre#\n台一,https://cdn.example/a.m3u8\n台一,https://cdn.example/b.m3u8\n")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	cat, err := (Loader{AllowLAN: true}).Load(context.Background(), srv.URL+"/tv.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Channels) != 1 || len(cat.Channels[0].Lines) != 2 || strings.HasPrefix(cat.Channels[0].URL, "proxy://") {
		t.Fatalf("%+v warnings=%v", cat.Channels, cat.Warnings)
	}
	if cat.Channels[0].Lines[1].URL != "https://cdn.example/b.m3u8" {
		t.Fatal(cat.Channels[0].Lines[1].URL)
	}
}

func TestLoaderBlocksPrivateReferencesAndReportsPartialFailures(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "name,https://example.com/a.m3u8") }))
	defer server.Close()
	if _, err := (Loader{}).Load(context.Background(), server.URL); err == nil || !strings.Contains(err.Error(), "局域网") {
		t.Fatalf("private feed accepted: %v", err)
	}
}

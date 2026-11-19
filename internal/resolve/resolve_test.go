package resolve

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStaticExtractionFromVideoPlayerAndIframe(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/video":
			w.Header().Set("Content-Type", "text/html")
			fmtPage(w, `<video src="/media/a.m3u8"></video>`)
		case "/player":
			raw := "https://cdn.example/ep.mp4"
			enc := base64.StdEncoding.EncodeToString([]byte(raw))
			w.Header().Set("Content-Type", "text/html")
			fmtPage(w, `<script>var player_aaaa={"flag":"play","encrypt":2,"url":"`+enc+`"}</script>`)
		case "/frame":
			w.Header().Set("Content-Type", "text/html")
			fmtPage(w, `<iframe src="`+srv.URL+`/inner"></iframe>`)
		case "/inner":
			w.Header().Set("Content-Type", "text/html")
			fmtPage(w, `<source src="https://cdn.example/from-frame.mp4">`)
		case "/sniff":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:1,\nseg.ts\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	video, err := Resolve(t.Context(), Options{URL: srv.URL + "/video"})
	if err != nil || video.URL != srv.URL+"/media/a.m3u8" || video.Headers["Referer"] != srv.URL+"/video" {
		t.Fatalf("video %+v %v", video, err)
	}
	player, err := Resolve(t.Context(), Options{URL: srv.URL + "/player"})
	if err != nil || player.URL != "https://cdn.example/ep.mp4" {
		t.Fatalf("player %+v %v", player, err)
	}
	frame, err := Resolve(t.Context(), Options{URL: srv.URL + "/frame"})
	if err != nil || frame.URL != "https://cdn.example/from-frame.mp4" {
		t.Fatalf("frame %+v %v", frame, err)
	}
	sniff, err := Resolve(t.Context(), Options{URL: srv.URL + "/sniff"})
	if err != nil || sniff.URL != srv.URL+"/sniff" {
		t.Fatalf("sniff %+v %v", sniff, err)
	}
}

func TestParsersMatchFlagAndPreferJSON(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/page":
			w.Header().Set("Content-Type", "text/html")
			fmtPage(w, `<html>no media</html>`)
		case "/json":
			if r.URL.Query().Get("url") == "" || r.Header.Get("X-Parse") != "1" {
				http.Error(w, "bad", 400)
				return
			}
			_, _ = w.Write([]byte(`{"code":0,"data":{"url":"https://cdn.example/out.m3u8"}}`))
		case "/web":
			w.Header().Set("Content-Type", "text/html")
			fmtPage(w, `<a href="https://cdn.example/web.mp4">x</a>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	page := srv.URL + "/page"
	miss, err := Resolve(t.Context(), Options{
		URL: page, Flag: "线路甲",
		Parsers: []Parser{{Name: "其他", Type: 1, URL: srv.URL + "/json?url=", Flags: []string{"线路乙"}}},
	})
	if err == nil || miss.URL != "" {
		t.Fatalf("flag mismatch should fail: %+v %v", miss, err)
	}
	got, err := Resolve(t.Context(), Options{
		URL: page, Flag: "线路甲",
		Parsers: []Parser{
			{Name: "跳过", Type: 2, URL: srv.URL + "/json?url="},
			{Name: "JSON", Type: 1, URL: srv.URL + "/json?url={url}", Flags: []string{"线路甲"}, Header: map[string]string{"X-Parse": "1"}},
		},
	})
	if err != nil || got.URL != "https://cdn.example/out.m3u8" || got.Headers["X-Parse"] != "1" {
		t.Fatalf("json %+v %v", got, err)
	}
	web, err := Resolve(t.Context(), Options{
		URL: page, Flag: "线路甲",
		Parsers: []Parser{{Name: "网页", Type: 0, URL: srv.URL + "/web?play="}},
	})
	if err != nil || web.URL != "https://cdn.example/web.mp4" {
		t.Fatalf("web %+v %v", web, err)
	}
}

func TestEncryptOneUnescapesPlayerURL(t *testing.T) {
	html := `<script>player_data={"encrypt":1,"url":"https%3A%2F%2Fcdn.example%2Fa.m3u8"}</script>`
	got := macCMSURLs(html)
	if len(got) != 1 || got[0] != "https://cdn.example/a.m3u8" {
		t.Fatal(got)
	}
	if strings.Contains(strings.Join(mediaLiterals(`"https://cdn.example/b.mp4"`, nil), ","), " ") {
		t.Fatal("literal kept whitespace")
	}
}

func fmtPage(w http.ResponseWriter, body string) {
	_, _ = w.Write([]byte("<!doctype html><html><body>" + body + "</body></html>"))
}

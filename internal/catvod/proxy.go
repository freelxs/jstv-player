package catvod

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
)

// HeaderProxy serves streams that need request headers (Referer, Origin,
// User-Agent, Cookie) to players that cannot set them, such as the
// webview's <video>. HLS playlists are rewritten so that segments and keys
// go through the proxy too.
type HeaderProxy struct {
	Client *http.Client
	base   string
}

// Start listens on 127.0.0.1 (random port).
func (p *HeaderProxy) Start() error {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	p.base = "http://" + l.Addr().String()
	go http.Serve(l, p)
	return nil
}

// Wrap returns a proxied URL for u with headers h (u itself if h is empty).
func (p *HeaderProxy) Wrap(u string, h map[string]string) string {
	if len(h) == 0 || p.base == "" {
		return u
	}
	return p.wrap(u, encodeHeaders(h))
}

// WrapAlways proxies u even without headers (e.g. so plain-http media is
// served from loopback).
func (p *HeaderProxy) WrapAlways(u string, h map[string]string) string {
	if p.base == "" || !(strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")) {
		return u
	}
	if h == nil {
		h = map[string]string{}
	}
	return p.wrap(u, encodeHeaders(h))
}

// wrap builds /p/<name>?h=…&u=…. The last path element carries a file
// name with an extension, because FFmpeg's HLS demuxer (mpv, IINA,
// ffprobe) rejects segment URLs whose path has no known extension.
func (p *HeaderProxy) wrap(u, enc string) string {
	return p.base + "/p/" + proxyName(u) + "?h=" + enc + "&u=" + url.QueryEscape(u)
}

var safeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func proxyName(u string) string {
	name := "stream"
	if pu, err := url.Parse(u); err == nil {
		if b := path.Base(pu.Path); b != "/" && b != "." && b != "" {
			name = b
		}
	}
	name = safeName.ReplaceAllString(name, "_")
	if len(name) > 80 {
		name = name[len(name)-80:]
	}
	if path.Ext(name) == "" {
		name += ".bin"
	}
	return name
}

// segName names a playlist entry: playlists keep .m3u8; media segments
// are named .ts whatever they pretend to be (.jpg/.png disguised TS).
func segName(u string) string {
	n := proxyName(u)
	switch strings.ToLower(path.Ext(n)) {
	case ".m3u8", ".m3u", ".ts", ".m4s", ".mp4", ".aac", ".key", ".vtt", ".m4a", ".mp3":
		return n
	}
	return strings.TrimSuffix(n, path.Ext(n)) + ".ts"
}

func encodeHeaders(h map[string]string) string {
	b, _ := json.Marshal(h)
	return base64.RawURLEncoding.EncodeToString(b)
}

var uriAttr = regexp.MustCompile(`URI="([^"]+)"`)

// RewritePlaylist makes every URI in an m3u8 absolute (against base) and
// passes it through wrap.
func RewritePlaylist(body []byte, base *url.URL, wrap func(string) string) []byte {
	abs := func(s string) string {
		r, err := base.Parse(strings.TrimSpace(s))
		if err != nil {
			return s
		}
		return wrap(r.String())
	}
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(body))
	sc.Buffer(make([]byte, 64<<10), 8<<20)
	for sc.Scan() {
		line := sc.Text()
		t := strings.TrimSpace(line)
		switch {
		case t == "":
		case strings.HasPrefix(t, "#"):
			line = uriAttr.ReplaceAllStringFunc(line, func(m string) string {
				return `URI="` + abs(uriAttr.FindStringSubmatch(m)[1]) + `"`
			})
		default:
			line = abs(t)
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

func (p *HeaderProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("u")
	enc := r.URL.Query().Get("h")
	tu, err := url.Parse(target)
	if err != nil || (tu.Scheme != "http" && tu.Scheme != "https") {
		http.Error(w, "bad url", 400)
		return
	}
	var h map[string]string
	if b, err := base64.RawURLEncoding.DecodeString(enc); err == nil {
		_ = json.Unmarshal(b, &h)
	}
	req, _ := http.NewRequestWithContext(r.Context(), "GET", target, nil)
	for k, v := range h {
		req.Header.Set(k, v)
	}
	looksM3U8 := strings.HasSuffix(strings.ToLower(tu.Path), ".m3u8") || strings.HasSuffix(strings.ToLower(r.URL.Path), ".m3u8")
	if rg := r.Header.Get("Range"); rg != "" && !looksM3U8 {
		req.Header.Set("Range", rg)
	}
	c := p.Client
	if c == nil {
		c = http.DefaultClient
	}
	resp, err := c.Do(req)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Access-Control-Allow-Origin", "*")
	ct := resp.Header.Get("Content-Type")
	isM3U8 := strings.Contains(strings.ToLower(ct), "mpegurl") || looksM3U8
	if isM3U8 && (resp.StatusCode == 200 || resp.StatusCode == 206) {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		if bytes.HasPrefix(bytes.TrimSpace(body), []byte("#EXTM3U")) {
			body = RewritePlaylist(body, resp.Request.URL, func(s string) string {
				return p.base + "/p/" + segName(s) + "?h=" + enc + "&u=" + url.QueryEscape(s)
			})
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Write(body)
			return
		}
		w.Header().Set("Content-Type", ct)
		w.Write(body)
		return
	}
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

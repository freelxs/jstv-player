package app

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/freelxs/jstv-player/internal/catvod"
	"github.com/freelxs/jstv-player/internal/resolve"
	"github.com/freelxs/jstv-player/internal/tvbox"
)

func needsPageResolve(st catvod.Stream) bool {
	if !catvod.IsHTTPMediaURL(st.URL) || mediaFile(st.URL) {
		return false
	}
	return st.Parse || catvod.IsWebPage(st.URL)
}

func (p *Player) extensionlessPage(ctx context.Context, st catvod.Stream) bool {
	if st.Direct || st.Parse || mediaFile(st.URL) || catvod.IsWebPage(st.URL) || !catvod.IsHTTPMediaURL(st.URL) {
		return false
	}
	return peekKind(ctx, st.URL, st.Headers) == "html"
}

func peekKind(ctx context.Context, raw string, headers map[string]string) string {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return ""
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Range", "bytes=0-2047")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "mpegurl") || strings.Contains(ct, "video/") || bytesHasM3U(body) {
		return "media"
	}
	if strings.Contains(ct, "text/html") || (len(body) > 0 && body[0] == '<') {
		return "html"
	}
	return ""
}

func bytesHasM3U(b []byte) bool {
	s := strings.TrimSpace(string(b))
	return strings.HasPrefix(s, "#EXTM3U")
}

func mediaFile(raw string) bool {
	u := strings.ToLower(strings.Split(raw, "?")[0])
	for _, ext := range []string{".m3u8", ".mp4", ".m4v", ".flv", ".mkv", ".webm", ".mpd"} {
		if strings.HasSuffix(u, ext) {
			return true
		}
	}
	return false
}

func (p *Player) materializeStreams(flag string, streams []catvod.Stream) ([]catvod.Stream, error) {
	out := append([]catvod.Stream(nil), streams...)
	p.mu.Lock()
	ctx := p.sourceContext
	p.mu.Unlock()
	if ctx == nil {
		ctx = context.Background()
	}
	parsers := p.playbackParsers()
	allow := p.GetSettings().AllowLANMedia
	var sniff func(context.Context, string, map[string]string) (string, map[string]string, error)
	if !p.webMode.Load() {
		sniff = func(ctx context.Context, page string, headers map[string]string) (string, map[string]string, error) {
			found, err := sniffPage(ctx, page, headers)
			if err != nil {
				return "", nil, err
			}
			return found.URL, found.Headers, nil
		}
	}
	for i := range out {
		if !needsPageResolve(out[i]) && !p.extensionlessPage(ctx, out[i]) {
			continue
		}
		res, err := resolve.Resolve(ctx, resolve.Options{
			URL: out[i].URL, Headers: out[i].Headers, Flag: flag, Parsers: parsers, AllowLAN: allow, Sniff: sniff,
		})
		if err == nil && res.URL != "" && res.URL != out[i].URL {
			applyResolved(&out[i], res.URL, res.Headers)
			p.log("播放页已解析为媒体地址")
			continue
		}
		if err != nil {
			p.log("播放页解析: " + err.Error())
		}
		if p.webMode.Load() {
			continue
		}
		found, serr := sniffPage(ctx, out[i].URL, out[i].Headers)
		if serr != nil {
			return nil, serr
		}
		applyResolved(&out[i], found.URL, found.Headers)
		p.log("隐藏页面已嗅探到媒体地址")
	}
	return out, nil
}

func applyResolved(st *catvod.Stream, media string, headers map[string]string) {
	st.URL = media
	st.Parse = false
	st.Direct = true
	if len(headers) > 0 {
		st.Headers = headers
	}
}

func (p *Player) playbackParsers() []resolve.Parser {
	p.mu.Lock()
	cfg := p.vodConfig
	p.mu.Unlock()
	if cfg == nil {
		return nil
	}
	return parsersFromConfig(cfg)
}

func parsersFromConfig(cfg *tvbox.Config) []resolve.Parser {
	if cfg == nil {
		return nil
	}
	out := make([]resolve.Parser, 0, len(cfg.Parses))
	for _, item := range cfg.Parses {
		out = append(out, resolve.Parser{
			Name: item.Name, Type: item.Type, URL: item.URL, Flags: append([]string(nil), item.Flags...), Header: item.Header,
		})
	}
	return out
}

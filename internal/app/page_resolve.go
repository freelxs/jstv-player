package app

import (
	"context"
	"strings"

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
	for i := range out {
		if !needsPageResolve(out[i]) {
			continue
		}
		res, err := resolve.Resolve(ctx, resolve.Options{
			URL: out[i].URL, Headers: out[i].Headers, Flag: flag, Parsers: parsers, AllowLAN: allow,
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

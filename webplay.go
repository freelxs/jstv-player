package main

import (
	"errors"
	"net/url"
	"strings"

	"github.com/freelxs/jstv-player/internal/catvod"
)

// WebPlayback is what the browser player needs. URLs are same-origin /media
// paths. Upstream addresses and request headers stay on the server.
type WebPlayback struct {
	Action     string       `json:"action"` // play, qualities, page
	Title      string       `json:"title"`
	URL        string       `json:"url,omitempty"`
	Media      string       `json:"media,omitempty"` // hls or file
	PageURL    string       `json:"pageUrl,omitempty"`
	Message    string       `json:"message,omitempty"`
	Qualities  []WebQuality `json:"qualities,omitempty"`
	PlaylistID string       `json:"playlistId,omitempty"`
	Index      int          `json:"index"`
	Count      int          `json:"count"`
}

// WebQuality is one already-proxied rendition.
type WebQuality struct {
	Name  string `json:"name"`
	URL   string `json:"url"`
	Media string `json:"media"`
}

// WebPlay resolves an episode for the browser. It never opens a native player.
func (p *Player) WebPlay(api, vodID, show, flag string, eps []catvod.Episode, start int) (*WebPlayback, error) {
	if !p.webMode.Load() {
		return nil, errors.New("仅网页模式可用")
	}
	if start < 0 || start >= len(eps) {
		return nil, errors.New("剧集不存在")
	}
	streams, err := p.Play(api, flag, eps[start].ID)
	if err != nil {
		return nil, err
	}
	s := p.GetSettings()
	subID := ""
	if sub, ok := s.active(); ok {
		subID = sub.ID
	}
	p.recordHistory(subID, api, vodID, HistoryEntry{Flag: flag, Index: start, Name: eps[start].Name})
	title := entryTitle(show, eps[start].Name)
	out := &WebPlayback{Title: title, Index: start, Count: len(eps)}

	var playable []catvod.Stream
	var page *catvod.Stream
	for i := range streams {
		st := streams[i]
		if st.Parse || catvod.IsWebPage(st.URL) {
			if page == nil {
				page = &streams[i]
			}
			continue
		}
		if catvod.IsHTTPMediaURL(st.URL) {
			playable = append(playable, st)
		}
	}
	if len(playable) == 0 {
		if page == nil {
			return nil, errors.New("没有可在浏览器播放的地址")
		}
		out.Action = "page"
		if u, msg := pageForBrowser(page.URL); u != "" {
			out.PageURL = u
		} else {
			out.Message = msg
		}
		return out, nil
	}

	// Several renditions, or 连播 off: the page picks or plays one file.
	// One HTTP rendition with 连播 on: later episodes resolve when the
	// browser reaches them, the same way the desktop built-in player does.
	continuous := !s.DisableContinuous && len(playable) == 1
	if !continuous {
		qs := make([]WebQuality, 0, len(playable))
		for i := range playable {
			q, html, err := p.browserQuality(playable[i])
			if err != nil {
				return nil, err
			}
			if html {
				out.Action = "page"
				if u, msg := pageForBrowser(playable[i].URL); u != "" {
					out.PageURL = u
				} else {
					out.Message = msg
				}
				return out, nil
			}
			qs = append(qs, q)
		}
		if len(qs) == 1 {
			out.Action = "play"
			out.URL = qs[0].URL
			out.Media = qs[0].Media
			p.log("网页播放: " + title)
			return out, nil
		}
		out.Action = "qualities"
		out.Qualities = qs
		return out, nil
	}

	chosen := playable[0]
	q, html, err := p.browserQuality(chosen)
	if err != nil {
		return nil, err
	}
	if html {
		out.Action = "page"
		if u, msg := pageForBrowser(chosen.URL); u != "" {
			out.PageURL = u
		} else {
			out.Message = msg
		}
		return out, nil
	}
	first := chosen
	id := p.eps.add(&playlist{
		api: api, vodID: vodID, show: show, flag: flag, subID: subID,
		eps: eps, start: start, first: &first,
	})
	out.Action = "play"
	out.URL = q.URL
	out.Media = q.Media
	out.PlaylistID = id
	p.log("网页连播: " + title)
	return out, nil
}

// WebAdvance resolves a later episode of a browser playlist.
func (p *Player) WebAdvance(id string, index int) (*WebPlayback, error) {
	if !p.webMode.Load() {
		return nil, errors.New("仅网页模式可用")
	}
	pl := p.eps.get(id)
	if pl == nil || index < 0 || index >= len(pl.eps) {
		return nil, errors.New("没有这一集")
	}
	abs, err := p.resolveEpisode(pl, index)
	if err != nil {
		return nil, err
	}
	pub, err := p.toPublicMedia(abs)
	if err != nil {
		return nil, err
	}
	return &WebPlayback{
		Action:     "play",
		Title:      entryTitle(pl.show, pl.eps[index].Name),
		URL:        pub,
		Media:      mediaKind(abs),
		PlaylistID: id,
		Index:      index,
		Count:      len(pl.eps),
	}, nil
}

func (p *Player) browserQuality(st catvod.Stream) (WebQuality, bool, error) {
	if p.proxy == nil {
		return WebQuality{}, false, errors.New("本地播放代理未启动")
	}
	abs, err := p.proxy.WrapGranted(st.URL, st.Headers)
	if err != nil {
		return WebQuality{}, false, err
	}
	html, err := catvod.Preflight(nil, abs, nil)
	if err != nil {
		p.log("preflight: " + err.Error())
		return WebQuality{}, false, err
	}
	if html {
		return WebQuality{}, true, nil
	}
	pub, err := p.toPublicMedia(abs)
	if err != nil {
		return WebQuality{}, false, err
	}
	name := strings.TrimSpace(st.Name)
	if name == "" {
		name = "播放"
	}
	return WebQuality{Name: name, URL: pub, Media: mediaKind(abs)}, false, nil
}

func (p *Player) toPublicMedia(abs string) (string, error) {
	base := ""
	if p.proxy != nil {
		base = p.proxy.BaseURL()
	}
	pub, ok := publicMediaPath(base, abs)
	if !ok {
		return "", errors.New("无法生成浏览器播放地址")
	}
	return pub, nil
}

func (p *Player) pluginPosterHost() string {
	p.mu.Lock()
	h := p.host
	p.mu.Unlock()
	if h == nil {
		return ""
	}
	b := h.Base()
	u, err := url.Parse(b)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Host
}

// publicMediaPath maps a loopback proxy URL onto the web server's /media tree.
func publicMediaPath(proxyBase, absolute string) (string, bool) {
	proxyBase = strings.TrimRight(strings.TrimSpace(proxyBase), "/")
	if proxyBase == "" || absolute == "" {
		return "", false
	}
	if !strings.HasPrefix(absolute, proxyBase+"/") {
		return "", false
	}
	rest := strings.TrimPrefix(absolute, proxyBase)
	if !strings.HasPrefix(rest, "/p/") || strings.Contains(rest, "..") {
		return "", false
	}
	return "/media" + rest, true
}

func mediaKind(proxyURL string) string {
	u := strings.ToLower(strings.Split(proxyURL, "?")[0])
	if strings.HasSuffix(u, ".m3u8") || strings.HasSuffix(u, ".m3u") {
		return "hls"
	}
	return "file"
}

// pageForBrowser allows a new tab only for a public http(s) page. Plugin
// loopback pages are not proxied onto this origin.
func pageForBrowser(raw string) (string, string) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return "", "这个地址不是浏览器能打开的网页"
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || host == "127.0.0.1" || host == "::1" || catvod.IsLANAddressURL(raw) {
		return "", "这个播放地址是插件本机页面，浏览器访问不到。请换一条直链线路。"
	}
	return raw, ""
}

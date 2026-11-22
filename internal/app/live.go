package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/freelxs/jstv-player/internal/atomicfile"
	"github.com/freelxs/jstv-player/internal/catvod"
	"github.com/freelxs/jstv-player/internal/live"
)

func isLiveSubscription(sub Subscription) bool {
	if sub.Type == "live" {
		return true
	}
	if sub.Type == "plugin" || sub.Type == "tvbox" || sub.Type == "kazumi" {
		return false
	}
	u, err := url.Parse(sub.URL)
	if err != nil {
		return false
	}
	path := strings.ToLower(u.Path)
	return !strings.HasSuffix(path, ".js") && !strings.HasSuffix(path, ".md5")
}

func (p *Player) loadLive(ctx context.Context, sub Subscription, dir string, generation uint64, useCache bool) (*LoadResult, error) {
	var catalog *live.Catalog
	cache := filepath.Join(dir, "live.json")
	if useCache {
		b, err := os.ReadFile(cache)
		if err == nil {
			var c live.Catalog
			if err := json.Unmarshal(b, &c); err != nil {
				return nil, errors.New("直播缓存损坏，请刷新订阅")
			}
			if c.URL == sub.URL && len(c.Channels) > 0 {
				catalog = &c
			}
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	if catalog == nil {
		var err error
		catalog, err = (live.Loader{AllowLAN: p.GetSettings().AllowLANMedia}).Load(ctx, sub.URL)
		if err != nil {
			return nil, err
		}
	}
	p.mu.Lock()
	if ctx.Err() != nil || generation != p.sourceGeneration {
		p.mu.Unlock()
		return nil, context.Canceled
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	if err := atomicfile.JSON(cache, catalog); err != nil {
		p.mu.Unlock()
		return nil, err
	}
	p.liveCatalog = catalog
	p.liveAPI = fmt.Sprintf("live:%s:%d", sub.ID, generation)
	p.sites = []catvod.Site{{Key: "native-live", Name: "📺 " + sub.Name, Type: 3, API: p.liveAPI}}
	sites := append([]catvod.Site(nil), p.sites...)
	p.mu.Unlock()
	p.setKeepRunning(true)
	return &LoadResult{Sub: sub, Sites: sites, Warnings: catalog.Warnings}, nil
}

func (p *Player) nativeCatalog(api string) (*live.Catalog, error, bool) {
	if !strings.HasPrefix(api, "live:") {
		return nil, nil, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.liveCatalog == nil || api != p.liveAPI {
		return nil, errSourceChanged, true
	}
	return p.liveCatalog, nil, true
}

func liveVOD(ch live.Channel) catvod.M {
	return catvod.M{"vod_id": ch.ID, "vod_name": ch.Name, "vod_pic": ch.Logo, "vod_remarks": "直播", "type_name": ch.Group}
}

func liveHome(c *live.Catalog) catvod.M {
	seen := map[string]bool{}
	classes := []any{}
	for _, ch := range c.Channels {
		if !seen[ch.Group] {
			seen[ch.Group] = true
			classes = append(classes, catvod.M{"type_id": ch.Group, "type_name": ch.Group})
		}
	}
	return catvod.M{"class": classes}
}

func liveCategory(c *live.Catalog, group, query string, page int) catvod.M {
	if page < 1 {
		page = 1
	}
	const size = 100
	list := []any{}
	total := 0
	for _, ch := range c.Channels {
		if group != "" && ch.Group != group {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(ch.Name+" "+ch.Group), strings.ToLower(query)) {
			continue
		}
		if total >= (page-1)*size && len(list) < size {
			list = append(list, liveVOD(ch))
		}
		total++
	}
	return catvod.M{"page": page, "pagecount": max(1, (total+size-1)/size), "limit": size, "total": total, "list": list}
}

func liveDetail(c *live.Catalog, id string) (*DetailResult, error) {
	for _, ch := range c.Channels {
		if ch.ID == id {
			return &DetailResult{Vod: liveVOD(ch), Lines: []catvod.Line{{Flag: "直播", Episodes: []catvod.Episode{{Name: ch.Name, ID: ch.ID}}}}}, nil
		}
	}
	return nil, errors.New("直播频道不存在，请刷新列表")
}

func (p *Player) playLive(api, id string, generation uint64) ([]catvod.Stream, error) {
	c, err, _ := p.nativeCatalog(api)
	if err != nil {
		return nil, err
	}
	var channel *live.Channel
	for i := range c.Channels {
		if c.Channels[i].ID == id {
			channel = &c.Channels[i]
			break
		}
	}
	if channel == nil {
		return nil, errors.New("直播频道不存在")
	}
	lines := channel.Lines
	if len(lines) == 0 && channel.URL != "" {
		lines = []live.Line{{URL: channel.URL, Headers: channel.Headers}}
	}
	settings := p.GetSettings()
	var httpLines []live.Line
	for _, line := range lines {
		if !catvod.IsHTTPMediaURL(line.URL) {
			continue
		}
		if !settings.AllowLANMedia && catvod.IsLocalNetworkURL(line.URL) {
			continue
		}
		httpLines = append(httpLines, line)
	}
	if len(httpLines) == 0 {
		return nil, errors.New("当前直播仅支持 HTTP(S) 媒体；此频道的协议需要专用播放器，或请先允许局域网媒体")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if generation != p.sourceGeneration || p.liveAPI != api {
		return nil, errSourceChanged
	}
	if p.proxy == nil {
		return nil, errors.New("本地播放代理未启动")
	}
	for _, line := range httpLines {
		if err := p.proxy.GrantLive(line.URL, line.Headers, p.settings.AllowLANMedia); err != nil {
			return nil, err
		}
	}
	stream := catvod.Stream{Direct: true, Name: channel.Name, URL: httpLines[0].URL, Headers: httpLines[0].Headers}
	for i, line := range httpLines[1:] {
		stream.Fallbacks = append(stream.Fallbacks, catvod.Fallback{Name: fmt.Sprintf("线路%d", i+2), URL: line.URL, Headers: line.Headers})
	}
	return []catvod.Stream{stream}, nil
}

func (p *Player) proxiedFallbacks(items []catvod.Fallback) []string {
	if p == nil || p.proxy == nil || len(items) == 0 {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		u, err := p.proxy.WrapGranted(item.URL, item.Headers)
		if err != nil || u == "" {
			continue
		}
		out = append(out, u)
	}
	return out
}

func (p *Player) withWebLines(out *WebPlayback, st catvod.Stream) {
	if out == nil || out.URL == "" || len(st.Fallbacks) == 0 {
		return
	}
	lines := []WebQuality{{Name: "线路1", URL: out.URL, Media: out.Media}}
	for i, item := range st.Fallbacks {
		abs, err := p.proxy.WrapGranted(item.URL, item.Headers)
		if err != nil {
			continue
		}
		pub, err := p.toPublicMedia(abs)
		if err != nil {
			continue
		}
		name := strings.TrimSpace(item.Name)
		if name == "" {
			name = fmt.Sprintf("线路%d", i+2)
		}
		media := "file"
		if strings.Contains(strings.ToLower(item.URL), ".m3u8") {
			media = "hls"
		}
		lines = append(lines, WebQuality{Name: name, URL: pub, Media: media})
	}
	if len(lines) > 1 {
		out.Lines = lines
	}
}

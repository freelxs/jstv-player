package app

import (
	"errors"
	"strings"

	"github.com/freelxs/jstv-player/internal/catvod"
)

// Detail returns a vod with its play lines.
func (p *Player) Detail(api, id string) (*DetailResult, error) {
	if c, err, ok := p.nativeCatalog(api); ok {
		if err != nil {
			return nil, err
		}
		return liveDetail(c, id)
	}
	c, err := p.client(api)
	if err != nil {
		return nil, err
	}
	m, err := c.Detail(api, id)
	if err != nil {
		return nil, err
	}
	l, _ := m["list"].([]any)
	if len(l) == 0 {
		return nil, errors.New("详情为空")
	}
	vod, _ := l[0].(map[string]any)
	from, _ := vod["vod_play_from"].(string)
	urls, _ := vod["vod_play_url"].(string)
	return &DetailResult{Vod: vod, Lines: catvod.ParseLines(from, urls)}, nil
}

// Play resolves an episode to streams.
func (p *Player) Play(api, flag, id string) ([]catvod.Stream, error) {
	return p.playAtGeneration(api, flag, id, p.generation())
}

func (p *Player) playAtGeneration(api, flag, id string, generation uint64) ([]catvod.Stream, error) {
	if !p.isGeneration(generation) {
		return nil, errSourceChanged
	}
	if strings.HasPrefix(api, "live:") {
		return p.playLive(api, id, generation)
	}
	c, err := p.client(api)
	if err != nil {
		return nil, err
	}
	if !p.isGeneration(generation) {
		return nil, errSourceChanged
	}
	m, err := c.Play(api, flag, id)
	if err != nil {
		return nil, err
	}
	s := catvod.NormalizePlay(m)
	if len(s) == 0 {
		if e, ok := m["error"].(string); ok && e != "" {
			p.log("源返回错误: " + e)
			return nil, errors.New("站点解析失败，请稍后重试或查看日志")
		}
		return nil, errors.New("没有可播放的地址")
	}
	var allowed []string
	bridgeAddr := p.bridgeAddr()
	if bridgeAddr != "" {
		allowed = append(allowed, bridgeAddr)
	}
	catvod.LocalizePlayStreams(s, c.Base, bridgeAddr, c.LAN)
	if err := catvod.CheckPluginPlayStreams(s, c.Base, allowed...); err != nil {
		p.log("play: " + err.Error())
		return nil, err
	}
	if !p.GetSettings().AllowLANMedia {
		for _, st := range s {
			if !st.Parse && catvod.IsLANAddressURL(st.URL) {
				return nil, errors.New("播放地址指向局域网设备；请在设置中明确允许访问局域网媒体后重试")
			}
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if generation != p.sourceGeneration {
		return nil, errSourceChanged
	}
	if p.proxy != nil {
		for _, st := range s {
			if !st.Parse && catvod.IsHTTPMediaURL(st.URL) {
				if err := p.proxy.Grant(st.URL, st.Headers, p.settings.AllowLANMedia); err != nil {
					return nil, err
				}
			}
		}
	}
	return s, nil
}

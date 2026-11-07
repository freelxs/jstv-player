package app

import (
	"context"
	"errors"

	"github.com/freelxs/jstv-player/internal/catvod"
)

func (p *Player) client(api string) (*catvod.Client, error) {
	return p.clientWithContext(context.Background(), api)
}

func (p *Player) clientWithContext(ctx context.Context, api string) (*catvod.Client, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	p.mu.Lock()
	h := p.host
	p.mu.Unlock()
	if h == nil || h.Base() == "" {
		return nil, errors.New("源未加载")
	}
	c := &catvod.Client{Base: h.Base(), LAN: p.GetSettings().AllowLAN, Context: ctx}
	if api != "" {
		if err := p.initSite(api, c, h); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func (p *Player) initSite(api string, c *catvod.Client, expectedHost *catvod.Host) error {
	p.initMu.Lock()
	defer p.initMu.Unlock()
	p.mu.Lock()
	if p.host != expectedHost {
		p.mu.Unlock()
		return errors.New("订阅已切换，取消旧源初始化")
	}
	ready := p.inited[api]
	p.mu.Unlock()
	if ready {
		return nil
	}
	if err := c.Init(api); err != nil {
		p.log("init " + api + ": " + err.Error())
		return err
	}
	p.mu.Lock()
	if p.host != expectedHost {
		p.mu.Unlock()
		return errors.New("订阅已切换，忽略旧源初始化结果")
	}
	p.inited[api] = true
	p.mu.Unlock()
	return nil
}

// Home returns the site's categories and filters.
func (p *Player) Home(api string) (catvod.M, error) {
	c, err := p.client(api)
	if err != nil {
		return nil, err
	}
	return c.Home(api)
}

// Category lists one page of a category.
func (p *Player) Category(api, tid string, page int, filters map[string]string) (catvod.M, error) {
	c, err := p.client(api)
	if err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	return c.Category(api, tid, page, filters)
}

// Search searches one site.
func (p *Player) Search(api, wd string, page int) (catvod.M, error) {
	c, err := p.client(api)
	if err != nil {
		return nil, err
	}
	if page < 1 {
		page = 1
	}
	return c.Search(api, wd, page)
}

package main

import (
	"errors"
	"net/url"
	"strings"

	"github.com/egoist/mygo"
	"github.com/freelxs/jstv-player/internal/catvod"
)

// inAppNavigationURL reports a page on this machine. Those stay inside the
// app: handing them to the system browser drops the click when no external
// handler is presented.
func inAppNavigationURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".localhost")
}

func externalNavigationURL(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return !inAppNavigationURL(raw)
}

// configPageURL is the plugin's HTML configuration page. The JSON document at
// /website/ is the API and is not what a click should open.
func configPageURL(sourceBase string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(sourceBase))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("配置页面需要已加载的本机插件地址")
	}
	u.Path = "/website/index.html"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	u.User = nil
	return u.String(), nil
}

// configPageFitJS lets a configuration page scroll and scales images down
// instead of cropping them when the window is shorter than the code.
const configPageFitJS = `(() => {
  const css = 'html,body{height:auto!important;min-height:100%!important;overflow:auto!important}img{max-width:100%!important;height:auto!important;object-fit:contain!important}';
  let s = document.getElementById('jstv-page-fit');
  if (!s) { s = document.createElement('style'); s.id = 'jstv-page-fit'; (document.head || document.documentElement).appendChild(s); }
  s.textContent = css;
})()`

func (p *Player) openWebPage(title, pageURL string) {
	mygo.RunOnMain(func() {
		w := mygo.NewWindow(mygo.WindowOptions{Title: title, Width: 1100, Height: 700})
		page := w.Page()
		page.SetWindowOpenHandler(func(req mygo.WindowOpenRequest) *mygo.WindowOptions {
			if inAppNavigationURL(req.URL) {
				return &mygo.WindowOptions{Title: title, Width: 1100, Height: 700}
			}
			if externalNavigationURL(req.URL) {
				go mygo.Shell.OpenExternal(req.URL)
			}
			return nil
		})
		page.OnDidFinishLoad(func() {
			_, _ = page.Eval(configPageFitJS)
		})
		page.LoadURL(pageURL)
	})
}

// OpenConfigPage opens the loaded plugin's configuration page. In web mode
// the browser has to open the returned address itself.
func (p *Player) OpenConfigPage(api string) (string, error) {
	c, err := p.client(api)
	if err != nil {
		return "", err
	}
	page, err := configPageURL(c.Base)
	if err != nil {
		return "", err
	}
	if p.webMode.Load() {
		return page, nil
	}
	return page, p.Open(catvod.Stream{URL: page, Parse: true, Name: "配置"}, "配置")
}

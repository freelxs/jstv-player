package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/freelxs/jstv-player/internal/atomicfile"
	"github.com/freelxs/jstv-player/internal/catvod"
	"github.com/freelxs/jstv-player/internal/kazumi"
)

func kazumiCachePath(dir string) string {
	return filepath.Join(dir, "kazumi", "bundle.json")
}

func hasKazumiCache(dir string) bool {
	_, err := os.Stat(kazumiCachePath(dir))
	return err == nil
}

func shouldTryKazumiFirst(sub Subscription, dir string, useCache bool) bool {
	switch sub.Type {
	case "kazumi":
		return true
	case "plugin", "live", "tvbox":
		return false
	default:
		if useCache && hasKazumiCache(dir) {
			return true
		}
		return kazumi.LooksLikeRepoURL(sub.URL)
	}
}

func shouldTryKazumiFallback(sub Subscription) bool {
	if sub.Type != "" && sub.Type != "auto" {
		return false
	}
	return !kazumi.LooksLikeRepoURL(sub.URL) && !pluginURL(sub.URL) && !playlistFile(sub.URL)
}

func (p *Player) tryKazumi(ctx context.Context, s Settings, sub Subscription, dir string, generation uint64, useCache bool) (*LoadResult, bool, error) {
	if !s.AllowLANMedia && catvod.IsLANAddressURL(sub.URL) {
		if sub.Type == "kazumi" {
			return nil, true, errors.New("规则地址指向局域网；请在设置中允许局域网媒体后再加载")
		}
		return nil, false, nil
	}
	u, _ := url.Parse(strings.TrimSpace(sub.URL))
	allowedLoopback := catvod.AllowedLoopbackAddr(u)
	var bundle *kazumi.Bundle
	if useCache {
		cached, err := readKazumiCache(dir)
		if err == nil && cached != nil && strings.TrimSpace(cached.SourceURL) == strings.TrimSpace(sub.URL) && len(cached.Rules) > 0 {
			bundle = cached
		} else if err != nil && !errors.Is(err, os.ErrNotExist) {
			if sub.Type == "kazumi" {
				return nil, true, errors.New("Kazumi 规则缓存损坏，请重新加载订阅")
			}
			return nil, false, nil
		} else if sub.Type != "kazumi" {
			return nil, false, nil
		}
	}
	if bundle == nil {
		p.log("拉取 Kazumi 规则订阅「" + sub.Name + "」: " + sub.URL)
		fetchClient := kazumiHTTPClient(s.AllowLANMedia, allowedLoopback, 30*time.Second)
		fetched, isKazumi, err := kazumi.FetchBundle(ctx, fetchClient, sub.URL)
		if err != nil {
			if sub.Type == "kazumi" || isKazumi || kazumi.LooksLikeRepoURL(sub.URL) {
				return nil, true, err
			}
			return nil, false, nil
		}
		if !isKazumi || fetched == nil {
			if sub.Type == "kazumi" {
				return nil, true, errors.New("该地址不是有效的 Kazumi 规则仓库或规则 JSON")
			}
			return nil, false, nil
		}
		bundle = fetched
	}
	ruleClient := kazumiHTTPClient(s.AllowLANMedia, allowedLoopback, 20*time.Second)
	srv := kazumi.NewServer(bundle, kazumi.Options{
		HTTP: ruleClient,
		Log:  p.log,
		Bangumi: &kazumi.BangumiClient{
			HTTP:  ruleClient,
			Token: func() string { return p.GetSettings().BangumiToken },
		},
	})
	if err := srv.Start(); err != nil {
		return nil, true, err
	}
	p.mu.Lock()
	if generation != p.sourceGeneration || ctx.Err() != nil {
		p.mu.Unlock()
		srv.Stop()
		return nil, true, context.Canceled
	}
	if err := writeKazumiCache(dir, bundle); err != nil {
		p.mu.Unlock()
		srv.Stop()
		return nil, true, err
	}
	p.host, p.sites, p.inited = srv, srv.Sites(), map[string]bool{}
	sites := append([]catvod.Site(nil), p.sites...)
	p.mu.Unlock()
	p.setKeepRunning(true)
	p.log(fmt.Sprintf("Kazumi 规则订阅已加载：%d 条规则可用", len(bundle.Rules)))
	return &LoadResult{Sites: sites, Sub: sub, Warnings: bundle.Warnings}, true, nil
}

func kazumiHTTPClient(allowLAN bool, allowedLoopback string, timeout time.Duration) *http.Client {
	c := tvboxHTTPClient(allowLAN, allowedLoopback)
	c.Timeout = timeout
	return c
}

func readKazumiCache(dir string) (*kazumi.Bundle, error) {
	b, err := os.ReadFile(kazumiCachePath(dir))
	if err != nil {
		return nil, err
	}
	var bundle kazumi.Bundle
	if err := json.Unmarshal(b, &bundle); err != nil {
		return nil, err
	}
	return &bundle, nil
}

func writeKazumiCache(dir string, bundle *kazumi.Bundle) error {
	path := kazumiCachePath(dir)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return atomicfile.JSON(path, bundle)
}

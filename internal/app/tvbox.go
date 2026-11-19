package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"github.com/freelxs/jstv-player/internal/tvbox"
)

var errNoTVBoxCache = errors.New("没有已保存的配置缓存")

func shouldTryTVBox(sub Subscription) bool {
	switch sub.Type {
	case "tvbox":
		return true
	case "plugin", "live":
		return false
	default:
		return !pluginURL(sub.URL) && !playlistFile(sub.URL)
	}
}

func pluginURL(raw string) bool {
	path := subscriptionPath(raw)
	return strings.HasSuffix(path, ".js") || strings.HasSuffix(path, ".mjs") || strings.HasSuffix(path, ".cjs") || strings.HasSuffix(path, ".md5")
}

func playlistFile(raw string) bool {
	path := subscriptionPath(raw)
	return strings.HasSuffix(path, ".m3u") || strings.HasSuffix(path, ".m3u8") || strings.HasSuffix(path, ".txt")
}

func subscriptionPath(raw string) string {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	path := strings.ToLower(raw)
	if err == nil && u.Path != "" {
		path = strings.ToLower(u.Path)
	}
	return strings.Split(path, "?")[0]
}

func (p *Player) tryTVBox(ctx context.Context, s Settings, sub Subscription, dir string, generation uint64, useCache bool) (*LoadResult, bool, error) {
	if !s.AllowLANMedia && catvod.IsLANAddressURL(sub.URL) {
		return nil, true, errors.New("配置地址指向局域网；请在设置中允许局域网媒体后再加载")
	}
	client := tvboxHTTPClient(s.AllowLANMedia)
	body, final, cfg, err := readTVBoxDocument(ctx, client, sub, dir, useCache)
	if err != nil {
		// A live list and a point-to-point config share the auto route.
		// Missing TVBox cache means this subscription was not a VOD config.
		if sub.Type != "tvbox" && (errors.Is(err, errNoTVBoxCache) || !useCache) {
			return nil, false, nil
		}
		return nil, true, err
	}
	resolved, body, final, pending, err := followDepot(ctx, client, cfg, body, final, splitRepoLine(sub.Line), 0)
	if err != nil {
		return nil, true, err
	}
	if pending != nil {
		lines := make([]RepoLine, 0, len(pending.Lines))
		for _, line := range pending.Lines {
			lines = append(lines, RepoLine{Name: line.Name, URL: line.URL})
		}
		return &LoadResult{Sub: sub, RepoPick: true, RepoLines: lines}, true, nil
	}
	if resolved == nil || len(resolved.Sites) == 0 {
		if sub.Type == "tvbox" {
			return nil, true, errors.New("这份配置没有点播站点")
		}
		return nil, false, nil
	}
	node, nerr := catvod.FindNode(s.NodePath)
	if nerr != nil {
		if strings.TrimSpace(s.NodePath) != "" {
			return nil, true, nerr
		}
		node = ""
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	if res, stop, err := p.needTVBoxTrust(sub, dir, hash, node); stop || err != nil {
		return res, true, err
	}
	if err := p.activateTVBox(ctx, s, sub, dir, generation, resolved, body, final, node); err != nil {
		return nil, true, err
	}
	p.mu.Lock()
	sites := append([]catvod.Site(nil), p.sites...)
	p.mu.Unlock()
	return &LoadResult{Sites: sites, Node: node, Sub: sub}, true, nil
}

func readTVBoxDocument(ctx context.Context, client *http.Client, sub Subscription, dir string, useCache bool) ([]byte, string, *tvbox.Config, error) {
	if useCache {
		body, final, err := readTVBoxCache(dir)
		if err != nil {
			return nil, "", nil, err
		}
		cfg, err := tvbox.Parse(body, final)
		return body, final, cfg, err
	}
	body, final, err := tvbox.FetchContext(ctx, client, strings.TrimSpace(sub.URL), 8<<20)
	if err != nil {
		return nil, "", nil, err
	}
	cfg, err := tvbox.Parse(body, final)
	return body, final, cfg, err
}

func followDepot(ctx context.Context, client *http.Client, cfg *tvbox.Config, body []byte, final string, path []string, depth int) (*tvbox.Config, []byte, string, *tvbox.Config, error) {
	if cfg == nil || !cfg.Depot() {
		return cfg, body, final, nil, nil
	}
	if depth >= 4 {
		return nil, nil, "", nil, errors.New("仓库嵌套超过 4 层")
	}
	if len(path) == 0 {
		return nil, nil, "", cfg, nil
	}
	var chosen *tvbox.RepoLine
	for i := range cfg.Lines {
		if cfg.Lines[i].Name == path[0] || cfg.Lines[i].URL == path[0] {
			chosen = &cfg.Lines[i]
			break
		}
	}
	if chosen == nil {
		return nil, nil, "", nil, fmt.Errorf("没有名为「%s」的仓库线路", path[0])
	}
	nextBody, nextURL, err := tvbox.FetchContext(ctx, client, chosen.URL, 8<<20)
	if err != nil {
		return nil, nil, "", nil, err
	}
	next, err := tvbox.Parse(nextBody, nextURL)
	if err != nil {
		return nil, nil, "", nil, err
	}
	return followDepot(ctx, client, next, nextBody, nextURL, path[1:], depth+1)
}

func (p *Player) needTVBoxTrust(sub Subscription, dir, hash, node string) (*LoadResult, bool, error) {
	entered := strings.TrimSpace(sub.URL)
	pinned, pinErr := readPin(dir)
	if pinErr != nil && !errors.Is(pinErr, os.ErrNotExist) {
		return nil, false, fmt.Errorf("读取配置 SHA-256 信任记录失败：%w", pinErr)
	}
	if pinErr != nil || pinned.URL != entered || !strings.EqualFold(pinned.BundleSHA256, hash) {
		p.mu.Lock()
		p.pendingTrust[sub.ID] = sourcePin{URL: entered, SHA256: hash, BundleSHA256: hash}
		p.mu.Unlock()
		return &LoadResult{Sub: sub, TrustRequired: true, TrustSHA256: hash, TrustURL: displaySourceURL(entered), Node: node}, true, nil
	}
	return nil, false, nil
}

func (p *Player) activateTVBox(ctx context.Context, s Settings, sub Subscription, dir string, generation uint64, cfg *tvbox.Config, body []byte, final, node string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	srv := tvbox.NewServer(cfg, tvbox.Options{
		Node: node, Dir: filepath.Join(dir, "runtime"), Log: p.log, InheritEnv: s.InheritNodeEnv,
	})
	if err := srv.Start(); err != nil {
		return err
	}
	p.mu.Lock()
	if generation != p.sourceGeneration || ctx.Err() != nil {
		p.mu.Unlock()
		srv.Stop()
		return context.Canceled
	}
	if err := writeTVBoxCache(dir, body, final); err != nil {
		p.mu.Unlock()
		srv.Stop()
		return err
	}
	p.applyTVBoxMedia(cfg)
	p.host, p.vodConfig, p.sites, p.inited = srv, cfg, srv.Sites(), map[string]bool{}
	sites := append([]catvod.Site(nil), p.sites...)
	p.mu.Unlock()
	usable := 0
	for _, site := range sites {
		if !site.Unsupported {
			usable++
		}
	}
	p.log(fmt.Sprintf("配置订阅已加载：%d 个站点可打开，%d 个仅展示", usable, len(sites)-usable))
	return nil
}

func (p *Player) applyTVBoxMedia(cfg *tvbox.Config) {
	if p.proxy == nil || cfg == nil {
		return
	}
	var client *http.Client
	if len(cfg.Hosts) > 0 {
		base := tvbox.HTTPClient(cfg.Hosts, 0)
		client = &http.Client{Transport: base.Transport}
	}
	var rewrite func(string, []byte) []byte
	if len(cfg.Rules) > 0 {
		rules := append([]tvbox.Rule(nil), cfg.Rules...)
		rewrite = func(host string, body []byte) []byte {
			return tvbox.ApplyPlaylistRules(rules, host, body)
		}
	}
	p.proxy.SetUpstream(client, rewrite)
}

// SelectRepoLine appends one repository line and reloads. An empty name
// clears the path so the picker starts again.
func (p *Player) SelectRepoLine(name string) (*LoadResult, error) {
	name = strings.TrimSpace(name)
	current := p.GetSettings()
	sub, ok := current.active()
	if !ok {
		return nil, errors.New("没有活动订阅")
	}
	next := ""
	if name != "" {
		next = strings.Join(append(splitRepoLine(sub.Line), name), repoLineSep)
	}
	if _, err := p.editSubs(func(s *Settings) error {
		i := s.find(sub.ID)
		if i < 0 {
			return errNoSub
		}
		s.Subscriptions[i].Line = next
		return nil
	}); err != nil {
		return nil, err
	}
	return p.LoadSource()
}

func tvboxHTTPClient(allowLAN bool) *http.Client {
	return &http.Client{
		Timeout: 60 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("配置重定向过多")
			}
			if !allowLAN && catvod.IsLANAddressURL(req.URL.String()) {
				return errors.New("配置重定向到了局域网地址")
			}
			return nil
		},
	}
}

type tvboxCacheMeta struct {
	Final string `json:"final"`
}

func tvboxCachePaths(dir string) (string, string) {
	base := filepath.Join(dir, "tvbox")
	return filepath.Join(base, "config.body"), filepath.Join(base, "config.meta.json")
}

func writeTVBoxCache(dir string, body []byte, final string) error {
	bodyPath, metaPath := tvboxCachePaths(dir)
	if err := os.MkdirAll(filepath.Dir(bodyPath), 0o700); err != nil {
		return err
	}
	meta, err := json.Marshal(tvboxCacheMeta{Final: final})
	if err != nil {
		return err
	}
	if err := atomicfile.Write(bodyPath, body, 0o600); err != nil {
		return err
	}
	return atomicfile.Write(metaPath, meta, 0o600)
}

func readTVBoxCache(dir string) ([]byte, string, error) {
	bodyPath, metaPath := tvboxCachePaths(dir)
	body, err := os.ReadFile(bodyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", errNoTVBoxCache
		}
		return nil, "", err
	}
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, "", errors.New("配置缓存不完整，请刷新订阅")
	}
	var meta tvboxCacheMeta
	if json.Unmarshal(raw, &meta) != nil || strings.TrimSpace(meta.Final) == "" {
		return nil, "", errors.New("配置缓存不完整，请刷新订阅")
	}
	return body, meta.Final, nil
}

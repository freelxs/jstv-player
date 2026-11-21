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
	u, _ := url.Parse(strings.TrimSpace(sub.URL))
	allowedLoopback := catvod.AllowedLoopbackAddr(u)
	client := tvboxHTTPClient(s.AllowLANMedia, allowedLoopback)
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
	if useCache {
		if err := tvbox.VerifyCachedScripts(resolved); err != nil {
			return nil, true, err
		}
	} else {
		scriptClient := tvbox.RestrictedHTTPClient(resolved.Hosts, 60*time.Second, s.AllowLANMedia, allowedLoopback)
		if err := tvbox.PrefetchScripts(ctx, scriptClient, resolved); err != nil {
			return nil, true, err
		}
	}
	node, nerr := catvod.FindNode(s.NodePath)
	if nerr != nil {
		if strings.TrimSpace(s.NodePath) != "" {
			return nil, true, nerr
		}
		node = ""
	}
	configHash, scriptHash, bundleHash := tvboxBundleHashes(body, final, resolved)
	if res, stop, err := p.needTVBoxTrust(sub, dir, final, configHash, scriptHash, bundleHash, node); stop || err != nil {
		return res, true, err
	}
	if err := p.activateTVBox(ctx, s, sub, dir, generation, resolved, body, final, node, allowedLoopback); err != nil {
		return nil, true, err
	}
	p.mu.Lock()
	sites := append([]catvod.Site(nil), p.sites...)
	p.mu.Unlock()
	return &LoadResult{Sites: sites, Node: node, Sub: sub}, true, nil
}

func readTVBoxDocument(ctx context.Context, client *http.Client, sub Subscription, dir string, useCache bool) ([]byte, string, *tvbox.Config, error) {
	if useCache {
		body, final, scripts, err := readTVBoxCache(dir)
		if err != nil {
			return nil, "", nil, err
		}
		cfg, err := tvbox.Parse(body, final)
		if cfg != nil && len(scripts) > 0 {
			cfg.Scripts = scripts
		}
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

func tvboxBundleHashes(body []byte, final string, cfg *tvbox.Config) (configHash, scriptHash, bundleHash string) {
	sum := sha256.Sum256(body)
	configHash = hex.EncodeToString(sum[:])
	if cfg != nil {
		scriptHash = cfg.ScriptSHA256
	}
	if scriptHash == "" {
		return configHash, "", configHash
	}
	b := sha256.Sum256([]byte("jstv-tvbox-bundle-v1\nfinal:" + strings.TrimSpace(final) + "\nconfig:" + configHash + "\nscripts:" + scriptHash + "\n"))
	return configHash, scriptHash, hex.EncodeToString(b[:])
}

func (p *Player) needTVBoxTrust(sub Subscription, dir, final, configHash, scriptHash, bundleHash, node string) (*LoadResult, bool, error) {
	entered := strings.TrimSpace(sub.URL)
	pinned, pinErr := readPin(dir)
	if pinErr != nil && !errors.Is(pinErr, os.ErrNotExist) {
		return nil, false, fmt.Errorf("读取配置 SHA-256 信任记录失败：%w", pinErr)
	}
	if pinErr != nil || pinned.URL != entered || !strings.EqualFold(pinned.BundleSHA256, bundleHash) {
		pin := sourcePin{URL: entered, SHA256: configHash, BundleSHA256: bundleHash}
		if scriptHash != "" {
			pin.ConfigSHA256 = scriptHash
		}
		p.mu.Lock()
		p.pendingTrust[sub.ID] = pin
		p.mu.Unlock()
		trustURL := displaySourceURL(entered)
		if strings.TrimSpace(final) != "" && strings.TrimSpace(final) != entered {
			if finalURL := displaySourceURL(final); finalURL != "" && finalURL != trustURL {
				trustURL = trustURL + " → " + finalURL
			}
		}
		res := &LoadResult{Sub: sub, TrustRequired: true, TrustSHA256: bundleHash, TrustURL: trustURL, Node: node}
		if scriptHash != "" {
			res.TrustScriptSHA256 = scriptHash
			res.TrustConfigSHA256 = configHash
		}
		return res, true, nil
	}
	return nil, false, nil
}

func (p *Player) activateTVBox(ctx context.Context, s Settings, sub Subscription, dir string, generation uint64, cfg *tvbox.Config, body []byte, final, node, allowedLoopback string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	srv := tvbox.NewServer(cfg, tvbox.Options{
		Node:                node,
		Dir:                 filepath.Join(dir, "runtime"),
		Log:                 p.log,
		InheritEnv:          s.InheritNodeEnv,
		AllowLAN:            s.AllowLANMedia,
		RestrictLAN:         true,
		AllowedLoopbackAddr: allowedLoopback,
		PinnedScripts:       cfg.Scripts,
		LockScripts:         true,
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
	if err := writeTVBoxCache(dir, body, final, cfg.Scripts); err != nil {
		p.mu.Unlock()
		srv.Stop()
		return err
	}
	p.applyTVBoxMedia(cfg, s.AllowLANMedia, allowedLoopback)
	p.host, p.vodConfig, p.sites, p.inited = srv, cfg, srv.Sites(), map[string]bool{}
	sites := append([]catvod.Site(nil), p.sites...)
	p.mu.Unlock()
	p.setKeepRunning(true)
	usable := 0
	for _, site := range sites {
		if !site.Unsupported {
			usable++
		}
	}
	p.log(fmt.Sprintf("配置订阅已加载：%d 个站点可打开，%d 个仅展示", usable, len(sites)-usable))
	return nil
}

func (p *Player) applyTVBoxMedia(cfg *tvbox.Config, allowLAN bool, allowedLoopback string) {
	if p.proxy == nil || cfg == nil {
		return
	}
	var client *http.Client
	var dialRewrite func(string) string
	if len(cfg.Hosts) > 0 {
		hosts := make(map[string]string, len(cfg.Hosts))
		for k, v := range cfg.Hosts {
			hosts[k] = v
		}
		base := tvbox.RestrictedHTTPClient(hosts, 0, allowLAN, allowedLoopback)
		client = &http.Client{Transport: base.Transport}
		dialRewrite = func(addr string) string {
			return tvbox.RewriteDial(addr, hosts)
		}
	}
	var rewrite func(string, []byte) []byte
	if len(cfg.Rules) > 0 {
		rules := append([]tvbox.Rule(nil), cfg.Rules...)
		rewrite = func(host string, body []byte) []byte {
			return tvbox.ApplyPlaylistRules(rules, host, body)
		}
	}
	p.proxy.SetUpstream(client, rewrite)
	if dialRewrite != nil {
		p.proxy.SetDialRewrite(dialRewrite)
	}
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

func tvboxHTTPClient(allowLAN bool, allowedLoopback string) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if !allowLAN {
		tr.Proxy = nil
	}
	tr.DialContext = catvod.RestrictedDialContext(allowLAN, allowedLoopback, nil)
	return &http.Client{
		Timeout:   60 * time.Second,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return errors.New("配置重定向过多")
			}
			if !allowLAN && catvod.AllowedLoopbackAddr(req.URL) != allowedLoopback && (catvod.IsLANAddressURL(req.URL.String()) || catvod.IsLocalNetworkURL(req.URL.String())) {
				return errors.New("配置重定向到了局域网地址")
			}
			return nil
		},
	}
}

type tvboxCacheMeta struct {
	Final   string            `json:"final"`
	Scripts map[string][]byte `json:"scripts,omitempty"`
}

func tvboxCachePaths(dir string) (string, string) {
	base := filepath.Join(dir, "tvbox")
	return filepath.Join(base, "config.body"), filepath.Join(base, "config.meta.json")
}

func writeTVBoxCache(dir string, body []byte, final string, scripts map[string][]byte) error {
	bodyPath, metaPath := tvboxCachePaths(dir)
	if err := os.MkdirAll(filepath.Dir(bodyPath), 0o700); err != nil {
		return err
	}
	meta, err := json.Marshal(tvboxCacheMeta{Final: final, Scripts: scripts})
	if err != nil {
		return err
	}
	if err := atomicfile.Write(bodyPath, body, 0o600); err != nil {
		return err
	}
	return atomicfile.Write(metaPath, meta, 0o600)
}

func readTVBoxCache(dir string) ([]byte, string, map[string][]byte, error) {
	bodyPath, metaPath := tvboxCachePaths(dir)
	body, err := os.ReadFile(bodyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, "", nil, errNoTVBoxCache
		}
		return nil, "", nil, err
	}
	raw, err := os.ReadFile(metaPath)
	if err != nil {
		return nil, "", nil, errors.New("配置缓存不完整，请刷新订阅")
	}
	var meta tvboxCacheMeta
	if json.Unmarshal(raw, &meta) != nil || strings.TrimSpace(meta.Final) == "" {
		return nil, "", nil, errors.New("配置缓存不完整，请刷新订阅")
	}
	return body, meta.Final, meta.Scripts, nil
}

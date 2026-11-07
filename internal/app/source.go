package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/freelxs/jstv-player/internal/catvod"
)

func (p *Player) startBridge(tpl string) {
	tpl = strings.TrimSpace(tpl)
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bridge != nil {
		p.bridge.Template = tpl
		return
	}
	if tpl == "" {
		return
	}
	b := &catvod.Bridge{Template: tpl}
	if err := b.Start(catvod.DefaultBridgePort); err == nil {
		p.bridge = b
	}
}

func (p *Player) bridgeAddr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.bridge == nil || !p.bridge.Enabled() {
		return ""
	}
	return p.bridge.Addr()
}

// LoadSource downloads the active subscription's plugin, starts it and
// lists its sites. Any previous plugin process is stopped first.
func (p *Player) LoadSource() (*LoadResult, error)         { return p.loadSource(false) }
func (p *Player) ForceRefreshSource() (*LoadResult, error) { return p.loadSource(false, true) }

// StartCachedSource starts a verified, approved last working copy. Refreshing
// remains an explicit LoadSource operation and always checks configuration.
func (p *Player) StartCachedSource() (*LoadResult, error) { return p.loadSource(true) }

func (p *Player) loadSource(useCache bool, force ...bool) (*LoadResult, error) {
	ctx, generation := p.beginSourceLoad()
	p.loadMu.Lock()
	defer p.loadMu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s := p.GetSettings()
	sub, ok := s.active()
	if !ok {
		return nil, errors.New("请先添加一个订阅")
	}
	// Each subscription keeps its own plugin copy and data files.
	dir := filepath.Join(p.dir, "subs", sub.ID)
	if isLiveSubscription(sub) {
		return p.loadLive(ctx, sub, dir, generation, useCache)
	}
	node, err := catvod.FindNode(s.NodePath)
	if err != nil {
		return nil, err
	}
	p.log("加载订阅「" + sub.Name + "」: " + sub.URL)
	var src *catvod.Source
	if useCache {
		src, err = catvod.CachedSource(dir)
		if errors.Is(err, os.ErrNotExist) {
			src = nil
			err = nil
		}
		if err != nil {
			return nil, err
		}
		if src != nil && src.URL != sub.URL {
			src = nil
		}
	}
	if src == nil {
		src, err = catvod.FetchRefreshContext(ctx, nil, sub.URL, dir, s.StrictMD5, len(force) > 0 && force[0])
	}
	if err != nil {
		return nil, err
	}
	pinned, pinErr := readPin(dir)
	if pinErr != nil && !errors.Is(pinErr, os.ErrNotExist) {
		return nil, fmt.Errorf("读取插件 SHA-256 信任记录失败：%w", pinErr)
	}
	if s.StrictMD5 && src.MD5Mismatch() {
		return nil, errors.New("md5 校验失败，缓存版本与发布摘要不一致")
	}
	if pinErr != nil || pinned.URL != src.ScriptURL || pinned.BundleSHA256 != src.BundleSHA256 {
		p.mu.Lock()
		if generation != p.sourceGeneration || ctx.Err() != nil {
			p.mu.Unlock()
			return nil, context.Canceled
		}
		p.pendingTrust[sub.ID] = sourcePin{URL: src.ScriptURL, SHA256: src.SHA256, ConfigSHA256: src.ConfigSHA256, BundleSHA256: src.BundleSHA256}
		p.mu.Unlock()
		return &LoadResult{Sub: sub, TrustRequired: true, TrustSHA256: src.BundleSHA256, TrustScriptSHA256: src.SHA256, TrustConfigSHA256: src.ConfigSHA256, TrustURL: displaySourceURL(src.ScriptURL), MD5: src.MD5, ExpectedMD5: src.Expected, Mismatch: src.MD5Mismatch(), Node: node}, nil
	}
	if src.MD5Mismatch() {
		p.log(fmt.Sprintf("md5 不一致: 文件 %s, 发布 %s", src.MD5, src.Expected))
	}
	if err := src.Verify(); err != nil {
		return nil, err
	}
	h := &catvod.Host{Node: node, Dir: filepath.Join(dir, "runtime"), Script: src.Path, ConfigFile: src.ConfigPath, LogFunc: p.log, ListenLAN: s.AllowLAN, InheritEnv: s.InheritNodeEnv, InheritProxyEnv: s.InheritProxyEnv}
	if err := h.StartContext(ctx, 90*time.Second); err != nil {
		return nil, err
	}
	cfg, err := (&catvod.Client{Base: h.Base(), Context: ctx, LogFunc: p.log}).Config()
	if err != nil {
		h.Stop()
		return nil, err
	}
	p.mu.Lock()
	if generation != p.sourceGeneration || ctx.Err() != nil {
		p.mu.Unlock()
		h.Stop()
		return nil, context.Canceled
	}
	if err := catvod.PublishSource(dir, src); err != nil {
		p.mu.Unlock()
		h.Stop()
		return nil, err
	}
	p.host, p.sites, p.inited = h, cfg.Video.Sites, map[string]bool{}
	p.mu.Unlock()
	return &LoadResult{Sites: cfg.Video.Sites, MD5: src.MD5, ExpectedMD5: src.Expected, Mismatch: src.MD5Mismatch(), Node: node, Sub: sub}, nil
}

// ApproveSource records the exact currently active URL and hash presented by
// the UI. It never starts the bundle; the caller must explicitly reload.
func (p *Player) ApproveSource() error {
	s := p.GetSettings()
	sub, ok := s.active()
	if !ok {
		return errors.New("没有活动订阅")
	}
	p.mu.Lock()
	pending, ok := p.pendingTrust[sub.ID]
	p.mu.Unlock()
	if !ok {
		return errors.New("没有待确认的插件版本")
	}
	scriptURL, _ := catvod.ScriptURLs(sub.URL)
	if scriptURL != pending.URL {
		return errors.New("订阅地址已更改，请重新加载并确认新的版本")
	}
	if err := writePin(filepath.Join(p.dir, "subs", sub.ID), pending); err != nil {
		return err
	}
	p.mu.Lock()
	if cur, still := p.pendingTrust[sub.ID]; still && cur == pending {
		delete(p.pendingTrust, sub.ID)
	}
	p.mu.Unlock()
	return nil
}

// beginSourceLoad invalidates old work before waiting for loadMu. Cancellation
// reaches downloads and unpublished child processes as well as current hosts.
func (p *Player) beginSourceLoad() (context.Context, uint64) {
	p.mu.Lock()
	h := p.clearSourceLocked()
	ctx, cancel := context.WithCancel(context.Background())
	p.loadCancel = cancel
	p.sourceContext = ctx
	generation := p.sourceGeneration
	p.mu.Unlock()
	if h != nil {
		h.Stop()
	}
	return ctx, generation
}

// clearSourceLocked and playback authorization commits use the same mutex, so
// a stale play response cannot re-grant a resource after a stop has revoked it.
func (p *Player) clearSourceLocked() *catvod.Host {
	if p.loadCancel != nil {
		p.loadCancel()
		p.loadCancel = nil
	}
	p.sourceGeneration++
	p.invalidateCatalogLocked()
	h := p.host
	p.host, p.sites, p.inited = nil, nil, map[string]bool{}
	p.liveCatalog, p.liveAPI = nil, ""
	p.progress = nil
	p.eps.mu.Lock()
	p.eps.lists, p.eps.order = nil, nil
	p.eps.mu.Unlock()
	if p.proxy != nil {
		p.proxy.Reset()
	}
	return h
}

func (p *Player) StopSource() {
	p.mu.Lock()
	h := p.clearSourceLocked()
	p.mu.Unlock()
	if h != nil {
		h.Stop()
	}
}

// ProbeNodePermissions reports which permission-related flags are advertised
// by the selected Node binary. It does not enable them or claim sandboxing.
func (p *Player) ProbeNodePermissions() (string, error) {
	node, err := catvod.FindNode(p.GetSettings().NodePath)
	if err != nil {
		return "", err
	}
	out, err := exec.Command(node, "--help").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("读取 Node 能力失败：%w", err)
	}
	flags := nodePermissionFlags(string(out))
	if len(flags) == 0 {
		return "当前 Node 未在 --help 中列出权限模型标志；未启用任何限制。", nil
	}
	return "当前 Node 声明支持：" + strings.Join(flags, ", ") + "。这里只检测标志，不代表隔离有效；未启用权限限制。", nil
}

func nodePermissionFlags(help string) []string {
	var flags []string
	for _, flag := range []string{"--permission", "--experimental-permission"} {
		if strings.Contains(help, flag) {
			flags = append(flags, flag)
		}
	}
	return flags
}

var errSourceChanged = errors.New("订阅已切换，请重新选择内容")

func (p *Player) generation() uint64         { p.mu.Lock(); defer p.mu.Unlock(); return p.sourceGeneration }
func (p *Player) isGeneration(g uint64) bool { return p.generation() == g }

func (p *Player) addPlaylist(pl *playlist) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if pl.generation != p.sourceGeneration {
		return "", errSourceChanged
	}
	return p.eps.add(pl), nil
}

func (p *Player) playbackState() (Settings, uint64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.settings, p.sourceGeneration
}

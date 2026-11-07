package app

import (
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
func (p *Player) LoadSource() (*LoadResult, error) {
	p.loadMu.Lock()
	defer p.loadMu.Unlock()
	s := p.GetSettings()
	sub, ok := s.active()
	if !ok {
		p.StopSource()
		return nil, errors.New("请先添加一个订阅")
	}
	// Each subscription keeps its own plugin copy and data files.
	dir := filepath.Join(p.dir, "subs", sub.ID)
	node, err := catvod.FindNode(s.NodePath)
	if err != nil {
		return nil, err
	}
	p.log("加载订阅「" + sub.Name + "」: " + sub.URL)
	src, err := catvod.FetchWithOptions(nil, sub.URL, dir, s.StrictMD5)
	if err != nil {
		return nil, err
	}
	pinned, pinErr := readPin(dir)
	if pinErr != nil && !errors.Is(pinErr, os.ErrNotExist) {
		return nil, fmt.Errorf("读取插件 SHA-256 信任记录失败：%w", pinErr)
	}
	if pinErr != nil || pinned.URL != src.ScriptURL || !strings.EqualFold(pinned.SHA256, src.SHA256) {
		p.StopSource()
		p.mu.Lock()
		p.pendingTrust[sub.ID] = sourcePin{URL: src.ScriptURL, SHA256: src.SHA256}
		p.mu.Unlock()
		return &LoadResult{Sub: sub, TrustRequired: true, TrustSHA256: src.SHA256, TrustURL: displaySourceURL(src.ScriptURL), MD5: src.MD5, ExpectedMD5: src.Expected, Mismatch: src.MD5Mismatch(), Node: node}, nil
	}
	if src.MD5Mismatch() {
		p.log(fmt.Sprintf("md5 不一致: 文件 %s, 发布 %s", src.MD5, src.Expected))
	}
	p.StopSource()
	h := &catvod.Host{Node: node, Dir: filepath.Join(dir, "runtime"), Script: src.Path, ConfigFile: src.ConfigPath, LogFunc: p.log, ListenLAN: s.AllowLAN, InheritEnv: s.InheritNodeEnv}
	if err := h.Start(90 * time.Second); err != nil {
		return nil, err
	}
	cfg, err := (&catvod.Client{Base: h.Base()}).Config()
	if err != nil {
		h.Stop()
		return nil, err
	}
	p.mu.Lock()
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

// StopSource stops the running source process.
func (p *Player) StopSource() {
	p.mu.Lock()
	h := p.host
	p.host, p.sites, p.inited = nil, nil, map[string]bool{}
	p.mu.Unlock()
	if h != nil {
		h.Stop()
	}
	if p.proxy != nil {
		p.proxy.Reset()
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

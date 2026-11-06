package main

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/freelxs/jstv-player/internal/catvod"
	"github.com/freelxs/jstv-player/internal/tvbox"
)

func subscriptionShape(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	path := strings.ToLower(strings.TrimSpace(raw))
	if err == nil && u.Path != "" {
		path = strings.ToLower(u.Path)
	}
	path = strings.Split(path, "?")[0]
	switch {
	case strings.HasSuffix(path, ".json"), strings.HasSuffix(path, ".m3u"), strings.HasSuffix(path, ".m3u8"), strings.HasSuffix(path, ".txt"):
		return "tvbox"
	case strings.HasSuffix(path, ".js"), strings.HasSuffix(path, ".mjs"), strings.HasSuffix(path, ".cjs"), strings.HasSuffix(path, ".md5"):
		return "plugin"
	default:
		return "sniff"
	}
}

func (p *Player) loadPlugin(s Settings, sub Subscription) (*LoadResult, error) {
	dir := filepath.Join(p.dir, "subs", sub.ID)
	node, err := catvod.FindNode(s.NodePath)
	if err != nil {
		return nil, err
	}
	src, err := catvod.FetchWithOptions(nil, sub.URL, dir, s.StrictMD5)
	if err != nil {
		return nil, err
	}
	if res, stop, err := p.needTrust(sub, dir, src.ScriptURL, src.SHA256, src.MD5, src.Expected, src.MD5Mismatch(), node); stop || err != nil {
		return res, err
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

func (p *Player) loadTVBox(s Settings, sub Subscription) (*LoadResult, error) {
	body, finalURL, err := tvbox.Fetch(nil, sub.URL, 8<<20)
	if err != nil {
		return nil, err
	}
	return p.activateTVBox(s, sub, body, finalURL)
}

func (p *Player) activateTVBox(s Settings, sub Subscription, body []byte, finalURL string) (*LoadResult, error) {
	cfg, err := tvbox.Parse(body, finalURL)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	hash := hex.EncodeToString(sum[:])
	digest := md5.Sum(body)
	md5sum := hex.EncodeToString(digest[:])
	dir := filepath.Join(p.dir, "subs", sub.ID)
	node, _ := catvod.FindNode(s.NodePath)
	if res, stop, err := p.needTrust(sub, dir, strings.TrimSpace(sub.URL), hash, md5sum, "", false, node); stop || err != nil {
		return res, err
	}
	p.StopSource()
	srv := tvbox.NewServer(cfg, tvbox.Options{
		Node: node, Dir: filepath.Join(dir, "tvbox"), Log: p.log, InheritEnv: s.InheritNodeEnv,
	})
	if err := srv.Start(); err != nil {
		return nil, err
	}
	p.applyTVBoxMedia(cfg)
	sites := srv.Sites()
	p.mu.Lock()
	p.host, p.sites, p.inited = srv, sites, map[string]bool{}
	p.mu.Unlock()
	usable := 0
	for _, site := range sites {
		if !site.Unsupported {
			usable++
		}
	}
	p.log(fmt.Sprintf("配置订阅已加载：%d 个站点可打开，%d 个仅展示", usable, len(sites)-usable))
	return &LoadResult{Sites: sites, MD5: md5sum, Node: node, Sub: sub}, nil
}

func (p *Player) needTrust(sub Subscription, dir, pinURL, hash, md5sum, expected string, mismatch bool, node string) (*LoadResult, bool, error) {
	pinned, pinErr := readPin(dir)
	if pinErr != nil && !errors.Is(pinErr, os.ErrNotExist) {
		return nil, false, fmt.Errorf("读取插件 SHA-256 信任记录失败：%w", pinErr)
	}
	if pinErr != nil || pinned.URL != pinURL || !strings.EqualFold(pinned.SHA256, hash) {
		p.StopSource()
		p.mu.Lock()
		p.pendingTrust[sub.ID] = sourcePin{URL: pinURL, SHA256: hash}
		p.mu.Unlock()
		return &LoadResult{Sub: sub, TrustRequired: true, TrustSHA256: hash, TrustURL: displaySourceURL(pinURL), MD5: md5sum, ExpectedMD5: expected, Mismatch: mismatch, Node: node}, true, nil
	}
	return nil, false, nil
}

func (p *Player) applyTVBoxMedia(cfg *tvbox.Config) {
	if p.proxy == nil || cfg == nil {
		return
	}
	var client *http.Client
	if len(cfg.Hosts) > 0 {
		client = &http.Client{Transport: tvbox.HTTPClient(cfg.Hosts, 0).Transport}
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

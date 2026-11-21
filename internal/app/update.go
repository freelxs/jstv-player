package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

const (
	githubRepoSlug       = "freelxs/jstv-player"
	defaultUpdateAPIURL  = "https://api.github.com/repos/" + githubRepoSlug + "/releases/latest"
	githubReleasesPrefix = "https://github.com/" + githubRepoSlug + "/releases/"
)

// UpdateInfo describes the result of checking for a newer application version.
type UpdateInfo struct {
	CurrentVersion string `json:"currentVersion"`
	LatestVersion  string `json:"latestVersion,omitempty"`
	Available      bool   `json:"available"`
	CanAutoInstall bool   `json:"canAutoInstall"`
	ReleaseURL     string `json:"releaseUrl,omitempty"`
	DownloadURL    string `json:"downloadUrl,omitempty"`
	AssetName      string `json:"assetName,omitempty"`
	Notes          string `json:"notes,omitempty"`
	PublishedAt    string `json:"publishedAt,omitempty"`
	Source         string `json:"source"`
}

type githubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

type githubReleaseResponse struct {
	TagName     string               `json:"tag_name"`
	HTMLURL     string               `json:"html_url"`
	Body        string               `json:"body"`
	PublishedAt string               `json:"published_at"`
	Draft       bool                 `json:"draft"`
	Prerelease  bool                 `json:"prerelease"`
	Assets      []githubReleaseAsset `json:"assets"`
}

func (p *Player) currentAppVersion() string {
	if p.appVersion != "" {
		return p.appVersion
	}
	if v := strings.TrimPrefix(strings.TrimSpace(mygo.App.Version()), "v"); v != "" {
		return v
	}
	return "0.0.0"
}

// CheckUpdate checks for a newer JSTV Player release. When the desktop build
// has MyGo's signed updater enabled, it checks the signed feed for one-click
// in-place installation; otherwise it checks the latest GitHub Release.
func (p *Player) CheckUpdate(ctx context.Context) (*UpdateInfo, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	current := p.currentAppVersion()
	if !p.webMode.Load() && p.desktop.UpdaterEnabled != nil && p.desktop.UpdaterCheck != nil && p.desktop.UpdaterEnabled() {
		upd, err := p.desktop.UpdaterCheck(ctx)
		if err != nil {
			return nil, fmt.Errorf("检查更新失败: %w", err)
		}
		p.updateMu.Lock()
		p.pendingUpdate = upd
		p.pendingUpdateURL = ""
		p.updateMu.Unlock()
		if upd == nil {
			return &UpdateInfo{
				CurrentVersion: current,
				LatestVersion:  current,
				Available:      false,
				CanAutoInstall: true,
				ReleaseURL:     githubReleasesPrefix + "latest",
				Source:         "mygo",
			}, nil
		}
		latest := strings.TrimPrefix(strings.TrimSpace(upd.Version), "v")
		published := ""
		if !upd.Date.IsZero() {
			published = upd.Date.UTC().Format(time.RFC3339)
		}
		return &UpdateInfo{
			CurrentVersion: current,
			LatestVersion:  latest,
			Available:      true,
			CanAutoInstall: true,
			ReleaseURL:     githubReleasesPrefix + "tag/v" + latest,
			Notes:          strings.TrimSpace(upd.Notes),
			PublishedAt:    published,
			Source:         "mygo",
		}, nil
	}
	return p.checkGitHubUpdate(ctx, current)
}

func (p *Player) checkGitHubUpdate(ctx context.Context, current string) (*UpdateInfo, error) {
	apiURL := p.updateAPIURL
	customEndpoint := apiURL != ""
	if apiURL == "" {
		apiURL = defaultUpdateAPIURL
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "JSTV-Player/"+current)
	client := p.updateHTTP
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("检查更新失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("检查更新失败: GitHub 返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读取更新信息失败: %w", err)
	}
	var rel githubReleaseResponse
	if err := json.Unmarshal(body, &rel); err != nil {
		return nil, fmt.Errorf("解析更新信息失败: %w", err)
	}
	latest := strings.TrimPrefix(strings.TrimSpace(rel.TagName), "v")
	if latest == "" {
		return nil, errors.New("未获取到最新版本号")
	}
	available := compareSemver(latest, current) > 0
	releaseURL := strings.TrimSpace(rel.HTMLURL)
	if !validUpdateURL(releaseURL, customEndpoint, apiURL) {
		releaseURL = githubReleasesPrefix + "tag/v" + latest
	}
	assetName, downloadURL := selectReleaseAsset(rel.Assets, runtime.GOOS, runtime.GOARCH, hasBundledNode())
	if !validUpdateURL(downloadURL, customEndpoint, apiURL) {
		assetName, downloadURL = "", ""
	}
	targetURL := downloadURL
	if targetURL == "" {
		targetURL = releaseURL
	}
	p.updateMu.Lock()
	p.pendingUpdate = nil
	if available {
		p.pendingUpdateURL = targetURL
	} else {
		p.pendingUpdateURL = ""
	}
	p.updateMu.Unlock()
	return &UpdateInfo{
		CurrentVersion: current,
		LatestVersion:  latest,
		Available:      available,
		CanAutoInstall: false,
		ReleaseURL:     releaseURL,
		DownloadURL:    downloadURL,
		AssetName:      assetName,
		Notes:          strings.TrimSpace(rel.Body),
		PublishedAt:    strings.TrimSpace(rel.PublishedAt),
		Source:         "github",
	}, nil
}

// InstallUpdate applies a pending signed MyGo update and relaunches the app, or
// opens/returns the verified GitHub Release installer URL when in-place updates
// are unavailable.
func (p *Player) InstallUpdate(ctx context.Context) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	p.updateMu.Lock()
	upd := p.pendingUpdate
	targetURL := p.pendingUpdateURL
	p.updateMu.Unlock()
	if !p.webMode.Load() && upd != nil {
		p.log("开始下载并安装更新: v" + strings.TrimPrefix(upd.Version, "v"))
		if err := upd.Install(ctx, nil); err != nil {
			return "", fmt.Errorf("安装更新失败: %w", err)
		}
		p.updateMu.Lock()
		p.pendingUpdate = nil
		p.updateMu.Unlock()
		p.StopSource()
		if p.desktop.Relaunch != nil {
			p.desktop.Relaunch()
		}
		return "installed", nil
	}
	if targetURL == "" {
		return "", errors.New("请先检查更新")
	}
	if !p.webMode.Load() {
		open := p.desktop.OpenExternal
		if open == nil {
			open = mygo.Shell.OpenExternal
		}
		if err := open(targetURL); err != nil {
			return "", fmt.Errorf("打开更新链接失败: %w", err)
		}
	}
	return targetURL, nil
}

func validUpdateURL(raw string, allowCustomOrigin bool, apiURL string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	if u.Scheme == "https" && strings.HasPrefix(u.String(), githubReleasesPrefix) {
		return true
	}
	if allowCustomOrigin {
		base, err := url.Parse(apiURL)
		if err == nil && u.Scheme == base.Scheme && strings.EqualFold(u.Host, base.Host) {
			return true
		}
	}
	return false
}

func hasBundledNode() bool {
	exe, err := os.Executable()
	if err != nil || exe == "" {
		return false
	}
	dir := filepath.Dir(exe)
	candidates := []string{
		filepath.Join(dir, "node", "bin", "node.exe"),
		filepath.Join(dir, "node", "bin", "node"),
		filepath.Join(dir, "..", "Resources", "node", runtime.GOARCH, "bin", "node"),
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return true
		}
	}
	return false
}

func selectReleaseAsset(assets []githubReleaseAsset, goos, goarch string, withNode bool) (name, dlURL string) {
	var preferred []func(string) bool
	switch goos {
	case "darwin":
		if withNode {
			preferred = []func(string) bool{
				func(n string) bool { return strings.HasSuffix(n, "-macos-universal-with-node.dmg") },
				func(n string) bool { return strings.HasSuffix(n, "-macos-universal-with-node.zip") },
				func(n string) bool { return strings.HasSuffix(n, "-macos-universal.dmg") },
			}
		} else {
			preferred = []func(string) bool{
				func(n string) bool { return strings.HasSuffix(n, "-macos-universal.dmg") && !strings.Contains(n, "-with-node") },
				func(n string) bool { return strings.HasSuffix(n, "-macos-universal.zip") && !strings.Contains(n, "-with-node") },
			}
		}
	case "windows":
		if withNode {
			preferred = []func(string) bool{
				func(n string) bool { return strings.HasSuffix(n, "-windows-"+goarch+"-with-node-setup.exe") },
				func(n string) bool { return strings.HasSuffix(n, "-windows-"+goarch+"-with-node.zip") },
				func(n string) bool { return strings.HasSuffix(n, "-windows-"+goarch+"-setup.exe") },
			}
		} else {
			preferred = []func(string) bool{
				func(n string) bool { return strings.HasSuffix(n, "-windows-"+goarch+"-setup.exe") && !strings.Contains(n, "-with-node") },
				func(n string) bool { return strings.HasSuffix(n, "-windows-"+goarch+".zip") && !strings.Contains(n, "-with-node") },
			}
		}
	case "linux":
		preferred = []func(string) bool{
			func(n string) bool { return strings.HasSuffix(n, "_"+goarch+".deb") },
			func(n string) bool { return strings.HasSuffix(n, ".rpm") },
			func(n string) bool { return strings.HasSuffix(n, "-linux-"+goarch+".tar.gz") },
		}
	}
	for _, match := range preferred {
		for _, asset := range assets {
			lower := strings.ToLower(strings.TrimSpace(asset.Name))
			if match(lower) && strings.TrimSpace(asset.BrowserDownloadURL) != "" {
				return asset.Name, strings.TrimSpace(asset.BrowserDownloadURL)
			}
		}
	}
	return "", ""
}

func compareSemver(a, b string) int {
	pa, preA := parseSemver(a)
	pb, preB := parseSemver(b)
	for i := 0; i < 3; i++ {
		if pa[i] != pb[i] {
			if pa[i] > pb[i] {
				return 1
			}
			return -1
		}
	}
	switch {
	case preA == "" && preB != "":
		return 1
	case preA != "" && preB == "":
		return -1
	case preA != preB:
		if preA > preB {
			return 1
		}
		return -1
	default:
		return 0
	}
}

func parseSemver(raw string) ([3]int, string) {
	s := strings.TrimPrefix(strings.TrimSpace(raw), "v")
	if plus := strings.IndexByte(s, '+'); plus >= 0 {
		s = s[:plus]
	}
	pre := ""
	if dash := strings.IndexByte(s, '-'); dash >= 0 {
		pre = s[dash+1:]
		s = s[:dash]
	}
	parts := strings.Split(s, ".")
	var nums [3]int
	for i := 0; i < 3 && i < len(parts); i++ {
		n, err := strconv.Atoi(parts[i])
		if err == nil && n >= 0 {
			nums[i] = n
		}
	}
	return nums, pre
}

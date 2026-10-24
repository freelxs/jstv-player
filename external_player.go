package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// senPlayerBundleID is the macOS bundle identifier published for SenPlayer
// (Mac App Store id 6443975850).
const senPlayerBundleID = "com.wuziqi.SenPlayer"

// playerUsesPlaylist reports whether continuous play should hand the player
// the local M3U. SenPlayer's documented play action takes one URL, and its
// M3U support is an IPTV subscription rather than an episode queue, so it
// plays the selected episode the same way the system player does.
func playerUsesPlaylist(player string) bool {
	switch player {
	case "system", "senplayer":
		return false
	default:
		return true
	}
}

func senPlayerBundleCandidates(home string) []string {
	c := []string{"/Applications/SenPlayer.app"}
	if home != "" {
		c = append(c, filepath.Join(home, "Applications", "SenPlayer.app"))
	}
	return c
}

func parseBundlePaths(out string) []string {
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			paths = append(paths, line)
		}
	}
	return paths
}

func bundleInfoPlist(path string) string {
	return filepath.Join(path, "Contents", "Info.plist")
}

func isLaunchableAppBundle(path string) bool {
	st, err := os.Stat(path)
	if err != nil || !st.IsDir() || !strings.EqualFold(filepath.Ext(path), ".app") {
		return false
	}
	info, err := os.Stat(bundleInfoPlist(path))
	return err == nil && info.Mode().IsRegular()
}

// chooseSenPlayer returns the first candidate that is an app bundle, then
// the first bundle reported by extra (Spotlight).
func chooseSenPlayer(candidates []string, extra func() []string) string {
	for _, path := range candidates {
		if isLaunchableAppBundle(path) {
			return path
		}
	}
	if extra == nil {
		return ""
	}
	for _, path := range extra() {
		if isLaunchableAppBundle(path) {
			return path
		}
	}
	return ""
}

func lookupSenPlayerBundles() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "mdfind", `kMDItemCFBundleIdentifier == "`+senPlayerBundleID+`"`)
	b, err := cmd.Output()
	if err != nil {
		return nil
	}
	return parseBundlePaths(string(b))
}

// findSenPlayer returns the SenPlayer app bundle, or "" when this is not
// macOS or SenPlayer is not installed.
func findSenPlayer() string {
	if runtime.GOOS != "darwin" {
		return ""
	}
	home, _ := os.UserHomeDir()
	return chooseSenPlayer(senPlayerBundleCandidates(home), lookupSenPlayerBundles)
}

func encodePlayerURL(raw string) string {
	return strings.ReplaceAll(url.QueryEscape(raw), "+", "%20")
}

// senPlayerOpenArgs is the argv for /usr/bin/open. The media address is one
// query value inside SenPlayer's x-callback-url play action, the scheme
// documented by SenPlayer's URL Scheme playback and used by other macOS
// apps. open is not a shell, so the URL is not interpolated.
func senPlayerOpenArgs(media string) []string {
	scheme := "senplayer://x-callback-url/play?url=" + encodePlayerURL(media)
	return []string{"-b", senPlayerBundleID, scheme}
}

const (
	customPlayerURLToken = "{url}"
	maxCustomPlayerArgs  = 32
	maxCustomPlayerText  = 4096
)

func splitPlayerArgs(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if len(s) > maxCustomPlayerText {
		return nil, errors.New("附加参数过长")
	}
	var args []string
	var b strings.Builder
	var quote rune
	flush := func() {
		if b.Len() == 0 {
			return
		}
		args = append(args, b.String())
		b.Reset()
	}
	for _, r := range s {
		if r == 0 {
			return nil, errors.New("附加参数无效")
		}
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	if quote != 0 {
		return nil, errors.New("附加参数的引号没有闭合")
	}
	flush()
	if len(args) > maxCustomPlayerArgs {
		return nil, errors.New("附加参数过多")
	}
	return args, nil
}

// customPlayerArgv splits extra the way a command line splits quotes, without
// running a shell. {url} in an argument is replaced by media. When no
// argument contains {url}, media is appended as its own argument.
func customPlayerArgv(extra, media string) ([]string, error) {
	if media == "" || strings.ContainsRune(media, 0) {
		return nil, errors.New("播放地址无效")
	}
	parts, err := splitPlayerArgs(extra)
	if err != nil {
		return nil, err
	}
	used := false
	for i, part := range parts {
		if strings.Contains(part, customPlayerURLToken) {
			parts[i] = strings.ReplaceAll(part, customPlayerURLToken, media)
			used = true
		}
	}
	if !used {
		parts = append(parts, media)
	}
	return parts, nil
}

func validateCustomPlayer(path string) error {
	return validateCustomPlayerOS(runtime.GOOS, path)
}

func validateCustomPlayerOS(goos, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("请先选择自定义播放器")
	}
	if strings.ContainsRune(path, 0) || !filepath.IsAbs(path) {
		return errors.New("请使用播放器的完整路径")
	}
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("找不到播放器：%s", path)
	}
	if strings.EqualFold(filepath.Ext(path), ".app") {
		if goos != "darwin" {
			return errors.New("macOS 应用程序包只能在 macOS 上启动")
		}
		if !st.IsDir() || !isLaunchableAppBundle(path) {
			return errors.New("应用程序包不完整，缺少 Contents/Info.plist")
		}
		return nil
	}
	if st.IsDir() || !st.Mode().IsRegular() {
		return errors.New("请选择播放器程序，而不是文件夹")
	}
	switch goos {
	case "windows":
		if !strings.EqualFold(filepath.Ext(path), ".exe") {
			return errors.New("请选择 .exe 程序")
		}
	default:
		if st.Mode()&0o111 == 0 {
			return errors.New("该文件没有可执行权限")
		}
	}
	return nil
}

// customLaunchPlan is the executable and argv for a custom player. A macOS
// .app with no extra arguments is opened as a document so the app receives
// the URL through Launch Services. Extra arguments, including {url}, are
// passed with open --args or directly to an executable. Nothing is joined
// into a shell command.
func customLaunchPlan(goos, path, extra, media string) (string, []string, error) {
	path = filepath.Clean(strings.TrimSpace(path))
	if err := validateCustomPlayerOS(goos, path); err != nil {
		return "", nil, err
	}
	extra = strings.TrimSpace(extra)
	argv, err := customPlayerArgv(extra, media)
	if err != nil {
		return "", nil, err
	}
	if goos == "darwin" && strings.EqualFold(filepath.Ext(path), ".app") {
		if extra == "" {
			return "open", []string{"-a", path, media}, nil
		}
		return "open", append([]string{"-a", path, "--args"}, argv...), nil
	}
	return path, argv, nil
}

func customLaunch(path, extra, media string) (string, []string, error) {
	return customLaunchPlan(runtime.GOOS, path, extra, media)
}

func customPlayerDialogOptions(goos string) mygo.OpenDialogOptions {
	opts := mygo.OpenDialogOptions{
		Title:       "选择播放器",
		ButtonLabel: "选择",
	}
	switch goos {
	case "windows":
		opts.Filters = []mygo.FileFilter{
			{Name: "程序 (*.exe)", Extensions: []string{"exe"}},
			{Name: "所有文件", Extensions: []string{"*"}},
		}
	case "darwin":
		opts.Message = "选择 .app 应用程序或可执行文件"
	}
	return opts
}

// PickCustomPlayer shows the native file dialog and returns the chosen path,
// or "" when the dialog is canceled.
func (p *Player) PickCustomPlayer(ctx context.Context) (string, error) {
	if p.webMode.Load() {
		return "", errors.New("网页模式不能选择本机播放器")
	}
	opts := customPlayerDialogOptions(runtime.GOOS)
	opts.Parent = mygo.CallerWindow(ctx)
	if cur := strings.TrimSpace(p.GetSettings().CustomPlayer); cur != "" {
		opts.DefaultPath = cur
	}
	paths, err := mygo.Dialog.Open(opts)
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

func normalizeCustomPlayer(s *Settings) error {
	s.CustomPlayer = strings.TrimSpace(s.CustomPlayer)
	s.CustomArgs = strings.TrimSpace(s.CustomArgs)
	if s.Player != "custom" {
		return nil
	}
	if err := validateCustomPlayer(s.CustomPlayer); err != nil {
		return err
	}
	s.CustomPlayer = filepath.Clean(s.CustomPlayer)
	if _, err := customPlayerArgv(s.CustomArgs, "http://127.0.0.1/"); err != nil {
		return err
	}
	return nil
}

func runExternal(name string, args []string) error {
	if !filepath.IsAbs(name) {
		p, err := exec.LookPath(name)
		if err != nil {
			return fmt.Errorf("无法启动 %s", name)
		}
		name = p
	}
	cmd := exec.Command(name, args...)
	devnull, err := os.Open(os.DevNull)
	if err == nil {
		cmd.Stdin = devnull
	}
	if err := cmd.Start(); err != nil {
		if devnull != nil {
			devnull.Close()
		}
		return err
	}
	go func() {
		_ = cmd.Wait()
		if devnull != nil {
			devnull.Close()
		}
	}()
	return nil
}

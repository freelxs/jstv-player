package tvbox

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed all:pyboot
var pybootFS embed.FS

var (
	pythonDetectMu    sync.Mutex
	pythonDetectCache = map[string]bool{}
)

// FindPython locates a usable Python 3 interpreter. If explicit is non-empty,
// only that path is checked. Otherwise bundled, managed, PATH, and standard
// system locations are probed in order.
func FindPython(explicit string) string {
	explicit = strings.TrimSpace(explicit)
	if explicit != "" {
		if isUsablePython3(explicit) {
			return explicit
		}
		return ""
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		if runtime.GOOS == "darwin" {
			res := filepath.Join(filepath.Dir(filepath.Dir(exe)), "Resources")
			candidates = append(candidates,
				filepath.Join(res, "python", runtime.GOARCH, "bin", "python3"),
				filepath.Join(res, "python", "bin", "python3"),
			)
		} else if runtime.GOOS == "windows" {
			dir := filepath.Dir(exe)
			candidates = append(candidates,
				filepath.Join(dir, "python", "python.exe"),
				filepath.Join(dir, "python.exe"),
			)
		} else {
			dir := filepath.Dir(exe)
			candidates = append(candidates, filepath.Join(dir, "python", "bin", "python3"))
		}
	}
	if cacheDir, err := os.UserCacheDir(); err == nil && cacheDir != "" {
		if runtime.GOOS == "windows" {
			candidates = append(candidates, filepath.Join(cacheDir, "jstv-player", "python", "python.exe"))
		} else {
			candidates = append(candidates, filepath.Join(cacheDir, "jstv-player", "python", "bin", "python3"))
		}
	}
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			candidates = append(candidates, p)
		}
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		candidates = append(candidates,
			"/opt/homebrew/bin/python3",
			"/usr/local/bin/python3",
			"/usr/bin/python3",
			"/Library/Frameworks/Python.framework/Versions/Current/bin/python3",
		)
	}
	seen := map[string]bool{}
	for _, cand := range candidates {
		if cand == "" || seen[cand] {
			continue
		}
		seen[cand] = true
		if isUsablePython3(cand) {
			return cand
		}
	}
	return ""
}

func isUsablePython3(bin string) bool {
	st, err := os.Stat(bin)
	if err != nil || st.IsDir() {
		return false
	}
	pythonDetectMu.Lock()
	if ok, cached := pythonDetectCache[bin]; cached {
		pythonDetectMu.Unlock()
		return ok
	}
	pythonDetectMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "-c", "import sys; print(sys.version_info[0])").Output()
	ok := err == nil && strings.TrimSpace(string(out)) == "3"

	pythonDetectMu.Lock()
	pythonDetectCache[bin] = ok
	pythonDetectMu.Unlock()
	return ok
}

func pythonScriptURL(src Source) string {
	if isHTTP(src.API) && strings.Contains(apiPath(src.API), ".py") {
		return strings.TrimSpace(src.API)
	}
	var extStr string
	if json.Unmarshal(src.Ext, &extStr) == nil {
		extStr = strings.TrimSpace(extStr)
		if idx := strings.Index(extStr, "$$$"); idx >= 0 {
			extStr = strings.TrimSpace(extStr[:idx])
		}
		if isHTTP(extStr) && strings.Contains(apiPath(extStr), ".py") {
			return extStr
		}
	}
	return ""
}

type pythonHost struct {
	cmd     *exec.Cmd
	base    string
	token   string
	dir     string
	client  *http.Client
	done    chan struct{}
	mu      sync.Mutex
	scripts map[string]string
}

func startPythonHost(ctx context.Context, opt Options, hosts map[string]string) (*pythonHost, error) {
	pyBin := strings.TrimSpace(opt.Python)
	if pyBin == "" {
		pyBin = FindPython("")
	}
	if pyBin == "" {
		return nil, errors.New("未找到 Python 3 运行时，无法运行 Python 站点")
	}
	dir := strings.TrimSpace(opt.Dir)
	if dir == "" {
		var err error
		dir, err = os.MkdirTemp("", "jstv-py-")
		if err != nil {
			return nil, err
		}
	}
	bootDir := filepath.Join(dir, "pyboot")
	if err := extractPyBoot(bootDir); err != nil {
		return nil, err
	}
	cacheDir := filepath.Join(dir, "pycache")
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		return nil, err
	}
	token := randomHexToken(16)
	hostScript := filepath.Join(bootDir, "py_host.py")
	cmd := exec.Command(pyBin, "-u", hostScript)
	cmd.Dir = bootDir
	cmd.Env = pythonEnv(opt.InheritEnv, hosts, token, cacheDir)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("启动 PythonHost 失败: %w", err)
	}
	h := &pythonHost{
		cmd:     cmd,
		token:   token,
		dir:     dir,
		client:  &http.Client{Timeout: 45 * time.Second},
		done:    make(chan struct{}),
		scripts: map[string]string{},
	}
	go func() {
		_ = cmd.Wait()
		close(h.done)
	}()
	go logPipe(opt.Log, stderr)
	ready := make(chan int, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "PY_READY ") {
				var port int
				if _, err := fmt.Sscanf(line, "PY_READY %d", &port); err == nil && port > 0 {
					select {
					case ready <- port:
					default:
					}
				}
				continue
			}
			if opt.Log != nil {
				opt.Log(line)
			}
		}
	}()
	if ctx == nil {
		ctx = context.Background()
	}
	timer := time.NewTimer(15 * time.Second)
	defer timer.Stop()
	select {
	case port := <-ready:
		h.base = fmt.Sprintf("http://127.0.0.1:%d", port)
		if opt.Log != nil {
			opt.Log("Python 站点宿主已启动 (" + filepath.Base(pyBin) + ")")
		}
		return h, nil
	case <-h.done:
		h.stop()
		return nil, errors.New("PythonHost 进程提前退出")
	case <-ctx.Done():
		h.stop()
		return nil, ctx.Err()
	case <-timer.C:
		h.stop()
		return nil, errors.New("PythonHost 启动超时")
	}
}

func extractPyBoot(dst string) error {
	return fs.WalkDir(pybootFS, "pyboot", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(p, "pyboot")
		rel = strings.TrimPrefix(rel, "/")
		target := dst
		if rel != "" {
			target = filepath.Join(dst, filepath.FromSlash(rel))
		}
		if d.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := pybootFS.ReadFile(p)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
}

func pythonEnv(inherit bool, hosts map[string]string, token, cacheDir string) []string {
	base := os.Environ()
	if !inherit {
		base = minimalEnv()
	}
	hostJSON := ""
	if len(hosts) > 0 {
		b, _ := json.Marshal(hosts)
		hostJSON = string(b)
	}
	return append(base,
		"PYTHONDONTWRITEBYTECODE=1",
		"PYTHONUTF8=1",
		"JSTV_PY_TOKEN="+token,
		"JSTV_PY_CACHE="+cacheDir,
		"JSTV_HOSTS="+hostJSON,
	)
}

func randomHexToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (h *pythonHost) alive() bool {
	if h == nil || h.base == "" {
		return false
	}
	select {
	case <-h.done:
		return false
	default:
		return true
	}
}

func (h *pythonHost) stop() {
	if h == nil || h.cmd == nil || h.cmd.Process == nil {
		return
	}
	_ = h.cmd.Process.Kill()
	select {
	case <-h.done:
	case <-time.After(2 * time.Second):
	}
}

func (h *pythonHost) ensureScript(ctx context.Context, client *http.Client, opt Options, src Source) (string, error) {
	rawURL := pythonScriptURL(src)
	if rawURL == "" {
		return "", errors.New("Python 站点缺少脚本地址")
	}
	h.mu.Lock()
	if p, ok := h.scripts[rawURL]; ok {
		if _, err := os.Stat(p); err == nil {
			h.mu.Unlock()
			return p, nil
		}
	}
	h.mu.Unlock()

	var body []byte
	if opt.PinnedScripts != nil {
		if b, ok := opt.PinnedScripts[rawURL]; ok && len(b) > 0 {
			body = b
		}
	}
	if len(body) == 0 {
		var err error
		body, err = fetchPythonScript(ctx, client, rawURL, src.Header)
		if err != nil {
			return "", fmt.Errorf("下载 Python 站点脚本失败: %w", err)
		}
	}
	if looksLikeHTML(body) {
		return "", errors.New("下载到的 Python 脚本内容无效（返回了 HTML 页面）")
	}
	sum := sha256.Sum256([]byte(rawURL))
	scriptDir := filepath.Join(h.dir, "py_scripts")
	if err := os.MkdirAll(scriptDir, 0o700); err != nil {
		return "", err
	}
	localPath := filepath.Join(scriptDir, hex.EncodeToString(sum[:8])+".py")
	if err := os.WriteFile(localPath, body, 0o600); err != nil {
		return "", err
	}
	h.mu.Lock()
	h.scripts[rawURL] = localPath
	h.mu.Unlock()
	return localPath, nil
}

func fetchPythonScript(ctx context.Context, client *http.Client, rawURL string, headers map[string]string) ([]byte, error) {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	body, err := fetchURLHeaders(ctx, client, rawURL, headers)
	if err == nil && !looksLikeHTML(body) {
		return body, nil
	}
	if direct := unwrapGitHubProxy(rawURL); direct != "" && direct != rawURL {
		if alt, err2 := fetchURLHeaders(ctx, client, direct, headers); err2 == nil && !looksLikeHTML(alt) {
			return alt, nil
		}
	}
	if err != nil {
		return nil, err
	}
	return body, nil
}

// unwrapGitHubProxy extracts an embedded raw.githubusercontent.com URL from
// common proxy prefixes like https://gh-proxy.org/https://raw.githubusercontent.com/...
func unwrapGitHubProxy(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	trimmed := strings.TrimPrefix(u.Path, "/")
	if strings.HasPrefix(trimmed, "https://raw.githubusercontent.com/") || strings.HasPrefix(trimmed, "http://raw.githubusercontent.com/") {
		if u.RawQuery != "" {
			return trimmed + "?" + u.RawQuery
		}
		return trimmed
	}
	return ""
}

func pythonExtValue(src Source) any {
	raw := bytesTrim(src.Ext)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		if isHTTP(s) && s == pythonScriptURL(src) {
			return ""
		}
		return s
	}
	return string(raw)
}

func (h *pythonHost) call(ctx context.Context, scriptPath string, src Source, proxyBase, fn string, args map[string]any) (any, error) {
	if !h.alive() {
		return nil, errors.New("PythonHost 未运行")
	}
	payload := map[string]any{
		"site":       src.Key,
		"script":     scriptPath,
		"ext":        pythonExtValue(src),
		"proxy_base": proxyBase,
		"fn":         fn,
		"args":       args,
	}
	b, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.base+"/call", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-JSTV-Token", h.token)
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var wrap struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(respBody, &wrap); err != nil {
		return nil, fmt.Errorf("Python 站点返回不是 JSON: %s", truncate(string(respBody), 200))
	}
	if !wrap.OK {
		if wrap.Error == "" {
			wrap.Error = "Python 站点调用失败"
		}
		return nil, errors.New(wrap.Error)
	}
	return interpretDrpy(wrap.Result), nil
}

func (h *pythonHost) serveProxy(w http.ResponseWriter, r *http.Request, siteKey string) {
	if !h.alive() {
		http.Error(w, "PythonHost 未运行", http.StatusBadGateway)
		return
	}
	q := r.URL.Query()
	q.Set("_site", siteKey)
	target := h.base + "/proxy?" + q.Encode()
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	req.Header.Set("X-JSTV-Token", h.token)
	resp, err := h.client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for k, vals := range resp.Header {
		if strings.EqualFold(k, "Content-Length") || strings.EqualFold(k, "Transfer-Encoding") {
			continue
		}
		for _, v := range vals {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 16<<20))
}

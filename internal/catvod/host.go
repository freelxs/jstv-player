package catvod

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed bootstrap.js
var bootstrapJS []byte

// Host runs a plugin bundle in a Node.js child process.
type Host struct {
	Node       string // node executable
	Dir        string // working dir; the bundle keeps default.db.json here
	Script     string // path to index.js
	Config     string // optional JSON merged into the start() config
	ConfigFile string // optional index.config.js
	LogFunc    func(line string)
	// ListenLAN binds the plugin's HTTP server to all interfaces (0.0.0.0)
	// instead of loopback, so its config page is reachable from other devices.
	ListenLAN bool
	// InheritEnv is an explicit compatibility option. The default environment
	// keeps only platform/runtime basics and CA variables, not proxy credentials.
	InheritEnv      bool
	InheritProxyEnv bool

	mu      sync.Mutex
	cmd     *exec.Cmd
	in      io.WriteCloser
	port    int
	done    chan struct{}
	started bool
}

// FindNode returns a usable node executable: explicit, bundled, then PATH and
// usual install locations (GUI apps on macOS get a minimal PATH).
func FindNode(explicit string) (string, error) {
	return findNode(explicit, runtime.GOOS, runtime.GOARCH, os.Executable, exec.LookPath, os.Stat, os.UserHomeDir, os.Getenv)
}

type executableFunc func() (string, error)
type lookPathFunc func(string) (string, error)
type statFunc func(string) (os.FileInfo, error)
type homeDirFunc func() (string, error)
type getenvFunc func(string) string

func findNode(explicit, goos, goarch string, executable executableFunc, lookPath lookPathFunc, stat statFunc, homeDir homeDirFunc, getenv getenvFunc) (string, error) {
	if explicit != "" {
		if _, err := stat(explicit); err == nil {
			return explicit, nil
		}
		return "", fmt.Errorf("找不到 node: %s", explicit)
	}
	// MyGo puts platform resources at the top of Contents/Resources on macOS
	// and beside the executable on Windows. Derive both from the executable,
	// never from the caller's working directory.
	if exe, err := executable(); err == nil {
		candidates := []string{}
		if goos == "darwin" {
			resources := filepath.Join(filepath.Dir(filepath.Dir(exe)), "Resources")
			if goarch == "arm64" || goarch == "amd64" {
				candidates = append(candidates, filepath.Join(resources, "node", goarch, "bin", "node"))
			}
		} else {
			candidates = append(candidates, filepath.Join(filepath.Dir(exe), "node", "bin", "node.exe"), filepath.Join(filepath.Dir(exe), "node.exe"))
		}
		for _, candidate := range candidates {
			if _, err := stat(candidate); err == nil {
				return candidate, nil
			}
		}
	}
	if p, err := lookPath("node"); err == nil {
		return p, nil
	}
	home, _ := homeDir()
	cands := []string{"/opt/homebrew/bin/node", "/usr/local/bin/node", "/usr/bin/node"}
	if goos == "windows" {
		cands = []string{`C:\Program Files\nodejs\node.exe`}
		for _, env := range []string{"ProgramFiles", "NVM_SYMLINK", "LOCALAPPDATA"} {
			if d := getenv(env); d != "" {
				cands = append(cands, filepath.Join(d, "nodejs", "node.exe"), filepath.Join(d, "node.exe"))
			}
		}
	}
	nvm, _ := filepath.Glob(filepath.Join(home, ".nvm/versions/node/*/bin/node"))
	for i := len(nvm) - 1; i >= 0; i-- {
		cands = append(cands, nvm[i])
	}
	cands = append(cands, filepath.Join(home, ".volta/bin/node"), filepath.Join(home, ".local/bin/node"))
	for _, c := range cands {
		if _, err := stat(c); err == nil {
			return c, nil
		}
	}
	return "", errors.New("未找到 Node.js：请安装 Node.js 18+，或在设置中填写 node 路径")
}

// readPluginLogLine returns the next line, discarding it when it exceeds max.
// A scanner that stops on ErrTooLong leaves the stdout pipe full, which stalls
// the plugin and prevents the process from exiting.
func readPluginLogLine(r *bufio.Reader, max int) (string, error) {
	var buf []byte
	oversized := false
	for {
		frag, err := r.ReadSlice('\n')
		if !oversized {
			if len(buf)+len(frag) > max {
				oversized = true
				buf = nil
			} else {
				buf = append(buf, frag...)
			}
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if oversized {
			return "", err
		}
		return strings.TrimRight(string(buf), "\r\n"), err
	}
}

// listenHost is the address the plugin's HTTP server binds: loopback by
// default, all interfaces when the user enables LAN access.
func listenHost(lan bool) string {
	if lan {
		return "0.0.0.0"
	}
	return "127.0.0.1"
}

// Start launches the bundle and waits until GET /config answers.
func (h *Host) Start(timeout time.Duration) error {
	return h.StartContext(context.Background(), timeout)
}

// StartContext launches the bundle and waits for GET /config. Cancellation and
// timeout both stop and reap a child that has already been started.
func (h *Host) StartContext(ctx context.Context, timeout time.Duration) error {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	h.Stop()
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := os.MkdirAll(h.Dir, 0o755); err != nil {
		return err
	}
	boot := filepath.Join(h.Dir, "jstv-bootstrap.cjs")
	if err := os.WriteFile(boot, bootstrapJS, 0o644); err != nil {
		return err
	}
	// Let the plugin bind port 0 itself and report the selected port. This
	// removes the close-then-rebind race from reserving a free port in Go.
	port := 0
	cmd := exec.Command(h.Node, boot, h.Script)
	cmd.Dir = h.Dir
	cmd.Env = append(nodeEnvironment(h.InheritEnv, h.InheritProxyEnv),
		fmt.Sprintf("DEV_HTTP_PORT=%d", port),
		"JSTV_LISTEN="+listenHost(h.ListenLAN),
		"NODE_PATH="+h.Dir,
		"NODE_ENV=production",
		"JSTV_CONFIG="+h.Config,
		"JSTV_CONFIG_FILE="+h.ConfigFile,
	)
	in, _ := cmd.StdinPipe()
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		_ = in.Close()
		_ = pr.Close()
		_ = pw.Close()
		return err
	}
	h.cmd, h.in, h.port, h.done, h.started = cmd, in, port, make(chan struct{}), true
	var lastErr, lastLine string
	var lmu sync.Mutex
	snapshot := func() (line, fatal string) {
		lmu.Lock()
		defer lmu.Unlock()
		return lastLine, lastErr
	}
	ready := make(chan int, 1)
	go func() {
		rd := bufio.NewReaderSize(pr, 64<<10)
		for {
			line, err := readPluginLogLine(rd, 1<<20)
			if line != "" {
				if n, ok := pluginReadyPort(line); ok {
					select {
					case ready <- n:
					default:
					}
				}
				lmu.Lock()
				lastLine = trimStartupLog(line)
				if strings.Contains(line, "JSTV_FATAL") || strings.Contains(line, "Error") {
					lastErr = lastLine
				}
				lmu.Unlock()
				if h.LogFunc != nil {
					h.LogFunc(line)
				}
			}
			if err != nil {
				return
			}
		}
	}()
	done := h.done
	go func() { cmd.Wait(); pw.Close(); close(done) }()
	select {
	case h.port = <-ready:
	case <-done:
		_, fatal := snapshot()
		h.stopLocked()
		return fmt.Errorf("源进程已退出: %s", fatal)
	case <-ctx.Done():
		line, _ := snapshot()
		h.stopLocked()
		return startupFailure(ctx.Err(), line, 0)
	}

	// No Client.Timeout. The request uses the startup context, so a slow
	// /config is allowed to finish instead of being aborted and retried
	// every 3 seconds. A dial failure still retries; the process is already
	// listening when its log reports the port.
	dialer := &net.Dialer{Timeout: 2 * time.Second}
	client := &http.Client{Transport: &http.Transport{
		Proxy: func(req *http.Request) (*url.URL, error) {
			if req != nil && req.URL != nil {
				switch strings.TrimSuffix(strings.ToLower(req.URL.Hostname()), ".") {
				case "localhost", "127.0.0.1", "::1":
					return nil, nil
				}
			}
			return http.ProxyFromEnvironment(req)
		},
		DialContext: dialer.DialContext,
	}}
	for {
		if err := ctx.Err(); err != nil {
			line, _ := snapshot()
			h.stopLocked()
			return startupFailure(err, line, h.port)
		}
		select {
		case <-done:
			_, fatal := snapshot()
			h.stopLocked()
			return fmt.Errorf("源进程已退出: %s", fatal)
		default:
		}
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, h.baseLocked()+"/config", nil)
		if reqErr != nil {
			h.stopLocked()
			return reqErr
		}
		resp, err := client.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				line, _ := snapshot()
				h.stopLocked()
				return startupFailure(ctx.Err(), line, h.port)
			}
		} else {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			// 200 is the configured bundle. Any other HTTP status still
			// proves this port is the plugin; keep asking until the routes
			// answer or the startup deadline arrives.
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			line, _ := snapshot()
			h.stopLocked()
			return startupFailure(ctx.Err(), line, h.port)
		case <-done:
			_, fatal := snapshot()
			h.stopLocked()
			return fmt.Errorf("源进程已退出: %s", fatal)
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// pluginReadyPort reads the port the plugin is serving. Bundles that use
// catServerFactory print "JSTV_READY <port>". Fastify bundles instead log
// "Server listening at http://host:port", including inside a JSON line, and
// do not print JSTV_READY.
func pluginReadyPort(line string) (int, bool) {
	line = strings.TrimSpace(strings.ReplaceAll(line, `\/`, `/`))
	if strings.HasPrefix(line, "JSTV_READY ") {
		n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "JSTV_READY ")))
		return n, err == nil && n > 0
	}
	const marker = "Server listening at "
	i := strings.Index(line, marker)
	if i < 0 {
		return 0, false
	}
	raw := line[i+len(marker):]
	if cut := strings.IndexAny(raw, "\" \t"); cut >= 0 {
		raw = raw[:cut]
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return 0, false
	}
	n, err := strconv.Atoi(u.Port())
	return n, err == nil && n > 0
}

func trimStartupLog(line string) string {
	line = strings.TrimSpace(line)
	if len(line) > 400 {
		return line[:400] + "…"
	}
	return line
}

func startupFailure(err error, lastLine string, port int) error {
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	msg := "插件启动超时：没有收到监听端口"
	if port > 0 {
		msg = fmt.Sprintf("插件启动超时：已监听到端口 %d，但 /config 没有在时限内完成", port)
	}
	if lastLine != "" {
		msg += "。最后日志：" + lastLine
	}
	return fmt.Errorf("%s: %w", msg, err)
}

func nodeEnvironment(inherit bool, proxy ...bool) []string {
	if inherit {
		return os.Environ()
	}
	wanted := []string{"PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "TMPDIR", "PATHEXT", "COMSPEC", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS", "LANG", "LC_ALL"}
	if len(proxy) > 0 && proxy[0] {
		wanted = append(wanted, "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY")
	}
	values := make(map[string]string, len(wanted))
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		for _, allowed := range wanted {
			if strings.EqualFold(key, allowed) {
				values[key] = value
				break
			}
		}
	}
	out := make([]string, 0, len(values))
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	return out
}

func (h *Host) baseLocked() string { return fmt.Sprintf("http://127.0.0.1:%d", h.port) }

// Base is the http://127.0.0.1:port of the running bundle ("" if stopped).
func (h *Host) Base() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cmd == nil {
		return ""
	}
	select {
	case <-h.done:
		return ""
	default:
	}
	return h.baseLocked()
}

// Stop asks the bootstrap to call the plugin's stop(), then force-kills after
// a bounded grace period. The JSON line protocol works on Windows and Unix.
func (h *Host) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopLocked()
}

func (h *Host) stopLocked() {
	if h.cmd == nil {
		return
	}
	if h.in != nil && h.started {
		_ = json.NewEncoder(h.in).Encode(map[string]string{"type": "shutdown"})
	}
	if h.in != nil {
		_ = h.in.Close()
	}
	select {
	case <-h.done:
	case <-time.After(1500 * time.Millisecond):
		_ = h.cmd.Process.Kill()
		<-h.done
	}
	h.cmd = nil
	h.in = nil
	h.done = nil
	h.port = 0
	h.started = false
}

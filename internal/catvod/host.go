package catvod

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	InheritEnv bool

	mu      sync.Mutex
	cmd     *exec.Cmd
	in      io.WriteCloser
	port    int
	done    chan struct{}
	started bool
}

// FindNode returns a usable node executable: explicit, then PATH, then the
// usual install locations (GUI apps on macOS get a minimal PATH).
func FindNode(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err == nil {
			return explicit, nil
		}
		return "", fmt.Errorf("找不到 node: %s", explicit)
	}
	if p, err := exec.LookPath("node"); err == nil {
		return p, nil
	}
	home, _ := os.UserHomeDir()
	cands := []string{"/opt/homebrew/bin/node", "/usr/local/bin/node", "/usr/bin/node"}
	if runtime.GOOS == "windows" {
		cands = []string{`C:\Program Files\nodejs\node.exe`}
		for _, env := range []string{"ProgramFiles", "NVM_SYMLINK", "LOCALAPPDATA"} {
			if d := os.Getenv(env); d != "" {
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
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", errors.New("未找到 Node.js：请安装 Node.js 18+，或在设置中填写 node 路径")
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
	cmd.Env = append(nodeEnvironment(h.InheritEnv),
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
	var lastErr string
	var lmu sync.Mutex
	ready := make(chan int, 1)
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "JSTV_READY ") {
				if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "JSTV_READY "))); err == nil && n > 0 {
					select {
					case ready <- n:
					default:
					}
				}
			}
			if strings.Contains(line, "JSTV_FATAL") || strings.Contains(line, "Error") {
				lmu.Lock()
				lastErr = line
				lmu.Unlock()
			}
			if h.LogFunc != nil {
				h.LogFunc(line)
			}
		}
	}()
	done := h.done
	go func() { cmd.Wait(); pw.Close(); close(done) }()
	select {
	case h.port = <-ready:
	case <-done:
		lmu.Lock()
		err := fmt.Errorf("源进程已退出: %s", lastErr)
		lmu.Unlock()
		h.stopLocked()
		return err
	case <-ctx.Done():
		h.stopLocked()
		return ctx.Err()
	}

	c := &http.Client{Timeout: 3 * time.Second}
	for {
		if err := ctx.Err(); err != nil {
			h.stopLocked()
			return err
		}
		select {
		case <-done:
			lmu.Lock()
			err := fmt.Errorf("源进程已退出: %s", lastErr)
			lmu.Unlock()
			h.stopLocked()
			return err
		default:
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, h.baseLocked()+"/config", nil)
		resp, err := c.Do(req)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			h.stopLocked()
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func nodeEnvironment(inherit bool) []string {
	if inherit {
		return os.Environ()
	}
	wanted := []string{"PATH", "HOME", "USERPROFILE", "SYSTEMROOT", "WINDIR", "TEMP", "TMP", "TMPDIR", "PATHEXT", "COMSPEC", "SSL_CERT_FILE", "SSL_CERT_DIR", "NODE_EXTRA_CA_CERTS", "LANG", "LC_ALL"}
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

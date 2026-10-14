package catvod

import (
	"bufio"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

	mu   sync.Mutex
	cmd  *exec.Cmd
	in   io.WriteCloser
	port int
	done chan struct{}
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

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// Start launches the bundle and waits until GET /config answers.
func (h *Host) Start(timeout time.Duration) error {
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
	port, err := freePort()
	if err != nil {
		return err
	}
	cmd := exec.Command(h.Node, boot, h.Script)
	cmd.Dir = h.Dir
	cmd.Env = append(os.Environ(),
		fmt.Sprintf("DEV_HTTP_PORT=%d", port),
		"NODE_PATH="+h.Dir,
		"NODE_ENV=production",
		"JSTV_CONFIG="+h.Config,
		"JSTV_CONFIG_FILE="+h.ConfigFile,
	)
	in, _ := cmd.StdinPipe()
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return err
	}
	h.cmd, h.in, h.port, h.done = cmd, in, port, make(chan struct{})
	var lastErr string
	var lmu sync.Mutex
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
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

	deadline := time.Now().Add(timeout)
	c := &http.Client{Timeout: 3 * time.Second}
	for time.Now().Before(deadline) {
		select {
		case <-done:
			lmu.Lock()
			defer lmu.Unlock()
			return fmt.Errorf("源进程已退出: %s", lastErr)
		default:
		}
		resp, err := c.Get(h.baseLocked() + "/config")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(300 * time.Millisecond)
	}
	return errors.New("等待源启动超时")
}

func (h *Host) baseLocked() string { return fmt.Sprintf("http://127.0.0.1:%d", h.port) }

// Base is the http://127.0.0.1:port of the running bundle ("" if stopped).
func (h *Host) Base() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cmd == nil {
		return ""
	}
	return h.baseLocked()
}

// Stop kills the child process.
func (h *Host) Stop() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cmd == nil {
		return
	}
	h.in.Close()
	select {
	case <-h.done:
	case <-time.After(1500 * time.Millisecond):
		h.cmd.Process.Kill()
		<-h.done
	}
	h.cmd = nil
}

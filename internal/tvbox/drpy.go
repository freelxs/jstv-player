package tvbox

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed jsboot
var jsbootFS embed.FS

type drpyEngine struct {
	cmd    *exec.Cmd
	base   string
	client *http.Client
	done   chan struct{}
}

func startDrpy(ctx context.Context, opt Options, hosts map[string]string, src Source) (*drpyEngine, error) {
	if strings.TrimSpace(opt.Node) == "" {
		return nil, errors.New("未找到 Node.js")
	}
	if strings.TrimSpace(opt.Dir) == "" {
		return nil, errors.New("没有可写的缓存目录")
	}
	dir, err := prepareDrpy(ctx, opt, src)
	if err != nil {
		return nil, err
	}
	extURL, extFile, extJSON, err := materializeExt(ctx, clientFor(opt), dir, src.Ext)
	if err != nil {
		return nil, err
	}
	return startDrpyModule(ctx, opt, hosts, filepath.Join(dir, "drpy2.mjs"), extURL, extFile, extJSON)
}

func startDrpyModule(ctx context.Context, opt Options, hosts map[string]string, modulePath, extURL, extFile, extJSON string) (*drpyEngine, error) {
	dir := filepath.Dir(modulePath)
	if err := copyBoot(dir); err != nil {
		return nil, err
	}
	boot := filepath.Join(dir, "drpy_boot.mjs")
	cmd := exec.Command(opt.Node, boot)
	cmd.Dir = dir
	cmd.Env = drpyEnv(opt.InheritEnv, hosts, modulePath, extURL, extFile, extJSON)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	eng := &drpyEngine{cmd: cmd, client: &http.Client{Timeout: 60 * time.Second}, done: make(chan struct{})}
	go func() { cmd.Wait(); close(eng.done) }()
	go logPipe(opt.Log, stderr)
	ready := make(chan int, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if opt.Log != nil && !strings.HasPrefix(line, "DRPY_READY ") {
				opt.Log(line)
			}
			if strings.HasPrefix(line, "DRPY_READY ") {
				var port int
				if _, err := fmt.Sscanf(line, "DRPY_READY %d", &port); err == nil && port > 0 {
					select {
					case ready <- port:
					default:
					}
				}
			}
		}
	}()
	timer := time.NewTimer(90 * time.Second)
	defer timer.Stop()
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case port := <-ready:
		eng.base = fmt.Sprintf("http://127.0.0.1:%d", port)
		return eng, nil
	case <-eng.done:
		eng.stop()
		return nil, errors.New("JavaScript 站点进程已退出")
	case <-ctx.Done():
		eng.stop()
		return nil, ctx.Err()
	case <-timer.C:
		eng.stop()
		return nil, errors.New("JavaScript 站点启动超时")
	}
}

func (e *drpyEngine) call(ctx context.Context, fn string, args map[string]any) (any, error) {
	if e == nil || e.base == "" {
		return nil, errors.New("站点未启动")
	}
	if args == nil {
		args = map[string]any{}
	}
	args["fn"] = fn
	body, _ := json.Marshal(args)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.base+"/", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var wrap struct {
		OK     bool            `json:"ok"`
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	if err := json.Unmarshal(b, &wrap); err != nil {
		return nil, fmt.Errorf("站点返回不是 JSON: %s", truncate(string(b), 200))
	}
	if !wrap.OK {
		if wrap.Error == "" {
			wrap.Error = "站点调用失败"
		}
		return nil, errors.New(wrap.Error)
	}
	return interpretDrpy(wrap.Result), nil
}

func (e *drpyEngine) stop() {
	if e == nil || e.cmd == nil || e.cmd.Process == nil {
		return
	}
	_ = e.cmd.Process.Kill()
	select {
	case <-e.done:
	case <-time.After(2 * time.Second):
	}
}

func interpretDrpy(raw json.RawMessage) any {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return map[string]any{}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return map[string]any{}
	}
	s, ok := v.(string)
	if !ok {
		return v
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return map[string]any{}
	}
	var inner any
	if json.Unmarshal([]byte(s), &inner) == nil {
		return inner
	}
	return map[string]any{"parse": 0, "url": s}
}

func prepareDrpy(ctx context.Context, opt Options, src Source) (string, error) {
	script, err := url.Parse(src.API)
	if err != nil || (script.Scheme != "http" && script.Scheme != "https") {
		return "", errors.New("JavaScript 站点地址无效")
	}
	sum := sha256.Sum256([]byte(script.String()))
	dir := filepath.Join(opt.Dir, "drpy", hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	client := opt.HTTP
	if client == nil {
		client = HTTPClient(nil, 60*time.Second)
	}
	body, err := fetchURL(ctx, client, script.String())
	if err != nil {
		return "", fmt.Errorf("下载 drpy 失败: %w", err)
	}
	rewritten, err := rewriteDrpy(string(body))
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "drpy2.mjs"), []byte(rewritten), 0o644); err != nil {
		return "", err
	}
	base := *script
	base.Path = script.Path[:strings.LastIndex(script.Path, "/")+1]
	base.RawQuery, base.Fragment = "", ""
	files := [][2]string{
		{"cheerio.min.js", "cheerio.min.js"},
		{"crypto-js.js", "crypto-js.cjs"},
		{"node-rsa.js", "node-rsa.cjs"},
		{"pako.min.js", "pako.min.cjs"},
		{"模板.js", "模板.js"},
		{"gbk.js", "gbk.js"},
		{"json5.js", "json5.cjs"},
		{"jinja.js", "jinja.cjs"},
	}
	for _, pair := range files {
		ref, err := url.Parse(pair[0])
		if err != nil {
			return "", err
		}
		target := base.ResolveReference(ref)
		content, err := fetchURL(ctx, client, target.String())
		if err != nil {
			return "", fmt.Errorf("下载 %s 失败: %w", pair[0], err)
		}
		if err := os.WriteFile(filepath.Join(dir, pair[1]), content, 0o644); err != nil {
			return "", err
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{\"type\":\"module\"}\n"), 0o644); err != nil {
		return "", err
	}
	shim, err := jsbootFS.ReadFile("jsboot/shims.mjs")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "shims.mjs"), shim, 0o644); err != nil {
		return "", err
	}
	return dir, nil
}

func rewriteDrpy(src string) (string, error) {
	if !strings.Contains(src, "export default") || !strings.Contains(src, "function init") {
		return "", errors.New("下载到的脚本不是 drpy 模块")
	}
	src = strings.ReplaceAll(src, "assets://js/lib/", "./")
	for _, stmt := range []string{
		`import"./crypto-js.js";`,
		`import "./crypto-js.js";`,
		`import"./node-rsa.js";`,
		`import "./node-rsa.js";`,
		`import"./pako.min.js";`,
		`import "./pako.min.js";`,
		`import"./json5.js";`,
		`import "./json5.js";`,
		`import"./jinja.js";`,
		`import "./jinja.js";`,
	} {
		src = strings.ReplaceAll(src, stmt, "")
	}
	needle := `import cheerio from"./cheerio.min.js";`
	if !strings.Contains(src, needle) {
		needle = `import cheerio from "./cheerio.min.js";`
	}
	if !strings.Contains(src, needle) {
		return "", errors.New("drpy 的 cheerio 导入无法识别")
	}
	src = strings.Replace(src, needle, needle+`import"./shims.mjs";`, 1)
	return src, nil
}

func clientFor(opt Options) *http.Client {
	if opt.HTTP != nil {
		return opt.HTTP
	}
	return HTTPClient(nil, 60*time.Second)
}

func materializeExt(ctx context.Context, client *http.Client, dir string, raw json.RawMessage) (extURL, extFile, extJSON string, err error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return "", "", "", nil
	}
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		if strings.HasPrefix(asString, "http://") || strings.HasPrefix(asString, "https://") {
			body, err := fetchURL(ctx, client, asString)
			if err != nil {
				return "", "", "", fmt.Errorf("下载规则失败: %w", err)
			}
			asString = string(body)
		}
		path := filepath.Join(dir, "ext.txt")
		if err := os.WriteFile(path, []byte(asString), 0o644); err != nil {
			return "", "", "", err
		}
		return "", path, "", nil
	}
	path := filepath.Join(dir, "ext.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return "", "", "", err
	}
	return "", path, "1", nil
}

func copyBoot(dir string) error {
	for _, name := range []string{"drpy_boot.mjs", "drpy_http.mjs"} {
		b, err := jsbootFS.ReadFile("jsboot/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func fetchURL(ctx context.Context, client *http.Client, raw string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 JSTVPlayer")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	if len(b) == 0 {
		return nil, errors.New("空文件")
	}
	return b, nil
}

func drpyEnv(inherit bool, hosts map[string]string, modulePath, extURL, extFile, extJSON string) []string {
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
		"DRPY_MODULE="+modulePath,
		"DRPY_EXT_URL="+extURL,
		"DRPY_EXT_FILE="+extFile,
		"DRPY_EXT_JSON="+extJSON,
		"JSTV_HOSTS="+hostJSON,
	)
}

func minimalEnv() []string {
	allow := map[string]bool{
		"PATH": true, "HOME": true, "USERPROFILE": true, "SYSTEMROOT": true, "WINDIR": true,
		"TEMP": true, "TMP": true, "TMPDIR": true, "PATHEXT": true, "COMSPEC": true,
		"SSL_CERT_FILE": true, "SSL_CERT_DIR": true, "NODE_EXTRA_CA_CERTS": true, "LANG": true, "LC_ALL": true,
	}
	var out []string
	for _, entry := range os.Environ() {
		k, _, ok := strings.Cut(entry, "=")
		if ok && allow[strings.ToUpper(k)] {
			out = append(out, entry)
		}
	}
	return out
}

func logPipe(log func(string), r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if log != nil {
			log(sc.Text())
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Fetch downloads a subscription body and returns the final URL after redirects.
func Fetch(client *http.Client, raw string, limit int64) ([]byte, string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, "", errors.New("订阅地址需要是 http(s) URL")
	}
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	req, err := http.NewRequest(http.MethodGet, raw, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 JSTVPlayer")
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if limit <= 0 {
		limit = 8 << 20
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(b)) > limit {
		return nil, "", errors.New("订阅内容过大")
	}
	final := raw
	if resp.Request != nil && resp.Request.URL != nil {
		final = resp.Request.URL.String()
	}
	return b, final, nil
}

package catvod

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// DefaultBridgePort is the loopback port some plugins use for an optional
// external "play bridge" (http://127.0.0.1:9979/play?vid=…).
const DefaultBridgePort = 9979

// DefaultBridgeTemplate is empty: the play bridge is disabled until the
// user enters their own URL template ({vid}, {res}) in Settings.
const DefaultBridgeTemplate = ""

// Bridge is an optional, user-configured play bridge: GET /play?vid=<id>
// is resolved through the user's Template (first {res} that answers with
// video/*) and streamed with Range support. It does nothing without a
// template.
type Bridge struct {
	Client   *http.Client
	Template string   // default DefaultBridgeTemplate
	Res      []string // tried in order
	addr     string
	mu       sync.Mutex
	resolved map[string]string
}

var vidRe = regexp.MustCompile(`^[0-9]{6,25}$`)

// Start listens on 127.0.0.1:port, or a random port when it is taken.
func (b *Bridge) Start(port int) error {
	l, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		if l, err = net.Listen("tcp", "127.0.0.1:0"); err != nil {
			return err
		}
	}
	b.addr = l.Addr().String()
	go http.Serve(l, b)
	return nil
}

// Addr is host:port of the bridge.
func (b *Bridge) Addr() string { return b.addr }

// Rewrite points URLs at the default bridge port to wherever this bridge
// actually listens.
func (b *Bridge) Rewrite(u string) string {
	if b.addr == "" {
		return u
	}
	pu, err := url.Parse(u)
	if err != nil || !IsLoopback(u) || pu.Port() != fmt.Sprint(DefaultBridgePort) {
		return u
	}
	pu.Host = b.addr
	return pu.String()
}

func (b *Bridge) client() *http.Client {
	if b.Client != nil {
		return b.Client
	}
	return &http.Client{Timeout: 0}
}

func (b *Bridge) template() string {
	if b.Template != "" {
		return b.Template
	}
	return DefaultBridgeTemplate
}

// Enabled reports whether the user configured a template.
func (b *Bridge) Enabled() bool { return strings.TrimSpace(b.template()) != "" }

func (b *Bridge) newReq(method, u string) *http.Request {
	req, _ := http.NewRequest(method, u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 JSTVPlayer")
	return req
}

// Resolve returns the MP4 URL of vid.
func (b *Bridge) Resolve(vid string) (string, error) {
	if !b.Enabled() {
		return "", fmt.Errorf("播放桥未配置（设置 → 播放桥模板）")
	}
	b.mu.Lock()
	if b.resolved == nil {
		b.resolved = map[string]string{}
	}
	if u, ok := b.resolved[vid]; ok {
		b.mu.Unlock()
		return u, nil
	}
	b.mu.Unlock()
	res := b.Res
	if len(res) == 0 {
		res = []string{"1080p", "720p", "540p", "480p", "360p"}
	}
	c := &http.Client{Timeout: 15 * time.Second, Transport: b.client().Transport}
	for _, r := range res {
		u := strings.NewReplacer("{vid}", vid, "{res}", r).Replace(b.template())
		req := b.newReq("GET", u)
		req.Header.Set("Range", "bytes=0-0")
		resp, err := c.Do(req)
		if err != nil {
			continue
		}
		resp.Body.Close()
		if (resp.StatusCode == 200 || resp.StatusCode == 206) && strings.HasPrefix(resp.Header.Get("Content-Type"), "video/") {
			b.mu.Lock()
			b.resolved[vid] = u
			b.mu.Unlock()
			return u, nil
		}
	}
	return "", fmt.Errorf("播放桥找不到 %s 的视频", vid)
}

func (b *Bridge) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/play" {
		http.NotFound(w, r)
		return
	}
	vid := r.URL.Query().Get("vid")
	if !vidRe.MatchString(vid) {
		http.Error(w, "bad vid", 400)
		return
	}
	u, err := b.Resolve(vid)
	if err != nil {
		http.Error(w, err.Error(), 404)
		return
	}
	req := b.newReq(r.Method, u).WithContext(r.Context())
	if rg := r.Header.Get("Range"); rg != "" {
		req.Header.Set("Range", rg)
	}
	resp, err := b.client().Do(req)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	defer resp.Body.Close()
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	if w.Header().Get("Accept-Ranges") == "" {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// LocalizeURL handles every loopback URL a source returns in one place:
// URLs on the running source server pass unchanged; URLs on the default
// bridge port go to the user-configured bridge when there is one; other loopback URLs whose path is a
// spider route (/spider/…, a stale or default source port such as 9988) are
// re-pointed at the current source server.
func LocalizeURL(u, sourceBase, bridgeAddr string) string {
	if !IsLoopback(u) {
		return u
	}
	if SameHost(u, sourceBase) {
		return u
	}
	pu, err := url.Parse(u)
	if err != nil {
		return u
	}
	if bridgeAddr != "" && pu.Port() == fmt.Sprint(DefaultBridgePort) && pu.Path == "/play" {
		pu.Host = bridgeAddr
		return pu.String()
	}
	if strings.HasPrefix(pu.Path, "/spider/") || pu.Path == "/m3u8-clean" || strings.HasPrefix(pu.Path, "/proxy") {
		if sb, err := url.Parse(sourceBase); err == nil && sb.Host != "" {
			pu.Host = sb.Host
			return pu.String()
		}
	}
	return u
}

// Preflight fetches the first byte of a stream the way a player would and
// reports HTTP errors (with the server's short message) before the player
// shows a generic "invalid URL / cannot open" dialog. html is true when the
// address is a web page (Content-Type text/html) rather than media.
func Preflight(c *http.Client, u string, headers map[string]string) (html bool, err error) {
	if c == nil {
		c = &http.Client{Timeout: 20 * time.Second}
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return false, fmt.Errorf("地址无效: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Range", "bytes=0-1023")
	resp, err := c.Do(req)
	if err != nil {
		return false, fmt.Errorf("无法连接播放地址: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return false, fmt.Errorf("该线路不可用（HTTP %d: %s），请换其他线路", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return strings.HasPrefix(strings.ToLower(resp.Header.Get("Content-Type")), "text/html"), nil
}

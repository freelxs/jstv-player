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

// LocalizeURL maps URLs returned by a source back to the source process that
// is actually running. Source servers bind to loopback, but some plugins
// advertise their website using a LAN interface address. Stale ports are
// only accepted for routes owned by this plugin protocol.
func LocalizeURL(u, sourceBase, bridgeAddr string) string {
	pu, err := url.Parse(u)
	if err != nil {
		return u
	}
	sb, err := url.Parse(sourceBase)
	if err != nil || sb.Scheme != "http" || sb.Host == "" {
		return u
	}
	if pu.Scheme == "" && pu.Host == "" {
		resolved := sb.ResolveReference(pu)
		if !sourceOwnedRoute(resolved.Path) {
			return u
		}
		return resolved.String()
	}
	if pu.Scheme == "" && pu.Host != "" {
		pu.Scheme = sb.Scheme
	}
	if pu.Scheme != "http" || pu.Host == "" {
		return u
	}
	if SameHost(u, sourceBase) {
		return u
	}
	if bridgeAddr != "" && pu.Port() == fmt.Sprint(DefaultBridgePort) && pu.Path == "/play" {
		if IsLoopback(u) {
			pu.Host = bridgeAddr
			return pu.String()
		}
	}
	localHost := IsLoopback(u) || isInterfaceIP(pu.Hostname())
	if !localHost {
		return u
	}
	// A URL on the current source port is attributable to the running plugin.
	// For an older port, only rewrite the source's narrow, known route space.
	if pu.Port() == sb.Port() || sourceOwnedRoute(pu.Path) {
		pu.Host = sb.Host
		pu.Scheme = sb.Scheme
		return pu.String()
	}
	return u
}

func sourceOwnedRoute(path string) bool {
	return path == "/website" || strings.HasPrefix(path, "/website/") ||
		strings.HasPrefix(path, "/spider/") || path == "/m3u8-clean" ||
		path == "/proxy" || strings.HasPrefix(path, "/proxy/")
}

func isInterfaceIP(host string) bool {
	ip := net.ParseIP(strings.Split(host, "%")[0])
	if ip == nil {
		return false
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return false
	}
	for _, addr := range addrs {
		var local net.IP
		switch a := addr.(type) {
		case *net.IPNet:
			local = a.IP
		case *net.IPAddr:
			local = a.IP
		}
		if local != nil && ip.Equal(local) {
			return true
		}
	}
	return false
}

// ConfigPageURL converts the plugin's configuration API root into its HTML
// UI only when it is being used as a navigation target. /website/ itself is
// an application/json API and must remain unchanged for normal requests.
func ConfigPageURL(u, sourceBase string) (string, bool) {
	pu, err := url.Parse(u)
	if err != nil || pu.Scheme != "http" || pu.User != nil || !SameHost(u, sourceBase) || (pu.Path != "/website" && pu.Path != "/website/") {
		return u, false
	}
	pu.Path = "/website/index.html"
	pu.RawPath = ""
	return pu.String(), true
}

// LocalizePlayStreams localizes plugin play results and recognizes its
// configuration endpoint as a browser page navigation.
func LocalizePlayStreams(streams []Stream, sourceBase, bridgeAddr string) {
	for i := range streams {
		streams[i].URL = LocalizeURL(streams[i].URL, sourceBase, bridgeAddr)
		if pageURL, ok := ConfigPageURL(streams[i].URL, sourceBase); ok {
			streams[i].URL = pageURL
			streams[i].Parse = true
		}
	}
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

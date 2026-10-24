package catvod

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

// HeaderProxy serves streams that need request headers (Referer, Origin,
// User-Agent, Cookie) to players that cannot set them, such as the
// webview's <video>. HLS playlists are rewritten so that segments and keys
// go through the proxy too.
type HeaderProxy struct {
	Client    *http.Client
	base      string
	mu        sync.Mutex
	resources map[string]proxyResource
	grants    map[string]proxyGrant
}

type proxyResource struct {
	URL      string
	Headers  map[string]string
	Session  string
	Name     string
	AllowLAN bool
	Expires  time.Time
}

type proxyGrant struct {
	URL      string
	Headers  map[string]string
	AllowLAN bool
	Expires  time.Time
}

const proxyResourceTTL = 2 * time.Hour

// defaultMediaUserAgent is sent when a plugin play response does not name
// one. Many hosts reject Go's default client token.
const defaultMediaUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
const maxProxyResources = 16384
const maxProxyGrants = 4096

// BaseURL is the loopback origin this proxy listens on, without a trailing slash.
// It is empty until Start succeeds. Web deployment rewrites it to a same-origin
// path before a browser sees a playlist; desktop players keep using this origin.
func (p *HeaderProxy) BaseURL() string {
	if p == nil {
		return ""
	}
	return p.base
}

// Start listens on 127.0.0.1 (random port).
func (p *HeaderProxy) Start() error {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	p.base = "http://" + l.Addr().String()
	p.resources = map[string]proxyResource{}
	p.grants = map[string]proxyGrant{}
	go http.Serve(l, p)
	return nil
}

// Wrap returns a proxied URL for u with headers h (u itself if h is empty).
func (p *HeaderProxy) Wrap(u string, h map[string]string) string {
	if len(h) == 0 || p.base == "" {
		return u
	}
	return p.WrapAlways(u, h)
}

// WrapAlways proxies u even without headers (e.g. so plain-http media is
// served from loopback).
func (p *HeaderProxy) WrapAlways(u string, h map[string]string) string {
	return p.WrapAlwaysWithLAN(u, h, false)
}

// WrapAlwaysWithLAN marks a user-authorized LAN media target for this playback scope.
func (p *HeaderProxy) WrapAlwaysWithLAN(u string, h map[string]string, allowLAN bool) string {
	if p.base == "" || !(strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")) {
		return u
	}
	if h == nil {
		h = map[string]string{}
	}
	return p.wrapSessionAuthorized(u, h, p.newSession(), proxyName(u), allowLAN)
}

// Grant records a plugin play result after the application validates it.
func (p *HeaderProxy) Grant(u string, h map[string]string, allowLAN bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.grants == nil {
		p.grants = map[string]proxyGrant{}
	}
	now := time.Now()
	for key, grant := range p.grants {
		if now.After(grant.Expires) {
			delete(p.grants, key)
		}
	}
	key := grantKey(u, h)
	if _, exists := p.grants[key]; !exists && len(p.grants) >= maxProxyGrants {
		return fmt.Errorf("too many active playback grants")
	}
	p.grants[key] = proxyGrant{URL: u, Headers: cloneHeaders(h), AllowLAN: allowLAN, Expires: now.Add(proxyResourceTTL)}
	return nil
}

// WrapGranted refuses any URL/header pair not registered by a play response.
func (p *HeaderProxy) WrapGranted(u string, h map[string]string) (string, error) {
	p.mu.Lock()
	g, ok := p.grants[grantKey(u, h)]
	if ok && time.Now().After(g.Expires) {
		delete(p.grants, grantKey(u, h))
		ok = false
	}
	p.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("播放地址未获应用授权")
	}
	wrapped := p.wrapSessionAuthorized(g.URL, g.Headers, p.newSession(), proxyName(g.URL), g.AllowLAN)
	if wrapped == "" {
		return "", fmt.Errorf("无法创建播放授权")
	}
	return wrapped, nil
}

func cloneHeaders(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = v
	}
	return out
}

func grantKey(u string, h map[string]string) string {
	// Stream.Headers is omitted from JSON when empty, so the frontend round
	// trip turns an empty map into nil. Those have identical request semantics.
	if len(h) == 0 {
		h = nil
	}
	b, _ := json.Marshal(struct {
		URL     string
		Headers map[string]string
	}{u, h})
	return string(b)
}

// NewSession starts an opaque authorization scope for one playback.
func (p *HeaderProxy) NewSession() string { return p.newSession() }

func (p *HeaderProxy) newSession() string {
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// WrapSession registers a URL and headers server-side and returns only an
// unpredictable ID. The file name suffix is cosmetic for demuxers.
func (p *HeaderProxy) WrapSession(u string, h map[string]string, session string) string {
	return p.wrapSessionAuthorized(u, h, session, proxyName(u), false)
}

func (p *HeaderProxy) wrapSessionNamed(u string, h map[string]string, session, name string) string {
	return p.wrapSessionAuthorized(u, h, session, name, false)
}

func (p *HeaderProxy) wrapSessionAuthorized(u string, h map[string]string, session, name string, allowLAN bool) string {
	if p.base == "" || session == "" {
		return ""
	}
	var b [24]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	id := hex.EncodeToString(b[:])
	copyHeaders := make(map[string]string, len(h))
	for k, v := range h {
		copyHeaders[k] = v
	}
	p.mu.Lock()
	if p.resources == nil {
		p.resources = map[string]proxyResource{}
	}
	now := time.Now()
	for oldID, old := range p.resources {
		if now.After(old.Expires) {
			delete(p.resources, oldID)
		}
	}
	if len(p.resources) >= maxProxyResources {
		p.mu.Unlock()
		return ""
	}
	p.resources[id] = proxyResource{URL: u, Headers: copyHeaders, Session: session, Name: name, AllowLAN: allowLAN, Expires: now.Add(proxyResourceTTL)}
	p.mu.Unlock()
	return p.base + "/p/" + id + "/" + name
}

// Reset revokes all outstanding playback IDs, e.g. when changing source.
func (p *HeaderProxy) Reset() {
	p.mu.Lock()
	p.resources = map[string]proxyResource{}
	p.grants = map[string]proxyGrant{}
	p.mu.Unlock()
}

func (p *HeaderProxy) resource(id string) (proxyResource, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	r, ok := p.resources[id]
	if ok && time.Now().After(r.Expires) {
		delete(p.resources, id)
		ok = false
	}
	return r, ok
}

var safeName = regexp.MustCompile(`[^A-Za-z0-9._-]`)

func proxyName(u string) string {
	name := "stream"
	if pu, err := url.Parse(u); err == nil {
		if b := path.Base(pu.Path); b != "/" && b != "." && b != "" {
			name = b
		}
	}
	name = safeName.ReplaceAllString(name, "_")
	ext := path.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	// Long paths are often base64 of the real media URL. Keeping the tail
	// put signatures into the local filename the player and logs can see.
	if len(stem) > 40 {
		stem = "stream"
	}
	if ext == "" {
		ext = ".bin"
	}
	return stem + ext
}

// segName names a playlist entry: playlists keep .m3u8; media segments
// are named .ts whatever they pretend to be (.jpg/.png disguised TS).
func segName(u string) string {
	n := proxyName(u)
	switch strings.ToLower(path.Ext(n)) {
	case ".m3u8", ".m3u", ".ts", ".m4s", ".mp4", ".aac", ".key", ".vtt", ".m4a", ".mp3":
		return n
	}
	return strings.TrimSuffix(n, path.Ext(n)) + ".ts"
}

var uriAttr = regexp.MustCompile(`URI="([^"]+)"`)

// RewritePlaylist makes every URI in an m3u8 absolute (against base) and
// passes it through wrap.
func RewritePlaylist(body []byte, base *url.URL, wrap func(string) string) []byte {
	abs := func(s string) string {
		r, err := base.Parse(strings.TrimSpace(s))
		if err != nil {
			return s
		}
		return wrap(r.String())
	}
	var out bytes.Buffer
	sc := bufio.NewScanner(bytes.NewReader(body))
	// ServeHTTP caps a playlist body at 32 MiB, so allow a single line up to
	// that bound instead of silently truncating otherwise valid large manifests.
	sc.Buffer(make([]byte, 64<<10), 32<<20)
	for sc.Scan() {
		line := sc.Text()
		t := strings.TrimSpace(line)
		switch {
		case t == "":
		case strings.HasPrefix(t, "#"):
			line = uriAttr.ReplaceAllStringFunc(line, func(m string) string {
				return `URI="` + abs(uriAttr.FindStringSubmatch(m)[1]) + `"`
			})
		default:
			line = abs(t)
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.Bytes()
}

func (p *HeaderProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/p/"), "/")
	if len(parts) != 2 || r.URL.RawQuery != "" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	resource, ok := p.resource(parts[0])
	if !ok {
		http.Error(w, "expired or unknown stream", http.StatusNotFound)
		return
	}
	if parts[1] != resource.Name {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	target, h := resource.URL, resource.Headers
	tu, err := url.Parse(target)
	if err != nil || (tu.Scheme != "http" && tu.Scheme != "https") {
		http.Error(w, "bad authorized target", 502)
		return
	}
	req, _ := http.NewRequestWithContext(r.Context(), r.Method, target, nil)
	for k, v := range h {
		req.Header.Set(k, v)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", defaultMediaUserAgent)
	}
	// Leave content decoding to the HTTP transport. Forwarding this header
	// makes it pass gzip bytes through to players that expect media.
	req.Header.Del("Accept-Encoding")
	looksM3U8 := strings.HasSuffix(strings.ToLower(tu.Path), ".m3u8") || strings.HasSuffix(strings.ToLower(resource.Name), ".m3u8")
	if rg := r.Header.Get("Range"); rg != "" && !looksM3U8 {
		req.Header.Set("Range", rg)
	}
	c := p.Client
	if c == nil {
		c = http.DefaultClient
	}
	client := *c
	initial := *tu
	previousCheck := c.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if previousCheck != nil {
			if err := previousCheck(next, via); err != nil {
				return err
			}
		} else if len(via) >= 10 {
			return http.ErrUseLastResponse
		}
		if !resource.AllowLAN && !sameOrigin(&initial, next.URL) && restrictedNetworkURL(next.URL.String()) {
			return fmt.Errorf("redirect to local/LAN address blocked")
		}
		return nil
	}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, "upstream request failed", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Access-Control-Allow-Origin", "*")
	bodyReader := bufio.NewReader(resp.Body)
	prefix, _ := bodyReader.Peek(64)
	ct := resp.Header.Get("Content-Type")
	isM3U8 := strings.Contains(strings.ToLower(ct), "mpegurl") || looksM3U8 || playlistMagic(prefix)
	if isM3U8 && (resp.StatusCode == 200 || resp.StatusCode == 206) {
		body, err := io.ReadAll(io.LimitReader(bodyReader, 32<<20))
		if err != nil {
			http.Error(w, "upstream request failed", http.StatusBadGateway)
			return
		}
		if playlistMagic(body) {
			body = bytes.TrimPrefix(bytes.TrimSpace(body), []byte{0xEF, 0xBB, 0xBF})
			base := resp.Request.URL
			if base == nil {
				base = tu
			}
			blocked := false
			body = RewritePlaylist(body, base, func(s string) string {
				resolved, _ := url.Parse(s)
				// sameOrigin is checked first so a long VOD playlist does not
				// DNS-lookup every segment that stays on the playlist host.
				if !resource.AllowLAN && !sameOrigin(tu, resolved) && restrictedNetworkURL(s) {
					blocked = true
					return "about:invalid#blocked-lan-resource"
				}
				wrapped := p.wrapSessionAuthorized(s, h, resource.Session, segName(s), resource.AllowLAN)
				if wrapped == "" {
					blocked = true
				}
				return wrapped
			})
			if blocked {
				http.Error(w, "playlist references a local/LAN resource without authorization", http.StatusForbidden)
				return
			}
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Write(body)
			return
		}
		w.Header().Set("Content-Type", ct)
		w.Write(body)
		return
	}
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, bodyReader)
}

// playlistMagic reports an HLS playlist, including a leading UTF-8 BOM.
func playlistMagic(b []byte) bool {
	b = bytes.TrimSpace(b)
	b = bytes.TrimPrefix(b, []byte{0xEF, 0xBB, 0xBF})
	b = bytes.TrimSpace(b)
	return bytes.HasPrefix(b, []byte("#EXTM3U"))
}

func sameOrigin(a, b *url.URL) bool {
	if a == nil || b == nil {
		return false
	}
	return strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host)
}

func restrictedNetworkURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	ip := net.ParseIP(host)
	restricted := func(ip net.IP) bool {
		return ip != nil && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast())
	}
	if ip != nil {
		return restricted(ip)
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	return lookupRestrictedHost(host)
}

// lookupRestrictedHost caches DNS answers briefly. Rewriting one HLS
// playlist otherwise looks the host up once per segment and can exceed the
// playback preflight deadline on a long movie.
type hostRestrict struct {
	restricted bool
	until      time.Time
}

var (
	hostRestrictMu    sync.Mutex
	hostRestrictCache = map[string]hostRestrict{}
)

func lookupRestrictedHost(host string) bool {
	now := time.Now()
	hostRestrictMu.Lock()
	if entry, ok := hostRestrictCache[host]; ok && now.Before(entry.until) {
		hostRestrictMu.Unlock()
		return entry.restricted
	}
	hostRestrictMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	restrictedHost := false
	if err == nil {
		for _, candidate := range ips {
			if candidate != nil && (candidate.IsLoopback() || candidate.IsPrivate() || candidate.IsLinkLocalUnicast() || candidate.IsLinkLocalMulticast()) {
				restrictedHost = true
				break
			}
		}
	}
	hostRestrictMu.Lock()
	if len(hostRestrictCache) > 512 {
		hostRestrictCache = map[string]hostRestrict{}
	}
	hostRestrictCache[host] = hostRestrict{restricted: restrictedHost, until: time.Now().Add(30 * time.Second)}
	hostRestrictMu.Unlock()
	return restrictedHost
}

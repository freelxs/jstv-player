package live

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxSubscriptionBytes = 16 << 20
const maxReferences = 32

type Loader struct {
	// Client may be supplied by tests. The default client pins DNS resolution
	// at connect time and checks every redirect against the LAN setting.
	Client   *http.Client
	AllowLAN bool
}

func (l Loader) Load(ctx context.Context, raw string) (*Catalog, error) {
	if l.Client == nil {
		l.Client = l.httpClient()
		defer l.Client.CloseIdleConnections()
	}
	catalog := &Catalog{URL: raw}
	seen := map[string]bool{}
	if err := l.load(ctx, raw, nil, "", catalog, seen, 0); err != nil {
		return nil, err
	}
	catalog.Channels = deduplicate(catalog.Channels)
	if len(catalog.Channels) == 0 {
		return nil, errors.New("订阅未返回可用的直播频道；请检查直播地址或网络")
	}
	return catalog, nil
}

func (l Loader) load(ctx context.Context, raw string, headers map[string]string, group string, out *Catalog, seen map[string]bool, depth int) error {
	if depth > 4 || len(seen) >= maxReferences {
		return errors.New("直播订阅引用过多或嵌套过深")
	}
	keyBytes, _ := json.Marshal([]any{raw, headers})
	key := string(keyBytes)
	if seen[key] {
		return nil
	}
	seen[key] = true
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("直播订阅需要 HTTP(S) 地址")
	}
	if err := validateHeaders(headers); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "okhttp/4.12.0 JSTVPlayer")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := l.Client
	if client == nil {
		client = l.httpClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("获取直播列表失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("直播列表请求失败（HTTP %d）", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSubscriptionBytes+1))
	if err != nil {
		return err
	}
	if len(data) > maxSubscriptionBytes {
		return errors.New("直播订阅超过 16 MiB")
	}
	data, err = unwrap(data)
	if err != nil {
		return err
	}
	base := resp.Request.URL
	if base == nil {
		base = u
	}
	if bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		return l.tvbox(ctx, data, base, group, out, seen, depth)
	}
	channels, err := Playlist(data, base)
	if err != nil {
		return err
	}
	for i := range channels {
		if group != "" && channels[i].Group == "默认" {
			channels[i].Group = group
		}
		merged := map[string]string{}
		for k, v := range headers {
			merged[http.CanonicalHeaderKey(k)] = v
		}
		for k, v := range channels[i].Headers {
			merged[k] = v
		}
		channels[i].Headers = merged
		channels[i].ID = channelID(channels[i])
	}
	out.Channels = append(out.Channels, channels...)
	if len(out.Channels) > MaxChannels {
		return errors.New("直播订阅频道过多")
	}
	return nil
}

type liveSource struct {
	Name     string            `json:"name"`
	URL      json.RawMessage   `json:"url"`
	Header   map[string]string `json:"header"`
	UA       string            `json:"ua"`
	Channels []struct {
		Name   string            `json:"name"`
		URLs   []string          `json:"urls"`
		URL    string            `json:"url"`
		Header map[string]string `json:"header"`
	} `json:"channels"`
}

func (l Loader) tvbox(ctx context.Context, data []byte, base *url.URL, group string, out *Catalog, seen map[string]bool, depth int) error {
	var config struct {
		Lives []liveSource `json:"lives"`
	}
	if err := json.Unmarshal(jsonData(data), &config); err != nil {
		return fmt.Errorf("TVBox 配置无效: %w", err)
	}
	if len(config.Lives) == 0 {
		return errors.New("TVBox 配置没有 lives 直播定义；点播 spiders 不会执行")
	}
	if len(config.Lives) > maxReferences {
		return errors.New("TVBox 直播源过多")
	}
	for _, source := range config.Lives {
		if err := ctx.Err(); err != nil {
			return err
		}
		headers := map[string]string{}
		for k, v := range source.Header {
			headers[http.CanonicalHeaderKey(k)] = v
		}
		if source.UA != "" {
			headers["User-Agent"] = source.UA
		}
		if err := validateHeaders(headers); err != nil {
			return err
		}
		for _, item := range source.Channels {
			urls := item.URLs
			if item.URL != "" {
				urls = append(urls, item.URL)
			}
			for _, raw := range urls {
				u, h, err := mediaURL(raw, base)
				if err != nil {
					return err
				}
				for k, v := range headers {
					if _, ok := h[k]; !ok {
						h[k] = v
					}
				}
				for k, v := range item.Header {
					h[http.CanonicalHeaderKey(k)] = v
				}
				if err := validateHeaders(h); err != nil {
					return err
				}
				ch := Channel{Name: item.Name, Group: source.Name, URL: u, Headers: h}
				ch.ID = channelID(ch)
				out.Channels = append(out.Channels, ch)
				if len(out.Channels) > MaxChannels {
					return errors.New("直播频道过多")
				}
			}
		}
		if len(source.URL) == 0 {
			continue
		}
		var raw string
		if err := json.Unmarshal(source.URL, &raw); err != nil {
			out.Warnings = append(out.Warnings, source.Name+": 不支持此直播 URL 结构")
			continue
		}
		target := resolveURL(base, raw)
		if err := l.load(ctx, target, headers, source.Name, out, seen, depth+1); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			out.Warnings = append(out.Warnings, source.Name+": "+err.Error())
		}
	}
	return nil
}

// unwrap supports the public reference's JPEG + marker + base64 JSON wrapper.
// It decodes bytes only. No script, JAR, expression, or encrypted code runs.
func unwrap(data []byte) ([]byte, error) {
	data = bytes.TrimPrefix(bytes.TrimSpace(data), []byte{0xef, 0xbb, 0xbf})
	if bytes.HasPrefix(data, []byte{0xff, 0xd8}) {
		end := bytes.Index(data, []byte{0xff, 0xd9})
		if end < 0 || end > 1<<20 {
			return nil, errors.New("图片外壳配置无效")
		}
		payload := data[end+2:]
		marker := bytes.Index(payload, []byte("**"))
		if marker < 0 || marker > 64 {
			return nil, errors.New("未识别的图片配置外壳")
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(payload[marker+2:])))
		if err != nil {
			return nil, errors.New("图片配置 Base64 无效")
		}
		return decoded, nil
	}
	return data, nil
}

// jsonData removes comments and trailing commas outside JSON strings. It does
// not implement JSON5 expressions, identifiers, or executable configuration.
func jsonData(data []byte) []byte {
	out := make([]byte, 0, len(data))
	quoted, escaped := false, false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if quoted {
			out = append(out, c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
			out = append(out, c)
			continue
		}
		if c == '/' && i+1 < len(data) {
			if data[i+1] == '/' {
				for i < len(data) && data[i] != '\n' {
					i++
				}
				out = append(out, '\n')
				continue
			}
			if data[i+1] == '*' {
				i += 2
				for i+1 < len(data) && !(data[i] == '*' && data[i+1] == '/') {
					i++
				}
				i++
				out = append(out, ' ')
				continue
			}
		}
		out = append(out, c)
	}
	clean := make([]byte, 0, len(out))
	quoted, escaped = false, false
	for i, c := range out {
		if quoted {
			clean = append(clean, c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				quoted = false
			}
			continue
		}
		if c == '"' {
			quoted = true
		}
		if c == ',' {
			j := i + 1
			for j < len(out) && strings.ContainsRune(" \r\n\t", rune(out[j])) {
				j++
			}
			if j < len(out) && (out[j] == '}' || out[j] == ']') {
				continue
			}
		}
		clean = append(clean, c)
	}
	return clean
}

func (l Loader) httpClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Do not let an environment proxy move the LAN decision to another machine.
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		var last error
		for _, entry := range ips {
			ip := entry.IP
			if !l.AllowLAN && (ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() || ip.IsMulticast()) {
				return nil, errors.New("直播列表指向本机或局域网；请先允许局域网媒体")
			}
			conn, err := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			last = err
		}
		if last == nil {
			last = errors.New("直播源域名没有可用地址")
		}
		return nil, last
	}
	return &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("直播订阅重定向过多")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return errors.New("直播订阅重定向协议无效")
		}
		return nil
	}}
}

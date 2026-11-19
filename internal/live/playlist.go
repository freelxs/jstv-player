// Package live reads IPTV subscriptions as data. It does not load TVBox
// spiders, evaluate JavaScript, or translate a third-party implementation.
package live

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"
)

const MaxChannels = 50000

// Line is one playable address of a channel. Channels that share a group
// and name keep every distinct address so playback can switch lines.
type Line struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
}

type Channel struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Group   string            `json:"group"`
	Logo    string            `json:"logo,omitempty"`
	TVGID   string            `json:"tvgId,omitempty"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Lines   []Line            `json:"lines,omitempty"`
}

type Catalog struct {
	URL      string    `json:"url"`
	Channels []Channel `json:"channels"`
	Warnings []string  `json:"warnings,omitempty"`
}

var attribute = regexp.MustCompile(`([A-Za-z0-9_-]+)\s*=\s*(?:"([^"]*)"|'([^']*)')`)

// Playlist distinguishes channel lists from HLS media/master manifests before
// considering EXTINF entries. Relative URLs use the final response URL.
func Playlist(data []byte, base *url.URL) ([]Channel, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("直播列表需要 UTF-8 编码")
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#EXT-X-") {
			return nil, errors.New("这是 HLS 播放清单，请添加频道订阅列表")
		}
	}
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	group := "默认"
	var pending *Channel
	var channels []Channel
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "#EXTINF:"):
			comma := unquotedComma(line)
			if comma < 0 {
				return nil, errors.New("EXTINF 缺少频道名称分隔符")
			}
			pending = &Channel{Name: strings.TrimSpace(line[comma+1:]), Group: group, Headers: map[string]string{}}
			for _, pair := range attribute.FindAllStringSubmatch(line[:comma], -1) {
				value := pair[2]
				if value == "" {
					value = pair[3]
				}
				switch strings.ToLower(pair[1]) {
				case "group-title":
					pending.Group = value
				case "tvg-logo":
					pending.Logo = resolveURL(base, value)
				case "tvg-id":
					pending.TVGID = value
				}
			}
		case strings.HasPrefix(line, "#EXTGRP:"):
			group = strings.TrimSpace(strings.TrimPrefix(line, "#EXTGRP:"))
			if pending != nil {
				pending.Group = group
			}
		case strings.HasPrefix(line, "#EXTVLCOPT:"):
			if pending == nil {
				pending = &Channel{Group: group, Headers: map[string]string{}}
			}
			key, value, ok := strings.Cut(strings.TrimPrefix(line, "#EXTVLCOPT:"), "=")
			if ok {
				switch strings.ToLower(key) {
				case "http-user-agent":
					pending.Headers["User-Agent"] = value
				case "http-referrer", "http-referer":
					pending.Headers["Referer"] = value
				}
			}
		case strings.HasPrefix(line, "#EXTHTTP:"):
			if pending == nil {
				pending = &Channel{Group: group, Headers: map[string]string{}}
			}
			var extra map[string]string
			if json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "#EXTHTTP:"))), &extra) == nil {
				for k, v := range extra {
					pending.Headers[http.CanonicalHeaderKey(k)] = v
				}
			}
		case strings.HasPrefix(line, "#"):
			continue
		default:
			name, raw, txt := strings.Cut(line, ",")
			if txt && strings.TrimSpace(raw) == "#genre#" {
				group = strings.TrimSpace(name)
				pending = nil
				continue
			}
			if pending == nil && !txt {
				continue
			}
			ch := Channel{Group: group, Headers: map[string]string{}}
			if pending != nil {
				ch = *pending
				raw = line
			} else {
				ch.Name = strings.TrimSpace(name)
			}
			pending = nil
			u, h, err := mediaURL(raw, base)
			if err != nil {
				return nil, err
			}
			ch.URL = u
			for k, v := range h {
				ch.Headers[k] = v
			}
			if ch.Name == "" {
				parsed, _ := url.Parse(u)
				ch.Name = path.Base(parsed.Path)
			}
			if ch.Group == "" {
				ch.Group = "默认"
			}
			if err := validateHeaders(ch.Headers); err != nil {
				return nil, err
			}
			ch.ID = channelID(ch)
			channels = append(channels, ch)
			if len(channels) > MaxChannels {
				return nil, fmt.Errorf("直播频道超过 %d 项", MaxChannels)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("直播列表行过长: %w", err)
	}
	if len(channels) == 0 {
		return nil, errors.New("未识别到直播频道，请使用 M3U、TXT 或含 lives 的 TVBox 配置")
	}
	return deduplicate(channels), nil
}

func unquotedComma(s string) int {
	var quote rune
	for i, c := range s {
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == ',' {
			return i
		}
	}
	return -1
}

func resolveURL(base *url.URL, raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	return u.String()
}

func mediaURL(raw string, base *url.URL) (string, map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if target, _, ok := unwrapLiveProxy(raw); ok {
		raw = target
	}
	address, tail, hasHeaders := strings.Cut(raw, "|")
	u := resolveURL(base, address)
	parsed, err := url.Parse(u)
	if err != nil || parsed.Scheme == "" {
		return "", nil, errors.New("频道地址无效")
	}
	// Preserve unsupported media schemes for an explicit player error later.
	if parsed.Scheme != "http" && parsed.Scheme != "https" && parsed.Scheme != "rtsp" && parsed.Scheme != "rtp" && parsed.Scheme != "udp" {
		return "", nil, errors.New("频道协议不受支持")
	}
	if parsed.Scheme == "http" || parsed.Scheme == "https" {
		if parsed.Host == "" || parsed.User != nil {
			return "", nil, errors.New("频道 HTTP 地址无效，请通过 headers 提供认证")
		}
	}
	headers := map[string]string{}
	if hasHeaders {
		q, err := url.ParseQuery(tail)
		if err != nil {
			return "", nil, errors.New("频道附加请求头无效")
		}
		for k, v := range q {
			if len(v) > 0 {
				headers[http.CanonicalHeaderKey(k)] = v[len(v)-1]
			}
		}
	}
	return u, headers, validateHeaders(headers)
}

func validateHeaders(headers map[string]string) error {
	for key, value := range headers {
		if key == "" || strings.ContainsAny(value, "\r\n\x00") {
			return errors.New("频道请求头无效")
		}
		for _, c := range key {
			if c <= 32 || c >= 127 || strings.ContainsRune("()<>@,;:\\\"/[]?={}", c) {
				return errors.New("频道请求头名称无效")
			}
		}
		switch strings.ToLower(key) {
		case "host", "connection", "transfer-encoding", "content-length", "upgrade", "proxy-authorization", "proxy-connection", "trailer", "te":
			return errors.New("频道包含禁止转发的请求头")
		}
	}
	return nil
}

func channelID(ch Channel) string {
	b, _ := json.Marshal([]any{ch.Group, ch.Name})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

func lineKey(url string, headers map[string]string) string {
	b, _ := json.Marshal([]any{url, headers})
	return string(b)
}

// deduplicate merges channels with the same group and name into one channel
// with multiple lines. An identical URL and header set is kept once.
func deduplicate(channels []Channel) []Channel {
	index := map[string]int{}
	out := make([]Channel, 0, len(channels))
	for _, ch := range channels {
		lines := ch.Lines
		if len(lines) == 0 && ch.URL != "" {
			lines = []Line{{URL: ch.URL, Headers: ch.Headers}}
		}
		key := ch.Group + "\x00" + ch.Name
		i, ok := index[key]
		if !ok {
			ch.Lines = nil
			ch.ID = channelID(ch)
			index[key] = len(out)
			out = append(out, ch)
			i = len(out) - 1
		}
		seen := map[string]bool{}
		for _, line := range out[i].Lines {
			seen[lineKey(line.URL, line.Headers)] = true
		}
		for _, line := range lines {
			if line.URL == "" || seen[lineKey(line.URL, line.Headers)] {
				continue
			}
			seen[lineKey(line.URL, line.Headers)] = true
			out[i].Lines = append(out[i].Lines, line)
		}
		if len(out[i].Lines) > 0 {
			out[i].URL = out[i].Lines[0].URL
			out[i].Headers = out[i].Lines[0].Headers
		}
	}
	return out
}

// unwrapLiveProxy reads proxy://do=live and /proxy?do=live wrappers.
// The url or ext value may be an HTTP address or standard base64 of one.
func unwrapLiveProxy(raw string) (target, kind string, ok bool) {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if !strings.Contains(lower, "do=live") {
		return "", "", false
	}
	query := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(lower, "proxy://"):
		query = raw[len("proxy://"):]
	default:
		u, err := url.Parse(raw)
		if err != nil || !strings.Contains(strings.ToLower(u.Path), "proxy") {
			return "", "", false
		}
		query = u.RawQuery
	}
	values, err := url.ParseQuery(query)
	if err != nil || values.Get("do") != "live" {
		return "", "", false
	}
	kind = strings.ToLower(values.Get("type"))
	target = strings.TrimSpace(values.Get("url"))
	if target == "" {
		target = strings.TrimSpace(values.Get("ext"))
	}
	if decoded, ok2 := decodeProxyTarget(target); ok2 {
		target = decoded
	}
	if target == "" || strings.ContainsAny(target, "\r\n") {
		return "", "", false
	}
	return target, kind, true
}

func isLiveList(kind, raw string) bool {
	switch strings.ToLower(kind) {
	case "txt", "text", "m3u":
		return true
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	p := strings.ToLower(u.Path)
	return strings.HasSuffix(p, ".txt") || strings.HasSuffix(p, ".m3u")
}

func decodeProxyTarget(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "://") {
		return "", false
	}
	if m := len(s) % 4; m != 0 {
		s += strings.Repeat("=", 4-m)
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) == 0 || bytesHasCtl(b) {
		return "", false
	}
	return strings.TrimSpace(string(b)), true
}

func bytesHasCtl(b []byte) bool {
	for _, c := range b {
		if c < 0x20 && c != '\t' {
			return true
		}
	}
	return false
}

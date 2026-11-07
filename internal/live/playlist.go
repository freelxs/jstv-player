// Package live reads IPTV subscriptions as data. It does not load TVBox
// spiders, evaluate JavaScript, or translate a third-party implementation.
package live

import (
	"bufio"
	"crypto/sha256"
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

type Channel struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Group   string            `json:"group"`
	Logo    string            `json:"logo,omitempty"`
	TVGID   string            `json:"tvgId,omitempty"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
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
	address, tail, hasHeaders := strings.Cut(strings.TrimSpace(raw), "|")
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
	b, _ := json.Marshal([]any{ch.Group, ch.Name, ch.URL, ch.Headers})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:16])
}

func deduplicate(channels []Channel) []Channel {
	seen := map[string]bool{}
	out := make([]Channel, 0, len(channels))
	for _, ch := range channels {
		if !seen[ch.ID] {
			seen[ch.ID] = true
			out = append(out, ch)
		}
	}
	return out
}

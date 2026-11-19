package tvbox

import (
	"net/url"
	"strings"
)

// Channel is one live entry from an M3U or TVBox TXT playlist.
type Channel struct {
	Name    string
	Group   string
	URL     string
	Logo    string
	Headers map[string]string
}

// ParsePlaylist reads an M3U playlist or a TVBox text list
// (`group,#genre#` then `name,url`). At least one channel is required.
func ParsePlaylist(text, origin string, headers map[string]string) ([]Channel, error) {
	text = strings.TrimPrefix(text, "\uFEFF")
	lines := strings.Split(text, "\n")
	m3u := false
	for _, raw := range lines {
		if strings.HasPrefix(strings.TrimSpace(raw), "#EXTINF:") {
			m3u = true
			break
		}
	}
	var out []Channel
	seen := map[string]bool{}
	var name, group, logo string
	entry := cloneHeader(headers)
	for _, raw := range lines {
		line := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if line == "" {
			continue
		}
		switch {
		case strings.HasPrefix(line, "#EXTINF:"):
			name = extinfTitle(line)
			if name == "" {
				name = "未命名频道"
			}
			if g := attr(line, "group-title"); g != "" {
				group = g
			}
			if l := attr(line, "tvg-logo"); l != "" {
				if u, err := resolveRef(origin, l); err == nil {
					logo = u
				}
			}
			entry = cloneHeader(headers)
		case strings.HasPrefix(line, "#EXTGRP:"):
			group = strings.TrimSpace(strings.TrimPrefix(line, "#EXTGRP:"))
		case strings.HasPrefix(line, "#EXTVLCOPT:"):
			k, v, ok := strings.Cut(strings.TrimPrefix(line, "#EXTVLCOPT:"), "=")
			if ok {
				switch strings.TrimSpace(k) {
				case "http-user-agent":
					entry["User-Agent"] = strings.TrimSpace(v)
				case "http-referrer":
					entry["Referer"] = strings.TrimSpace(v)
				}
			}
		case strings.HasPrefix(line, "#"):
		default:
			address := line
			if !m3u {
				left, right, ok := strings.Cut(line, ",")
				if !ok {
					continue
				}
				right = strings.TrimSpace(right)
				if right == "#genre#" {
					group = strings.TrimSpace(left)
					continue
				}
				name = strings.TrimSpace(left)
				address = right
				entry = cloneHeader(headers)
			}
			ch, ok := splitChannel(origin, name, group, logo, address, entry)
			if !ok {
				name, logo = "", ""
				entry = cloneHeader(headers)
				continue
			}
			if !seen[ch.Group+"|"+ch.Name+"|"+ch.URL] {
				seen[ch.Group+"|"+ch.Name+"|"+ch.URL] = true
				out = append(out, ch)
			}
			name, logo = "", ""
			entry = cloneHeader(headers)
		}
	}
	if len(out) == 0 {
		return nil, errNotConfig
	}
	return out, nil
}

func splitChannel(origin, name, group, logo, address string, headers map[string]string) (Channel, bool) {
	media, extra, _ := strings.Cut(address, "|")
	u, err := resolveRef(origin, strings.TrimSpace(media))
	if err != nil {
		return Channel{}, false
	}
	h := cloneHeader(headers)
	if extra != "" {
		for _, pair := range strings.Split(extra, "&") {
			k, v, ok := strings.Cut(pair, "=")
			if !ok {
				continue
			}
			if decoded, err := url.QueryUnescape(v); err == nil {
				v = decoded
			}
			h[k] = v
		}
	}
	if strings.TrimSpace(name) == "" {
		if pu, err := url.Parse(u); err == nil && pu.Path != "" {
			name = pu.Path[strings.LastIndex(pu.Path, "/")+1:]
		}
		if name == "" {
			name = "未命名频道"
		}
	}
	return Channel{Name: name, Group: group, URL: u, Logo: logo, Headers: h}, true
}

func extinfTitle(line string) string {
	quoted := false
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '"':
			quoted = !quoted
		case ',':
			if !quoted {
				return strings.TrimSpace(line[i+1:])
			}
		}
	}
	return ""
}

func attr(line, key string) string {
	needle := key + `="`
	i := strings.Index(line, needle)
	if i < 0 {
		return ""
	}
	rest := line[i+len(needle):]
	j := strings.IndexByte(rest, '"')
	if j < 0 {
		return ""
	}
	return rest[:j]
}

func cloneHeader(h map[string]string) map[string]string {
	if len(h) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[k] = v
	}
	return out
}

package tvbox

import (
	"regexp"
	"strings"
)

// ApplyPlaylistRules removes the simple host-scoped patterns from an HLS
// playlist. Patterns that do not compile are ignored. Matching is a substring
// of the hostname, which covers the short host fragments TVBox configs use.
func ApplyPlaylistRules(rules []Rule, host string, body []byte) []byte {
	if len(rules) == 0 || len(body) == 0 {
		return body
	}
	host = strings.ToLower(host)
	s := string(body)
	for _, rule := range rules {
		if !ruleMatches(rule.Hosts, host) {
			continue
		}
		for _, expr := range rule.Regex {
			re, err := regexp.Compile(expr)
			if err != nil {
				continue
			}
			s = re.ReplaceAllString(s, "")
		}
	}
	return []byte(s)
}

func ruleMatches(hosts []string, host string) bool {
	for _, h := range hosts {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			continue
		}
		if host == h || strings.Contains(host, h) || strings.Contains(h, host) {
			return true
		}
	}
	return false
}

// RewriteDial replaces a dial address hostname when the subscription hosts
// map names another host or IP. The port is kept so the HTTP Host header can
// stay on the original name.
func RewriteDial(addr string, hosts map[string]string) string {
	if len(hosts) == 0 {
		return addr
	}
	host, port, ok := strings.Cut(addr, ":")
	if !ok {
		return addr
	}
	// IPv6 addresses contain extra colons. Only rewrite host:port.
	if strings.Count(addr, ":") != 1 {
		return addr
	}
	if repl, ok := hosts[strings.ToLower(host)]; ok && repl != "" {
		return repl + ":" + port
	}
	return addr
}

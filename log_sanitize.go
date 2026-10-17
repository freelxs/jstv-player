package main

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	logURLRE      = regexp.MustCompile(`(?i)https?://[^\s"'<>]+`)
	logAuthRE     = regexp.MustCompile(`(?i)(authorization|cookie|set-cookie|proxy-authorization)(\s*[:=]\s*)([^,}\]]+)`)
	secretQueryRE = regexp.MustCompile(`(?i)(token|access_token|refresh_token|key|api_key|apikey|sig|signature|password|passwd|auth|secret)=([^&#\s]+)`)
	logLineBreaks = strings.NewReplacer("\r\n", " ", "\r", " ", "\n", " ")
)

// sanitizeLogLine removes common credential forms before they enter the
// in-memory log ring. Unknown plugin-defined secret formats cannot be
// reliably identified and must not be considered fully redacted.
func sanitizeLogLine(s string) string {
	s = logURLRE.ReplaceAllStringFunc(s, func(raw string) string {
		trail := ""
		for len(raw) > 0 && strings.ContainsRune("),.;]}", rune(raw[len(raw)-1])) {
			trail = raw[len(raw)-1:] + trail
			raw = raw[:len(raw)-1]
		}
		u, err := url.Parse(raw)
		if err == nil {
			if u.User != nil {
				u.User = url.UserPassword("REDACTED", "REDACTED")
			}
			q := u.Query()
			for k := range q {
				if isSecretName(k) {
					q.Set(k, "REDACTED")
				}
			}
			u.RawQuery = q.Encode()
			raw = u.String()
		}
		return raw + trail
	})
	s = secretQueryRE.ReplaceAllString(s, "$1=REDACTED")
	s = logAuthRE.ReplaceAllString(s, "$1$2REDACTED")
	if len(s) > 8192 {
		s = s[:8192] + "…[截断]"
	}
	s = strings.Map(func(r rune) rune {
		if r == 0 {
			return -1
		}
		return r
	}, s)
	return logLineBreaks.Replace(s)
}

func isSecretName(k string) bool {
	k = strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(k, "-", "_"), ".", "_"))
	for _, part := range []string{"token", "key", "sig", "signature", "password", "passwd", "auth", "secret", "credential"} {
		if strings.Contains(k, part) {
			return true
		}
	}
	return false
}

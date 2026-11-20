// Package netx normalizes request URLs before they are dialed.
package netx

import (
	"net"
	"net/url"
	"strings"

	"golang.org/x/net/idna"
)

// ASCIIURL rewrites an internationalized host to punycode. Paths, queries,
// and already-ASCII hosts are left intact. Relative references are returned
// unchanged.
func ASCIIURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	u, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if u.Host == "" {
		return raw, nil
	}
	host := u.Hostname()
	ascii, err := idna.Lookup.ToASCII(host)
	if err != nil {
		return "", err
	}
	if port := u.Port(); port != "" {
		u.Host = net.JoinHostPort(ascii, port)
	} else {
		u.Host = ascii
	}
	return u.String(), nil
}

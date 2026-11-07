package webserver

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (s *webServer) serveMedia(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	if s.runtime.MediaProxy == nil {
		http.Error(w, "播放代理未启动", http.StatusBadGateway)
		return
	}
	base := strings.TrimRight(s.runtime.MediaProxy.BaseURL(), "/")
	rest := strings.TrimPrefix(r.URL.Path, "/media")
	if base == "" || !strings.HasPrefix(rest, "/p/") || strings.Contains(rest, "..") {
		http.NotFound(w, r)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), r.Method, base+rest, nil)
	if err != nil {
		http.Error(w, "播放地址无效", http.StatusBadGateway)
		return
	}
	if rg := r.Header.Get("Range"); rg != "" {
		req.Header.Set("Range", rg)
	}
	resp, err := s.media.Do(req)
	if err != nil {
		http.Error(w, "播放代理不可用", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusForbidden {
		http.Error(w, "播放地址已失效，请重新点播", resp.StatusCode)
		return
	}
	ct := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(ct, "mpegurl") && (resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusPartialContent) {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if err != nil {
			http.Error(w, "播放列表读取失败", http.StatusBadGateway)
			return
		}
		body = rewriteMediaBody(base, body)
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "private, no-store")
		if r.Method != http.MethodHead {
			_, _ = w.Write(body)
		}
		return
	}
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges"} {
		if v := resp.Header.Get(k); v != "" {
			w.Header().Set(k, v)
		}
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(resp.StatusCode)
	if r.Method != http.MethodHead {
		_, _ = io.Copy(w, resp.Body)
	}
}

func rewriteMediaBody(proxyBase string, body []byte) []byte {
	proxyBase = strings.TrimRight(proxyBase, "/")
	if proxyBase == "" {
		return body
	}
	return bytes.ReplaceAll(body, []byte(proxyBase), []byte("/media"))
}

func (s *webServer) servePoster(w http.ResponseWriter, r *http.Request) {
	raw := r.URL.Query().Get("u")
	if err := validatePosterURL(raw, s.runtime.PosterHost()); err != nil {
		http.Error(w, "拒绝加载该图片", http.StatusForbidden)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, raw, nil)
	if err != nil {
		http.Error(w, "拒绝加载该图片", http.StatusForbidden)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
	resp, err := s.posters.Do(req)
	if err != nil {
		http.Error(w, "图片不可用", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		http.Error(w, "图片不可用", http.StatusBadGateway)
		return
	}
	buf := bufio.NewReader(io.LimitReader(resp.Body, 8<<20))
	sniff, _ := buf.Peek(32)
	ct := imageContentType(resp.Header.Get("Content-Type"), sniff)
	if ct == "" {
		http.Error(w, "不是图片", http.StatusUnsupportedMediaType)
		return
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Cache-Control", "private, max-age=3600")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = io.Copy(w, buf)
}

func validatePosterURL(raw, pluginHost string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return errors.New("bad url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("bad scheme")
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		if u.Host != pluginHost {
			return errors.New("blocked")
		}
		return nil
	}
	if ip := net.ParseIP(host); ip != nil && blockedPosterIP(ip) && u.Host != pluginHost {
		return errors.New("blocked")
	}
	return nil
}

func (s *webServer) posterTransport() *http.Transport {
	dialer := &net.Dialer{Timeout: 8 * time.Second}
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, err
			}
			if allow := s.runtime.PosterHost(); allow != "" && net.JoinHostPort(host, port) == allow {
				return dialer.DialContext(ctx, network, addr)
			}
			ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
			if err != nil || len(ips) == 0 {
				if err == nil {
					err = errors.New("no address")
				}
				return nil, err
			}
			for _, ip := range ips {
				if blockedPosterIP(ip.IP) {
					return nil, errors.New("blocked address")
				}
			}
			return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
		},
		ResponseHeaderTimeout: 15 * time.Second,
		IdleConnTimeout:       30 * time.Second,
		MaxIdleConns:          8,
	}
}

func blockedPosterIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsInterfaceLocalMulticast()
}

func imageContentType(header string, sniff []byte) string {
	ct := strings.ToLower(strings.TrimSpace(strings.Split(header, ";")[0]))
	switch {
	case bytes.HasPrefix(sniff, []byte{0xFF, 0xD8, 0xFF}):
		return "image/jpeg"
	case bytes.HasPrefix(sniff, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}):
		return "image/png"
	case bytes.HasPrefix(sniff, []byte("GIF87a")) || bytes.HasPrefix(sniff, []byte("GIF89a")):
		return "image/gif"
	case len(sniff) >= 12 && bytes.HasPrefix(sniff, []byte("RIFF")) && bytes.Equal(sniff[8:12], []byte("WEBP")):
		return "image/webp"
	case ct == "image/jpeg" || ct == "image/png" || ct == "image/gif" || ct == "image/webp":
		return ct
	default:
		return ""
	}
}

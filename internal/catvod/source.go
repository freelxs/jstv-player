package catvod

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Source is a downloaded plugin bundle.
type Source struct {
	URL        string // what the user entered
	ScriptURL  string // the index.js actually fetched
	Path       string // local copy
	ConfigPath string // local index.config.js, if published
	MD5        string // md5 of the local copy
	SHA256     string // local SHA-256 for TOFU change detection
	Expected   string // md5 published next to it (index.js.md5), if any
}

// MD5Mismatch reports whether a published checksum exists and differs.
// Many published .md5 files lag behind their index.js, so callers should
// treat this as a warning.
func (s *Source) MD5Mismatch() bool { return s.Expected != "" && !strings.EqualFold(s.Expected, s.MD5) }

var md5re = regexp.MustCompile(`(?i)\b[0-9a-f]{32}\b`)

// ScriptURLs maps a user-entered source URL to (index.js URL, md5 URL).
// "…/index.js.md5" → ("…/index.js", "…/index.js.md5"); "…/index.js" →
// ("…/index.js", "…/index.js.md5"); a directory URL gets "index.js" appended.
func ScriptURLs(u string) (script, sum string) {
	u = strings.TrimSpace(u)
	pu, err := url.Parse(u)
	if err != nil || pu.Scheme == "" || pu.Host == "" {
		return scriptURLsRaw(u)
	}
	path := pu.Path
	switch {
	case hasSuffixFold(path, ".md5"):
		pu.Path = trimSuffixFold(path, ".md5")
		pu.RawPath = ""
		return pu.String(), u
	case hasSuffixFold(path, ".js"):
		pu.Path = path + ".md5"
		pu.RawPath = ""
		return u, pu.String()
	default:
		if path == "" || !strings.HasSuffix(path, "/") {
			pu.Path = path + "/"
		}
		pu.RawPath = ""
		base := *pu
		base.Path += "index.js"
		sumURL := *pu
		sumURL.Path += "index.js.md5"
		return base.String(), sumURL.String()
	}
}

func scriptURLsRaw(u string) (script, sum string) {
	switch {
	case hasSuffixFold(u, ".md5"):
		return trimSuffixFold(u, ".md5"), u
	case hasSuffixFold(u, ".js"):
		return u, u + ".md5"
	default:
		if !strings.HasSuffix(u, "/") {
			u += "/"
		}
		return u + "index.js", u + "index.js.md5"
	}
}

func hasSuffixFold(s, suffix string) bool {
	return len(s) >= len(suffix) && strings.EqualFold(s[len(s)-len(suffix):], suffix)
}

func trimSuffixFold(s, suffix string) string {
	if hasSuffixFold(s, suffix) {
		return s[:len(s)-len(suffix)]
	}
	return s
}

// Fetch downloads the bundle into dir (cached under dir/source/index.js).
// Basic-auth credentials in the URL (http://user:pass@host/…) are honored.
func Fetch(client *http.Client, rawURL, dir string) (*Source, error) {
	return FetchWithOptions(client, rawURL, dir, false)
}

// FetchWithOptions downloads into same-directory temporary files, enforces
// byte limits, checks the publisher MD5 before publication when strictMD5 is
// enabled, then atomically replaces each completed artifact.
func FetchWithOptions(client *http.Client, rawURL, dir string, strictMD5 bool) (*Source, error) {
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	script, sumURL := ScriptURLs(rawURL)
	src := &Source{URL: rawURL, ScriptURL: script}
	if b, err := get(client, sumURL, 1<<10); err == nil {
		src.Expected = strings.ToLower(md5re.FindString(string(b)))
	}
	d := filepath.Join(dir, "source")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return nil, err
	}
	tmpScript, size, md5sum, sha256sum, err := downloadTemp(client, script, d, 256<<20, true)
	if err != nil {
		return nil, fmt.Errorf("下载 index.js 失败: %w", err)
	}
	defer os.Remove(tmpScript)
	if size < 20 {
		return nil, fmt.Errorf("index.js 内容异常（%d 字节）", size)
	}
	src.MD5, src.SHA256 = md5sum, sha256sum
	if strictMD5 && src.Expected != "" && src.MD5Mismatch() {
		return nil, fmt.Errorf("md5 校验失败（文件 %s ≠ 发布 %s）", src.MD5, src.Expected)
	}
	src.Path = filepath.Join(d, "index.js")
	cfgPath := filepath.Join(d, "index.config.js")
	// Stage the optional config before replacing either published file.
	configURL := strings.TrimSuffix(script, ".js") + ".config.js"
	tmpConfig, cfgSize, _, _, cfgErr := downloadTemp(client, configURL, d, 64<<20, false)
	removeConfig := false
	if cfgErr != nil && !errors.Is(cfgErr, errNotFound) {
		// Optional configuration retains the historical best-effort behavior for
		// network/server failures; a size violation is never silently accepted.
		if errors.Is(cfgErr, errTooLarge) {
			return nil, cfgErr
		}
		// Do not destroy the previous cache if script publication later fails.
		removeConfig = true
	}
	if cfgErr == nil {
		defer os.Remove(tmpConfig)
	}
	if errors.Is(cfgErr, errNotFound) || (cfgErr == nil && cfgSize == 0) {
		removeConfig = true
	}
	if err := replaceFile(tmpScript, src.Path); err != nil {
		return nil, err
	}
	if cfgErr == nil && cfgSize > 0 {
		if err := replaceFile(tmpConfig, cfgPath); err != nil {
			return nil, err
		}
		src.ConfigPath = cfgPath
	} else if removeConfig {
		_ = os.Remove(cfgPath)
	}
	_ = os.Remove(src.Path + ".mjs")
	return src, nil
}

var (
	errNotFound = errors.New("optional file not found")
	errTooLarge = errors.New("download exceeds size limit")
)

func downloadTemp(c *http.Client, u, dir string, limit int64, digest bool) (string, int64, string, string, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", 0, "", "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 JSTVPlayer")
	resp, err := c.Do(req)
	if err != nil {
		return "", 0, "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", 0, "", "", errNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return "", 0, "", "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, u)
	}
	tmp, err := os.CreateTemp(dir, ".download-*.tmp")
	if err != nil {
		return "", 0, "", "", err
	}
	name := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(name)
		}
	}()
	_ = tmp.Chmod(0o644)
	var md hash.Hash
	var sh hash.Hash
	var dst io.Writer = tmp
	if digest {
		md, sh = md5.New(), sha256.New()
		dst = io.MultiWriter(tmp, md, sh)
	}
	n, err := io.Copy(dst, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		tmp.Close()
		return "", 0, "", "", err
	}
	if n > limit {
		tmp.Close()
		return "", 0, "", "", fmt.Errorf("%w (%d bytes max)", errTooLarge, limit)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", 0, "", "", err
	}
	if err := tmp.Close(); err != nil {
		return "", 0, "", "", err
	}
	cleanup = false
	mdsum, shsum := "", ""
	if digest {
		m, s := md.Sum(nil), sh.Sum(nil)
		mdsum, shsum = hex.EncodeToString(m), hex.EncodeToString(s)
	}
	return name, n, mdsum, shsum, nil
}

func get(c *http.Client, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 JSTVPlayer")
	resp, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, u)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

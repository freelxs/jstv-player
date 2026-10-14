package catvod

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
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
	switch {
	case strings.HasSuffix(u, ".md5"):
		return strings.TrimSuffix(u, ".md5"), u
	case strings.HasSuffix(u, ".js"):
		return u, u + ".md5"
	default:
		if !strings.HasSuffix(u, "/") {
			u += "/"
		}
		return u + "index.js", u + "index.js.md5"
	}
}

// Fetch downloads the bundle into dir (cached under dir/source/index.js).
// Basic-auth credentials in the URL (http://user:pass@host/…) are honored.
func Fetch(client *http.Client, rawURL, dir string) (*Source, error) {
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	script, sumURL := ScriptURLs(rawURL)
	src := &Source{URL: rawURL, ScriptURL: script}
	if b, err := get(client, sumURL, 1<<10); err == nil {
		src.Expected = strings.ToLower(md5re.FindString(string(b)))
	}
	body, err := get(client, script, 256<<20)
	if err != nil {
		return nil, fmt.Errorf("下载 index.js 失败: %w", err)
	}
	if len(body) < 20 {
		return nil, fmt.Errorf("index.js 内容异常（%d 字节）", len(body))
	}
	sum := md5.Sum(body)
	src.MD5 = hex.EncodeToString(sum[:])
	d := filepath.Join(dir, "source")
	if err := os.MkdirAll(d, 0o755); err != nil {
		return nil, err
	}
	src.Path = filepath.Join(d, "index.js")
	_ = os.Remove(src.Path + ".mjs")
	if err := os.WriteFile(src.Path, body, 0o644); err != nil {
		return nil, err
	}
	// CatPaw passes module.exports of index.config.js (next to index.js) to
	// start(config).
	cfgPath := filepath.Join(d, "index.config.js")
	_ = os.Remove(cfgPath)
	if cfg, err := get(client, strings.TrimSuffix(script, ".js")+".config.js", 64<<20); err == nil && len(cfg) > 0 {
		if err := os.WriteFile(cfgPath, cfg, 0o644); err == nil {
			src.ConfigPath = cfgPath
		}
	}
	return src, nil
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

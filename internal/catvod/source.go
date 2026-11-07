package catvod

import (
	"context"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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

	"github.com/freelxs/jstv-player/internal/atomicfile"
)

// Source is a downloaded plugin bundle.
type Source struct {
	URL            string // what the user entered
	ScriptURL      string // the index.js actually fetched
	Path           string // local copy
	ConfigPath     string // local index.config.js, if published
	MD5            string // md5 of the local copy
	SHA256         string // local SHA-256 for TOFU change detection
	Expected       string // md5 published next to it (index.js.md5), if any
	ConfigSHA256   string // empty only when configuration is absent
	BundleSHA256   string // digest of the executable artifact manifest
	VersionName    string // immutable directory, normally the manifest digest
	ScriptETag     string
	ScriptModified string
	ConfigETag     string
	ConfigModified string
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

// ConfigURL derives the optional sibling without changing query credentials.
func ConfigURL(script string) (string, error) {
	u, err := url.Parse(script)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("订阅需要 HTTP(S) 地址")
	}
	u.Path = trimSuffixFold(u.Path, ".js") + ".config.js"
	u.RawPath, u.Fragment = "", ""
	return u.String(), nil
}

// Fetch stages a complete immutable executable version. It never changes the
// last successfully started version; PublishSource commits that pointer later.
func Fetch(client *http.Client, rawURL, dir string) (*Source, error) {
	return FetchWithOptions(client, rawURL, dir, false)
}

func FetchWithOptions(client *http.Client, rawURL, dir string, strictMD5 bool) (*Source, error) {
	return FetchContext(context.Background(), client, rawURL, dir, strictMD5)
}

func FetchContext(ctx context.Context, client *http.Client, rawURL, dir string, strictMD5 bool) (*Source, error) {
	return FetchRefreshContext(ctx, client, rawURL, dir, strictMD5, false)
}

// FetchRefreshContext conditionally refreshes both executable artifacts. A
// force refresh ignores validators; cached bytes must still pass SHA-256.
func FetchRefreshContext(ctx context.Context, client *http.Client, rawURL, dir string, strictMD5, force bool) (*Source, error) {
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	script, sumURL := ScriptURLs(rawURL)
	configURL, err := ConfigURL(script)
	if err != nil {
		return nil, err
	}
	src := &Source{URL: rawURL, ScriptURL: script}
	scriptValidator, configValidator := &downloadValidator{}, &downloadValidator{}
	if !force {
		if cached, err := CachedSource(dir); err == nil && cached.ScriptURL == script {
			scriptValidator = &downloadValidator{Path: cached.Path, SHA256: cached.SHA256, ETag: cached.ScriptETag, Modified: cached.ScriptModified}
			configValidator = &downloadValidator{Path: cached.ConfigPath, SHA256: cached.ConfigSHA256, ETag: cached.ConfigETag, Modified: cached.ConfigModified}
		}
	}
	if b, err := getContext(ctx, client, sumURL, 1<<10); err == nil {
		src.Expected = strings.ToLower(md5re.FindString(string(b)))
	}
	versions := filepath.Join(dir, "source", "versions")
	if err := os.MkdirAll(versions, 0o700); err != nil {
		return nil, err
	}
	stage, err := os.MkdirTemp(versions, ".stage-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	tmpScript, size, md5sum, sha256sum, err := downloadTempContext(ctx, client, script, stage, 256<<20, true, scriptValidator)
	if err != nil {
		return nil, fmt.Errorf("下载 index.js 失败: %w", err)
	}
	if size < 20 {
		return nil, fmt.Errorf("index.js 内容异常（%d 字节）", size)
	}
	src.MD5, src.SHA256 = md5sum, sha256sum
	src.ScriptETag, src.ScriptModified = scriptValidator.ETag, scriptValidator.Modified
	if strictMD5 && src.MD5Mismatch() {
		return nil, fmt.Errorf("md5 校验失败（文件 %s ≠ 发布 %s）", src.MD5, src.Expected)
	}
	tmpConfig, cfgSize, _, cfgHash, cfgErr := downloadTempContext(ctx, client, configURL, stage, 64<<20, true, configValidator)
	if cfgErr != nil && !errors.Is(cfgErr, errNotFound) {
		return nil, fmt.Errorf("下载 index.config.js 失败，保留原版本: %w", cfgErr)
	}
	// A 404 explicitly means no configuration. An empty successful response is
	// invalid, not an instruction to silently delete executable configuration.
	if cfgErr == nil {
		if cfgSize == 0 {
			return nil, errors.New("index.config.js 为空，保留原版本")
		}
		src.ConfigSHA256 = cfgHash
		src.ConfigETag, src.ConfigModified = configValidator.ETag, configValidator.Modified
		if err := os.Rename(tmpConfig, filepath.Join(stage, "index.config.js")); err != nil {
			return nil, err
		}
	}
	if err := os.Rename(tmpScript, filepath.Join(stage, "index.js")); err != nil {
		return nil, err
	}
	src.BundleSHA256 = bundleDigest(src.SHA256, src.ConfigSHA256)
	version := filepath.Join(versions, src.BundleSHA256)
	if _, err := os.Stat(version); err == nil {
		// Content-addressed directories are never overwritten while a host may be
		// using them. Verify cached bytes before reusing the existing directory.
		src.Path = filepath.Join(version, "index.js")
		if src.ConfigSHA256 != "" {
			src.ConfigPath = filepath.Join(version, "index.config.js")
		}
		if err := src.Verify(); err != nil {
			// Repair into a different immutable directory. Never replace bytes that a
			// separately running reader could have already opened from the old one.
			version = filepath.Join(versions, src.BundleSHA256+"-"+strings.TrimPrefix(filepath.Base(stage), ".stage-"))
			if err := os.Rename(stage, version); err != nil {
				return nil, err
			}
			src.Path = filepath.Join(version, "index.js")
			if src.ConfigSHA256 != "" {
				src.ConfigPath = filepath.Join(version, "index.config.js")
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	} else {
		if err := os.Rename(stage, version); err != nil {
			return nil, err
		}
		src.Path = filepath.Join(version, "index.js")
		if src.ConfigSHA256 != "" {
			src.ConfigPath = filepath.Join(version, "index.config.js")
		}
	}
	src.VersionName = filepath.Base(version)
	return src, nil
}

func bundleDigest(script, config string) string {
	// The explicit absence marker distinguishes no config from any byte content.
	b := sha256.Sum256([]byte("jstv-bundle-v1\nindex.js:" + script + "\nindex.config.js:" + config + "\n"))
	return hex.EncodeToString(b[:])
}

func (s *Source) Verify() error {
	for _, artifact := range []struct{ path, sum string }{{s.Path, s.SHA256}, {s.ConfigPath, s.ConfigSHA256}} {
		if artifact.path == "" && artifact.sum == "" {
			continue
		}
		f, err := os.Open(artifact.path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		f.Close()
		if err != nil {
			return err
		}
		if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), artifact.sum) {
			return errors.New("插件缓存完整性校验失败，请重新下载")
		}
	}
	if s.BundleSHA256 == "" || s.BundleSHA256 != bundleDigest(s.SHA256, s.ConfigSHA256) {
		return errors.New("插件版本清单无效")
	}
	if s.ConfigPath == "" {
		if _, err := os.Stat(filepath.Join(filepath.Dir(s.Path), "index.config.js")); !errors.Is(err, os.ErrNotExist) {
			return errors.New("插件版本出现未记录的配置")
		}
	}
	return nil
}

// PublishSource atomically commits a complete version only after it starts.
func PublishSource(dir string, src *Source) error {
	if err := src.Verify(); err != nil {
		return err
	}
	return atomicfile.JSON(filepath.Join(dir, "source", "current.json"), src)
}

// CachedSource validates the complete on-disk version, including configuration.
func CachedSource(dir string) (*Source, error) {
	b, err := os.ReadFile(filepath.Join(dir, "source", "current.json"))
	if err != nil {
		return nil, err
	}
	var src Source
	if err := json.Unmarshal(b, &src); err != nil {
		return nil, err
	}
	if len(src.BundleSHA256) != 64 || strings.ContainsAny(src.BundleSHA256, `/\\.`) {
		return nil, errors.New("插件缓存版本无效")
	}
	name := src.VersionName
	if name == "" {
		name = src.BundleSHA256
	}
	if !strings.HasPrefix(name, src.BundleSHA256) || filepath.Base(name) != name || strings.ContainsAny(name, `/\\.`) {
		return nil, errors.New("插件缓存路径无效")
	}
	version := filepath.Join(dir, "source", "versions", name)
	src.Path = filepath.Join(version, "index.js")
	src.ConfigPath = ""
	if src.ConfigSHA256 != "" {
		src.ConfigPath = filepath.Join(version, "index.config.js")
	}
	return &src, src.Verify()
}

var (
	errNotFound = errors.New("optional file not found")
	errTooLarge = errors.New("download exceeds size limit")
)

func downloadTemp(c *http.Client, u, dir string, limit int64, digest bool) (string, int64, string, string, error) {
	return downloadTempContext(context.Background(), c, u, dir, limit, digest)
}

type downloadValidator struct{ Path, SHA256, ETag, Modified string }

func downloadTempContext(ctx context.Context, c *http.Client, u, dir string, limit int64, digest bool, validators ...*downloadValidator) (string, int64, string, string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return "", 0, "", "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 JSTVPlayer")
	var validator *downloadValidator
	if len(validators) > 0 {
		validator = validators[0]
	}
	if validator != nil && validator.Path != "" {
		if validator.ETag != "" {
			req.Header.Set("If-None-Match", validator.ETag)
		} else if validator.Modified != "" {
			req.Header.Set("If-Modified-Since", validator.Modified)
		}
	}
	resp, err := c.Do(req)
	if err != nil {
		return "", 0, "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", 0, "", "", errNotFound
	}
	var body io.Reader = resp.Body
	if resp.StatusCode == http.StatusNotModified {
		if validator == nil || validator.Path == "" || !digest {
			return "", 0, "", "", errors.New("服务器返回 304，但没有已验证的缓存")
		}
		cached, err := os.Open(validator.Path)
		if err != nil {
			return "", 0, "", "", err
		}
		defer cached.Close()
		body = cached
	} else if resp.StatusCode != http.StatusOK {
		return "", 0, "", "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, u)
	}
	if validator != nil {
		if etag := resp.Header.Get("ETag"); etag != "" || resp.StatusCode == 200 {
			validator.ETag = etag
		}
		if modified := resp.Header.Get("Last-Modified"); modified != "" || resp.StatusCode == 200 {
			validator.Modified = modified
		}
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
	n, err := io.Copy(dst, io.LimitReader(body, limit+1))
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
	mdsum, shsum := "", ""
	if digest {
		m, s := md.Sum(nil), sh.Sum(nil)
		mdsum, shsum = hex.EncodeToString(m), hex.EncodeToString(s)
	}
	if resp.StatusCode == http.StatusNotModified && validator.SHA256 != shsum {
		return "", 0, "", "", errors.New("条件刷新期间缓存发生变化，请强制刷新")
	}
	cleanup = false
	return name, n, mdsum, shsum, nil
}

func get(c *http.Client, u string, limit int64) ([]byte, error) {
	return getContext(context.Background(), c, u, limit)
}

func getContext(ctx context.Context, c *http.Client, u string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
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
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err == nil && int64(len(b)) > limit {
		return nil, errTooLarge
	}
	return b, err
}

package webserver

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
)

func (s *webServer) serveFrontend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method", http.StatusMethodNotAllowed)
		return
	}
	clean := path.Clean("/" + r.URL.Path)
	if clean == "/" || clean == "/index.html" {
		s.serveIndex(w, r)
		return
	}
	if strings.Contains(clean, "..") || strings.HasPrefix(path.Base(clean), ".") {
		http.NotFound(w, r)
		return
	}
	ext := strings.ToLower(path.Ext(clean))
	switch ext {
	case ".html", ".js", ".css", ".png", ".svg", ".ico", ".webp":
	default:
		http.NotFound(w, r)
		return
	}
	if strings.HasSuffix(clean, ".test.cjs") || strings.HasSuffix(clean, ".md") {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(clean, "/")
	b, err := fs.ReadFile(s.assets, path.Join("web", name))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType(ext))
	w.Header().Set("Cache-Control", "no-cache")
	if r.Method != http.MethodHead {
		_, _ = w.Write(b)
	}
}

func (s *webServer) serveIndex(w http.ResponseWriter, r *http.Request) {
	b, err := fs.ReadFile(s.assets, "web/index.html")
	if err != nil {
		http.Error(w, "界面缺失", http.StatusInternalServerError)
		return
	}
	b = injectWebScripts(b)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodHead {
		_, _ = w.Write(b)
	}
}

func injectWebScripts(html []byte) []byte {
	html = bytes.Replace(html,
		[]byte(`<script src="request-token.js"></script>`),
		[]byte("<script src=\"/bridge.js\"></script>\n<script src=\"request-token.js\"></script>"),
		1)
	html = bytes.Replace(html,
		[]byte(`<script src="app.js"></script>`),
		[]byte("<script src=\"app.js\"></script>\n<script src=\"/web-player.js\"></script>"),
		1)
	return html
}

func (s *webServer) serveAsset(name, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(s.assets, name)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Cache-Control", "no-cache")
		_, _ = w.Write(b)
	}
}

func contentType(ext string) string {
	switch ext {
	case ".html":
		return "text/html; charset=utf-8"
	case ".js":
		return "text/javascript; charset=utf-8"
	case ".css":
		return "text/css; charset=utf-8"
	default:
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
		return "application/octet-stream"
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, dest any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, limit))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

var loginPage = []byte(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>登录 · JSTV Player</title>
<link rel="stylesheet" href="/login.css">
</head>
<body>
<main class="login">
  <div class="mark" aria-hidden="true"></div>
  <h1>JSTV Player</h1>
  <p class="lead">网页模式需要登录后才能浏览和播放。</p>
  <form id="login-form">
    <label>用户名<input name="username" autocomplete="username" required maxlength="64"></label>
    <label>密码<input name="password" type="password" autocomplete="current-password" required maxlength="72"></label>
    <button type="submit">登录</button>
    <p id="login-error" class="error" role="alert"></p>
  </form>
</main>
<script src="/login.js"></script>
</body>
</html>
`)

package webserver

import (
	"net/http"
	"strings"
)

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; media-src 'self' blob:; connect-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; worker-src 'none'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'; object-src 'none'")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func (s *webServer) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.auth.sessionUser(r); !ok {
			if r.Method == http.MethodGet && wantsHTML(r) {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未登录"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func wantsHTML(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

func (s *webServer) serveLogin(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.auth.sessionUser(r); ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(loginPage)
}

func (s *webServer) handleLogin(w http.ResponseWriter, r *http.Request) {
	if !jsonMutation(r) {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "需要 JSON 请求"})
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, 8<<10, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求无效"})
		return
	}
	token, err := s.auth.login(clientIP(r), body.Username, body.Password)
	if err != nil {
		status := http.StatusUnauthorized
		if err.Error() == "尝试次数过多，请稍后再试" {
			status = http.StatusTooManyRequests
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	s.auth.setCookie(w, token)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *webServer) handleLogout(w http.ResponseWriter, r *http.Request) {
	if !jsonMutation(r) {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "需要 JSON 请求"})
		return
	}
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.auth.logout(c.Value)
	}
	s.auth.clearCookie(w)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func jsonMutation(r *http.Request) bool {
	if r.Header.Get("X-JSTV-Request") != "1" {
		return false
	}
	ct := strings.ToLower(r.Header.Get("Content-Type"))
	return strings.HasPrefix(ct, "application/json")
}

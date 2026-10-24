package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionCookie   = "jstv_session"
	sessionLife     = 12 * time.Hour
	sessionAbsolute = 7 * 24 * time.Hour
)

// bcryptCost is 12 in the server binary. Tests lower it; they are not part of the binary.
var bcryptCost = 12

var errBadLogin = errors.New("用户名或密码错误")

type authFile struct {
	Username string `json:"username"`
	Hash     string `json:"hash"`
}

type storedSession struct {
	ID      string    `json:"id"`
	User    string    `json:"user"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
}

type sessionFile struct {
	Sessions []storedSession `json:"sessions"`
}

type authStore struct {
	path         string
	sessionsPath string
	secure       bool

	mu       sync.Mutex
	file     authFile
	dummy    []byte
	sessions map[string]*storedSession
	fails    map[string][]time.Time
}

func openAuth(dir, user, password string, reset, secure bool) (*authStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	a := &authStore{
		path:         filepath.Join(dir, "auth.json"),
		sessionsPath: filepath.Join(dir, "sessions.json"),
		secure:       secure,
		sessions:     map[string]*storedSession{},
		fails:        map[string][]time.Time{},
	}
	dummy, err := bcrypt.GenerateFromPassword([]byte("jstv-web-dummy-password"), bcryptCost)
	if err != nil {
		return nil, err
	}
	a.dummy = dummy

	b, err := os.ReadFile(a.path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err == nil && !reset {
		if jsonErr := json.Unmarshal(b, &a.file); jsonErr != nil {
			return nil, errors.New("登录凭据文件无法读取")
		}
		if a.file.Username == "" || a.file.Hash == "" {
			return nil, errors.New("登录凭据文件无效")
		}
		a.loadSessions()
		return a, nil
	}
	user = strings.TrimSpace(user)
	if user == "" {
		user = "admin"
	}
	if password == "" {
		return nil, errors.New("网页模式需要环境变量 JSTV_WEB_PASSWORD（8–72 字节）。已有凭据时要更换密码，请同时设置 JSTV_WEB_RESET_PASSWORD=1")
	}
	if err := validateCredential(user, password); err != nil {
		return nil, err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcryptCost)
	if err != nil {
		return nil, err
	}
	a.file = authFile{Username: user, Hash: string(hash)}
	if err := writePrivateJSON(a.path, a.file); err != nil {
		return nil, err
	}
	if reset {
		a.sessions = map[string]*storedSession{}
		_ = writePrivateJSON(a.sessionsPath, sessionFile{})
	} else {
		a.loadSessions()
	}
	return a, nil
}

func validateCredential(user, password string) error {
	if user == "" || len(user) > 64 || strings.TrimSpace(user) != user || strings.ContainsAny(user, " \t\r\n:/\\") {
		return errors.New("用户名须为 1–64 位，且不能包含空格、斜杠或冒号")
	}
	if len(password) < 8 || len(password) > 72 {
		return errors.New("密码长度须为 8–72 字节")
	}
	if password == user {
		return errors.New("密码不能与用户名相同")
	}
	return nil
}

func (a *authStore) loadSessions() {
	b, err := os.ReadFile(a.sessionsPath)
	if err != nil {
		return
	}
	var file sessionFile
	if json.Unmarshal(b, &file) != nil {
		return
	}
	now := time.Now()
	for i := range file.Sessions {
		s := file.Sessions[i]
		if s.ID == "" || s.User == "" || now.After(s.Expires) || now.After(s.Created.Add(sessionAbsolute)) {
			continue
		}
		cp := s
		a.sessions[s.ID] = &cp
	}
}

func (a *authStore) saveSessionsLocked() {
	file := sessionFile{Sessions: make([]storedSession, 0, len(a.sessions))}
	now := time.Now()
	for id, s := range a.sessions {
		if s == nil || now.After(s.Expires) || now.After(s.Created.Add(sessionAbsolute)) {
			delete(a.sessions, id)
			continue
		}
		file.Sessions = append(file.Sessions, *s)
	}
	_ = writePrivateJSON(a.sessionsPath, file)
}

func writePrivateJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func (a *authStore) login(ip, user, password string) (string, error) {
	if a.limited(ip) {
		return "", errors.New("尝试次数过多，请稍后再试")
	}
	user = strings.TrimSpace(user)
	a.mu.Lock()
	hash := a.file.Hash
	name := a.file.Username
	dummy := a.dummy
	a.mu.Unlock()
	cmp := dummy
	if user == name && hash != "" {
		cmp = []byte(hash)
	}
	if bcrypt.CompareHashAndPassword(cmp, []byte(password)) != nil || user != name {
		a.noteFail(ip)
		return "", errBadLogin
	}
	a.clearFails(ip)
	raw, err := newSessionToken()
	if err != nil {
		return "", err
	}
	now := time.Now()
	rec := &storedSession{ID: hashToken(raw), User: name, Created: now, Expires: now.Add(sessionLife)}
	a.mu.Lock()
	a.sessions[rec.ID] = rec
	a.saveSessionsLocked()
	a.mu.Unlock()
	return raw, nil
}

func (a *authStore) logout(raw string) {
	if raw == "" {
		return
	}
	id := hashToken(raw)
	a.mu.Lock()
	delete(a.sessions, id)
	a.saveSessionsLocked()
	a.mu.Unlock()
}

func (a *authStore) sessionUser(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || len(c.Value) < 32 {
		return "", false
	}
	id := hashToken(c.Value)
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.sessions[id]
	if s == nil || now.After(s.Expires) || now.After(s.Created.Add(sessionAbsolute)) || s.User != a.file.Username {
		delete(a.sessions, id)
		return "", false
	}
	if s.Expires.Sub(now) < 2*time.Hour {
		ext := now.Add(sessionLife)
		capAt := s.Created.Add(sessionAbsolute)
		if ext.After(capAt) {
			ext = capAt
		}
		s.Expires = ext
		a.saveSessionsLocked()
	}
	return s.User, true
}

func (a *authStore) setCookie(w http.ResponseWriter, raw string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    raw,
		Path:     "/",
		MaxAge:   int(sessionLife.Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.secure,
	})
}

func (a *authStore) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   a.secure,
	})
}

func (a *authStore) limited(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.recentFailsLocked(ip)) >= 10
}

func (a *authStore) noteFail(ip string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.fails[ip] = append(a.recentFailsLocked(ip), time.Now())
}

func (a *authStore) clearFails(ip string) {
	a.mu.Lock()
	delete(a.fails, ip)
	a.mu.Unlock()
}

func (a *authStore) recentFailsLocked(ip string) []time.Time {
	cut := time.Now().Add(-10 * time.Minute)
	old := a.fails[ip]
	kept := make([]time.Time, 0, len(old))
	for _, t := range old {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(a.fails, ip)
		return nil
	}
	a.fails[ip] = kept
	return kept
}

func newSessionToken() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

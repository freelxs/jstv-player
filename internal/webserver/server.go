package webserver

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"time"

	"github.com/freelxs/jstv-player/internal/app"
)

func Enabled() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("JSTV_MODE")), "web") {
		return true
	}
	for _, arg := range os.Args[1:] {
		if arg == "--web" || arg == "-web" {
			return true
		}
	}
	return false
}

func Run(assets fs.FS) error {
	dir := os.Getenv("JSTV_DATA_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		dir = filepathJoin(home, ".local", "share", "jstv-player")
	}
	user := strings.TrimSpace(os.Getenv("JSTV_WEB_USER"))
	password := os.Getenv("JSTV_WEB_PASSWORD")
	reset := envOn("JSTV_WEB_RESET_PASSWORD")
	secure := envOn("JSTV_WEB_SECURE")
	auth, err := openAuth(dir, user, password, reset, secure)
	if err != nil {
		return err
	}
	if reset {
		log.Printf("web: replaced saved login for %q and revoked sessions", auth.file.Username)
	} else {
		log.Printf("web: login user %q", auth.file.Username)
	}
	runtime := app.New(dir, assets, true)
	p := runtime.Player
	addr := strings.TrimSpace(os.Getenv("JSTV_WEB_ADDR"))
	if addr == "" {
		addr = ":8080"
	}
	srv := &http.Server{
		Addr:              addr,
		Handler:           newWebHandler(runtime, auth, assets),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    1 << 20,
	}
	errCh := make(chan error, 1)
	go func() {
		log.Printf("JSTV Player web listening on %s", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()
	select {
	case err := <-errCh:
		p.StopSource()
		return err
	case <-shutdownNotify():
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		p.StopSource()
		return nil
	}
}

func envOn(name string) bool {
	v := strings.TrimSpace(os.Getenv(name))
	return v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "yes")
}

func filepathJoin(elem ...string) string {
	return strings.Join(elem, string(os.PathSeparator))
}

type webServer struct {
	player        *app.Player
	runtime       *app.Runtime
	assets        fs.FS
	auth          *authStore
	media         *http.Client
	posters       *http.Client
	methods       map[string]reflect.Method
	configuration configurationBridge
}

func newWebHandler(runtime *app.Runtime, auth *authStore, assets fs.FS) http.Handler {
	s := &webServer{
		player:  runtime.Player,
		runtime: runtime,
		assets:  assets,
		auth:    auth,
		media:   newMediaClient(),
		posters: &http.Client{Timeout: 20 * time.Second},
		methods: playerMethods(),
	}
	s.posters.Transport = s.posterTransport()
	s.posters.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("too many redirects")
		}
		if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
			return errors.New("bad scheme")
		}
		return nil
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/plugin-configuration/", s.servePluginConfiguration)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /login", s.serveLogin)
	mux.HandleFunc("GET /login.js", s.serveAsset("webassets/login.js", "text/javascript; charset=utf-8"))
	mux.HandleFunc("GET /login.css", s.serveAsset("webassets/login.css", "text/css; charset=utf-8"))
	mux.HandleFunc("POST /api/login", s.handleLogin)
	protected := http.NewServeMux()
	protected.HandleFunc("GET /configuration/", s.serveConfiguration)
	protected.HandleFunc("POST /api/logout", s.handleLogout)
	protected.HandleFunc("POST /api/call", s.handleCall)
	protected.HandleFunc("POST /api/stream", s.handleStream)
	protected.HandleFunc("GET /poster", s.servePoster)
	protected.Handle("GET /media/", http.HandlerFunc(s.serveMedia))
	protected.Handle("HEAD /media/", http.HandlerFunc(s.serveMedia))
	protected.HandleFunc("GET /bridge.js", s.serveAsset("webassets/bridge.js", "text/javascript; charset=utf-8"))
	protected.HandleFunc("GET /web-player.js", s.serveAsset("webassets/web-player.js", "text/javascript; charset=utf-8"))
	protected.HandleFunc("GET /vendor/hls.min.js", s.serveAsset("webassets/hls.min.js", "text/javascript; charset=utf-8"))
	protected.Handle("GET /", http.HandlerFunc(s.serveFrontend))
	mux.Handle("/", s.requireAuth(protected))
	return securityHeaders(mux)
}

func newMediaClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       90 * time.Second,
			MaxIdleConns:          32,
		},
		// The loopback proxy follows upstream redirects itself.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

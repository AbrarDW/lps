// Package api is the HTTP API and dashboard server of lpsd.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/abrardw/lps/internal/auth"
	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/store"
)

// Config tunes the server.
type Config struct {
	// Host port ranges handed out to VPS SSH and app HTTP ports.
	VPSPortMin, VPSPortMax int
	AppPortMin, AppPortMax int
	// BindIP is where container ports are published (shown in the UI).
	BindIP string
	// SecureCookies sets the Secure flag; enable when served over HTTPS.
	SecureCookies bool
	SessionTTL    time.Duration
	Version       string
}

// DefaultConfig returns sensible defaults.
func DefaultConfig() Config {
	return Config{
		VPSPortMin: 2201, VPSPortMax: 2299,
		AppPortMin: 8100, AppPortMax: 8199,
		BindIP:     "127.0.0.1",
		SessionTTL: 7 * 24 * time.Hour,
		Version:    "dev",
	}
}

// Server holds dependencies for the HTTP handlers.
type Server struct {
	cfg     Config
	store   *store.Store
	engine  docker.Engine
	limiter *auth.Limiter
	ui      fs.FS
	log     *slog.Logger

	ctx    context.Context // cancelled on shutdown; parent of background jobs
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu        sync.Mutex
	portsHeld map[int]bool        // ports reserved by in-flight creates
	vpsTasks  map[int64]*taskInfo // provisioning status per VPS
	deploying map[int64]bool      // apps with a deploy in progress
	liveLogs  map[int64]*syncBuffer
}

type taskInfo struct {
	running bool
	err     string
	log     *syncBuffer // provisioning output (image build etc.)
}

// tail returns the last n bytes of the buffer.
func (b *syncBuffer) tail(n int) string {
	s := b.String()
	if len(s) > n {
		s = s[len(s)-n:]
	}
	return s
}

// New creates a Server. ui may be nil when the frontend has not been built.
func New(cfg Config, st *store.Store, eng docker.Engine, ui fs.FS, logger *slog.Logger) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	if logger == nil {
		logger = slog.Default()
	}
	return &Server{
		cfg: cfg, store: st, engine: eng, ui: ui, log: logger,
		limiter:   auth.NewLimiter(5, 15*time.Minute),
		ctx:       ctx,
		cancel:    cancel,
		portsHeld: map[int]bool{},
		vpsTasks:  map[int64]*taskInfo{},
		deploying: map[int64]bool{},
		liveLogs:  map[int64]*syncBuffer{},
	}
}

// Shutdown cancels background jobs and waits for them to finish.
func (s *Server) Shutdown() {
	s.cancel()
	s.wg.Wait()
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	// No RealIP middleware: trusting X-Forwarded-For would let clients dodge the login limiter.
	r.Use(middleware.Recoverer, securityHeaders)

	r.Route("/api", func(r chi.Router) {
		r.Use(csrfGuard, middleware.NoCache)
		r.Get("/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
		r.Get("/setup", s.handleSetupStatus)
		r.Post("/setup", s.handleSetup)
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/logout", s.handleLogout)

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/auth/me", s.handleMe)
			r.Get("/system", s.handleSystem)

			r.Get("/vps", s.handleListVPS)
			r.Post("/vps", s.handleCreateVPS)
			r.Get("/vps/{id}", s.handleGetVPS)
			r.Delete("/vps/{id}", s.handleDeleteVPS)
			r.Post("/vps/{id}/start", s.handleVPSAction("start"))
			r.Post("/vps/{id}/stop", s.handleVPSAction("stop"))
			r.Get("/vps/{id}/logs", s.handleVPSLogs)

			r.Get("/apps", s.handleListApps)
			r.Post("/apps", s.handleCreateApp)
			r.Get("/apps/{id}", s.handleGetApp)
			r.Delete("/apps/{id}", s.handleDeleteApp)
			r.Post("/apps/{id}/deploy", s.handleDeployApp)
			r.Post("/apps/{id}/start", s.handleAppAction("start"))
			r.Post("/apps/{id}/stop", s.handleAppAction("stop"))
			r.Get("/apps/{id}/logs", s.handleAppLogs)
			r.Get("/apps/{id}/deployments", s.handleListDeployments)
		})
		r.NotFound(func(w http.ResponseWriter, r *http.Request) { writeError(w, 404, "not found") })
	})

	r.Handle("/*", s.uiHandler())
	return r
}

// ---- middleware ----

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; frame-ancestors 'none'")
		next.ServeHTTP(w, r)
	})
}

// csrfGuard requires a custom header on state-changing requests. Browsers
// cannot add it cross-origin without a CORS preflight, which we never allow.
func csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if r.Header.Get("X-LPS-Request") != "1" {
				writeError(w, http.StatusForbidden, "missing X-LPS-Request header")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type ctxKey int

const userKey ctxKey = 1

const sessionCookie = "lps_session"

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "not logged in")
			return
		}
		u, err := s.store.UserBySession(r.Context(), auth.HashToken(c.Value))
		if err != nil {
			writeError(w, http.StatusUnauthorized, "session expired")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), userKey, u)))
	})
}

// ---- UI ----

func (s *Server) uiHandler() http.Handler {
	if s.ui == nil {
		return http.HandlerFunc(uiMissing)
	}
	if _, err := fs.Stat(s.ui, "index.html"); err != nil {
		return http.HandlerFunc(uiMissing)
	}
	files := http.FileServer(http.FS(s.ui))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if st, err := fs.Stat(s.ui, p); err == nil && !st.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		// SPA fallback: client-side routes get index.html.
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, s.ui, "index.html")
	})
}

func uiMissing(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	w.Write([]byte("LPS dashboard is not built. Run `make web` (or `cd web && npm ci && npm run build`) and rebuild lpsd.\nThe API is available under /api.\n"))
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.log.Error("internal error", "err", err)
	writeError(w, http.StatusInternalServerError, "internal error: "+err.Error())
}

func storeError(s *Server, w http.ResponseWriter, err error, what string) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, what+" not found")
	case errors.Is(err, store.ErrConflict):
		writeError(w, http.StatusConflict, what+" already exists")
	default:
		s.internalError(w, err)
	}
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// syncBuffer is a goroutine-safe log buffer for live build output.
type syncBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	const max = 1 << 20 // keep the last 1 MiB
	b.buf = append(b.buf, p...)
	if len(b.buf) > max {
		b.buf = b.buf[len(b.buf)-max:]
	}
	return len(p), nil
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

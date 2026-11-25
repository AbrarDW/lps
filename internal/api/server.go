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
	"github.com/abrardw/lps/internal/tunnel"
	"github.com/abrardw/lps/internal/update"
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
	// DashboardPort is the local port lpsd listens on; remote access
	// publishes it on the tailnet. 0 disables publishing the dashboard.
	DashboardPort int
	// DataDir is where lpsd keeps its database (used for the disk-space check).
	DataDir string
	// GatewayPort is the local port of the path-based apps gateway
	// (GatewayHandler); remote access publishes it on appsRemotePort.
	// 0 = no gateway.
	GatewayPort int
	// UpdateRepo is the GitHub repository whose releases LPS checks for
	// updates ("" = never check, as in tests).
	UpdateRepo string
	// Quit stops the running LPS (the Windows desktop app sets it); needed to
	// install updates.
	Quit func()
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
	tunnel  tunnel.Provider // nil = remote access unavailable
	hooks   systemHooks

	remote     remoteState
	remoteKick chan struct{}
	// vpsAppsKick asks vpsAppsLoop to restart stopped VPS apps now.
	vpsAppsKick chan struct{}
	stats       *statsState

	ctx    context.Context // cancelled on shutdown; parent of background jobs
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu        sync.Mutex
	portsHeld map[int]bool        // ports reserved by in-flight creates
	vpsTasks  map[int64]*taskInfo // provisioning status per VPS
	deploying map[int64]bool      // apps with a deploy in progress
	liveLogs  map[int64]*syncBuffer
	tickets   map[string]*loginTicket // sign-ins waiting for a 2FA code
	gitTried  map[int64]string        // last commit auto-deploy tried per app
	gitKick   chan struct{}
	snapping  map[int64]bool // VPSes a snapshot is being taken of
	updater   *update.Checker
	updates   updateState
	quick     quickState
}

type taskInfo struct {
	running bool
	kind    string // "" = provisioning, stateRestoring = restoring a snapshot
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

// New creates a Server. ui may be nil when the frontend has not been built;
// tp may be nil to disable remote access.
func New(cfg Config, st *store.Store, eng docker.Engine, tp tunnel.Provider, ui fs.FS, logger *slog.Logger) *Server {
	ctx, cancel := context.WithCancel(context.Background())
	if logger == nil {
		logger = slog.Default()
	}
	s := &Server{
		cfg: cfg, store: st, engine: eng, ui: ui, log: logger, tunnel: tp, hooks: defaultHooks(cfg.DataDir),
		remoteKick:  make(chan struct{}, 1),
		vpsAppsKick: make(chan struct{}, 1),
		stats:       newStatsState(),
		limiter:     auth.NewLimiter(5, 15*time.Minute),
		ctx:         ctx,
		cancel:      cancel,
		portsHeld:   map[int]bool{},
		vpsTasks:    map[int64]*taskInfo{},
		deploying:   map[int64]bool{},
		liveLogs:    map[int64]*syncBuffer{},
		tickets:     map[string]*loginTicket{},
	}
	if tp != nil {
		s.wg.Add(1)
		go s.remoteLoop()
	}
	s.gitTried = map[int64]string{}
	s.snapping = map[int64]bool{}
	s.updates.kick = make(chan struct{}, 1)
	if cfg.UpdateRepo != "" {
		s.updater = update.NewChecker(cfg.UpdateRepo)
		s.wg.Add(1)
		go s.updateLoop()
	}
	s.gitKick = make(chan struct{}, 1)
	s.quick.kick = make(chan struct{}, 1)
	s.wg.Add(4)
	go s.vpsAppsLoop()
	go s.statsLoop()
	go s.gitPollLoop()
	go s.quickLoop()
	return s
}

// Shutdown cancels background jobs and waits for them to finish.
func (s *Server) Shutdown() {
	s.cancel()
	s.wg.Wait()
}

// Handler returns the root HTTP handler.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	// No RealIP middleware: clientIP trusts X-Forwarded-For only from loopback.
	r.Use(middleware.Recoverer, securityHeaders)

	r.Route("/api", func(r chi.Router) {
		r.Use(csrfGuard, middleware.NoCache)
		r.Get("/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
		r.Get("/setup", s.handleSetupStatus)
		r.Post("/setup", s.handleSetup)
		r.Post("/auth/login", s.handleLogin)
		r.Post("/auth/login/2fa", s.handleLogin2FA)
		r.Post("/auth/logout", s.handleLogout)

		r.Group(func(r chi.Router) {
			r.Use(s.setupOrAuth)
			r.Get("/setup/checks", s.handleSetupChecks)
			r.Post("/setup/actions/{action}", s.handleSetupAction)
		})

		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)
			r.Get("/auth/me", s.handleMe)
			r.Get("/auth/2fa", s.handleGet2FA)
			r.Post("/auth/2fa/setup", s.handleSetup2FA)
			r.Post("/auth/2fa/enable", s.handleEnable2FA)
			r.Post("/auth/2fa/disable", s.handleDisable2FA)
			r.Post("/auth/2fa/recovery-codes", s.handleNewRecoveryCodes)
			r.Get("/system", s.handleSystem)
			r.Get("/system/autostart", s.handleGetAutostart)
			r.Put("/system/autostart", s.handleSetAutostart)
			r.Get("/system/update", s.handleGetUpdate)
			r.Put("/system/update", s.handleSetUpdate)
			r.Post("/system/update/check", s.handleCheckUpdate)
			r.Post("/system/update/install", s.handleInstallUpdate)

			r.Get("/vps", s.handleListVPS)
			r.Post("/vps", s.handleCreateVPS)
			r.Get("/vps/{id}", s.handleGetVPS)
			r.Delete("/vps/{id}", s.handleDeleteVPS)
			r.Post("/vps/{id}/start", s.handleVPSAction("start"))
			r.Post("/vps/{id}/stop", s.handleVPSAction("stop"))
			r.Get("/vps/{id}/logs", s.handleVPSLogs)
			r.Get("/vps/{id}/terminal", s.handleVPSTerminal)
			r.Get("/vps/{id}/snapshots", s.handleListSnapshots)
			r.Post("/vps/{id}/snapshots", s.handleCreateSnapshot)
			r.Post("/vps/{id}/snapshots/{sid}/restore", s.handleRestoreSnapshot)
			r.Post("/vps/{id}/snapshots/{sid}/clone", s.handleCloneSnapshot)
			r.Delete("/vps/{id}/snapshots/{sid}", s.handleDeleteSnapshot)

			r.Get("/apps", s.handleListApps)
			r.Post("/apps", s.handleCreateApp)
			r.Post("/apps/detect", s.handleDetectApp)
			r.Post("/apps/detect-git", s.handleDetectGit)
			r.Get("/apps/templates", s.handleAppTemplates)
			r.Get("/apps/{id}", s.handleGetApp)
			r.Delete("/apps/{id}", s.handleDeleteApp)
			r.Post("/apps/{id}/deploy", s.handleDeployApp)
			r.Post("/apps/{id}/start", s.handleAppAction("start"))
			r.Post("/apps/{id}/stop", s.handleAppAction("stop"))
			r.Get("/apps/{id}/logs", s.handleAppLogs)
			r.Get("/apps/{id}/deployments", s.handleListDeployments)
			r.Put("/apps/{id}/public", s.handleSetAppPublic)
			r.Put("/apps/{id}/quick-link", s.handleSetQuickLink)
			r.Get("/apps/{id}/dockerfile", s.handleGetAppDockerfile)
			r.Put("/apps/{id}/dockerfile", s.handleSetAppDockerfile)
			r.Put("/apps/{id}/run-config", s.handleSetAppRunConfig)
			r.Get("/apps/{id}/env", s.handleGetAppEnv)
			r.Put("/apps/{id}/env", s.handleSetAppEnv)
			r.Put("/apps/{id}/git", s.handleSetAppGit)

			r.Get("/events", s.handleListEvents)
			r.Get("/stats", s.handleStats)

			r.Get("/remote", s.handleGetRemote)
			r.Put("/remote", s.handleSetRemote)
			r.Post("/remote/sync", s.handleSyncRemote)
		})
		r.NotFound(func(w http.ResponseWriter, r *http.Request) { writeError(w, 404, "not found") })
	})

	r.Handle("/*", s.uiHandler())
	// <app>.localhost hosts reach the app, everything else the dashboard.
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if s.serveAppByHost(w, req) {
			return
		}
		r.ServeHTTP(w, req)
	})
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

// viaLocalProxy reports whether the request came from a reverse proxy on this
// machine (e.g. `tailscale serve`), the only case where forwarding headers are
// trusted. lpsd listens on localhost, so remote clients cannot forge this.
func viaLocalProxy(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback() && r.Header.Get("X-Forwarded-For") != ""
}

// clientIP identifies the client for login rate limiting. Behind a local proxy
// every request comes from 127.0.0.1, so use the forwarded address instead;
// otherwise one device's failed logins would lock out all the others.
func clientIP(r *http.Request) string {
	if viaLocalProxy(r) {
		fwd := r.Header.Get("X-Forwarded-For")
		if i := strings.LastIndex(fwd, ","); i >= 0 {
			fwd = fwd[i+1:] // the hop added by the local proxy
		}
		if ip := net.ParseIP(strings.TrimSpace(fwd)); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// isHTTPS reports whether the browser is talking HTTPS, directly or through a local proxy.
func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || (viaLocalProxy(r) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https"))
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

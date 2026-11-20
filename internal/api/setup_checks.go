package api

import (
	"context"
	"net/http"
	"runtime"

	"github.com/go-chi/chi/v5"

	"github.com/abrardw/lps/internal/platform"
	"github.com/abrardw/lps/internal/tunnel"
)

// systemHooks are the OS integrations used by the setup wizard; tests replace them.
type systemHooks struct {
	checks      func(ctx context.Context) []platform.Check
	startDocker func() error
	canStart    func() bool // whether StartDocker can work on this machine
	autostart   platform.Autostart
	// startInstaller runs a downloaded LPS installer (Windows).
	startInstaller func(path string) error
}

func defaultHooks(dataDir string) systemHooks {
	if dataDir == "" {
		dataDir = "."
	}
	return systemHooks{
		checks:         func(ctx context.Context) []platform.Check { return platform.SystemChecks(ctx, dataDir) },
		startDocker:    platform.StartDocker,
		canStart:       func() bool { return platform.DockerDesktopPath() != "" },
		autostart:      platform.NewAutostart(),
		startInstaller: platform.StartInstaller,
	}
}

// setupOrAuth allows a request while first-run setup is pending (no admin
// yet); afterwards it requires a logged-in session.
func (s *Server) setupOrAuth(next http.Handler) http.Handler {
	authed := s.requireAuth(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n, err := s.store.CountUsers(r.Context())
		if err != nil {
			s.internalError(w, err)
			return
		}
		if n == 0 {
			next.ServeHTTP(w, r)
			return
		}
		authed.ServeHTTP(w, r)
	})
}

type checksResp struct {
	OS     string           `json:"os"`
	Ready  bool             `json:"ready"` // every required check passed
	Checks []platform.Check `json:"checks"`
}

func (s *Server) handleSetupChecks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	checks := s.hooks.checks(ctx)

	dk := platform.Check{ID: "docker", Label: "Docker is running", Required: true}
	if v, err := s.engine.Ping(ctx); err == nil {
		dk.Status, dk.Detail = platform.StatusOK, "Docker "+v
	} else {
		dk.Status = platform.StatusFail
		dk.Detail = "LPS cannot reach Docker. " + platform.DockerHint()
		if s.hooks.canStart() {
			dk.Detail = "Docker Desktop is installed but not running. Start it and wait until it says \"Engine running\"."
			dk.Fixes = []platform.Fix{{Kind: platform.FixAction, Label: "Start Docker Desktop", Value: "start-docker"}}
		}
	}
	checks = append(checks, dk)

	if s.tunnel != nil {
		ts := platform.Check{ID: "tailscale", Label: "Tailscale (optional, for remote access)"}
		st, _ := s.tunnel.Status(ctx)
		switch {
		case st.Installed && st.Running:
			ts.Status, ts.Detail = platform.StatusOK, "Signed in as "+st.Tailnet
		case st.Installed:
			ts.Status, ts.Detail = platform.StatusWarn, "Installed but not signed in. You can set this up later."
		default:
			ts.Status, ts.Detail = platform.StatusWarn, "Not installed. Only needed to reach LPS when you are away from home."
			ts.Fixes = []platform.Fix{{Kind: platform.FixLink, Label: "Get Tailscale", Value: tunnel.TailscaleDownloadURL}}
		}
		checks = append(checks, ts)
	}

	ready := true
	for _, c := range checks {
		if c.Required && c.Status != platform.StatusOK {
			ready = false
		}
	}
	writeJSON(w, 200, checksResp{OS: runtime.GOOS, Ready: ready, Checks: checks})
}

func (s *Server) handleSetupAction(w http.ResponseWriter, r *http.Request) {
	switch chi.URLParam(r, "action") {
	case "start-docker":
		if err := s.hooks.startDocker(); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "starting"})
	default:
		writeError(w, http.StatusNotFound, "unknown action")
	}
}

func (s *Server) handleGetAutostart(w http.ResponseWriter, r *http.Request) {
	a := s.hooks.autostart
	on, err := a.Enabled()
	resp := map[string]any{"supported": a.Supported(), "enabled": on && a.Supported()}
	if err != nil && a.Supported() {
		resp["error"] = err.Error()
	}
	writeJSON(w, 200, resp)
}

func (s *Server) handleSetAutostart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !s.hooks.autostart.Supported() {
		writeError(w, http.StatusNotImplemented, "start at sign-in is only available in the installed Windows app")
		return
	}
	if err := s.hooks.autostart.Set(req.Enabled); err != nil {
		s.internalError(w, err)
		return
	}
	s.handleGetAutostart(w, r)
}

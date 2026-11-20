package api

// Update checks and one-click install. LPS asks GitHub for the latest release
// every few hours (unless turned off); the Windows desktop app can download
// the installer, verify its checksum, run it silently and quit, after which
// the installer starts the new version.

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/abrardw/lps/internal/platform"
	"github.com/abrardw/lps/internal/update"
)

const settingUpdateCheck = "updates.check"

var (
	updateInterval = 12 * time.Hour
	// canSelfUpdate is platform.CanSelfUpdate; tests override it.
	canSelfUpdate = platform.CanSelfUpdate
)

// updateState is the result of the last check and any install in progress.
type updateState struct {
	mu         sync.Mutex
	latest     *update.Release
	checkedAt  time.Time
	err        string
	installing string // "", "downloading", "starting"
	installErr string
	kick       chan struct{}
}

type updateView struct {
	Current    string          `json:"current"`
	Comparable bool            `json:"comparable"` // false for development builds
	Enabled    bool            `json:"enabled"`    // automatic checks
	Latest     *update.Release `json:"latest,omitempty"`
	Available  bool            `json:"available"`
	CheckedAt  *time.Time      `json:"checkedAt,omitempty"`
	Error      string          `json:"error,omitempty"`
	CanInstall bool            `json:"canInstall"` // one-click install works here
	Installing string          `json:"installing,omitempty"`
	InstallErr string          `json:"installError,omitempty"`
}

func (s *Server) updatesEnabled(ctx context.Context) bool {
	v, err := s.store.GetSetting(ctx, settingUpdateCheck, "true")
	return err == nil && v == "true"
}

// canInstallUpdate: the Windows desktop app, which can quit and be restarted
// by the installer.
func (s *Server) canInstallUpdate() bool {
	return canSelfUpdate && s.cfg.Quit != nil && update.Comparable(s.cfg.Version)
}

func (s *Server) checkUpdate(ctx context.Context) {
	if s.updater == nil || !update.Comparable(s.cfg.Version) {
		return
	}
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	r, err := s.updater.Latest(cctx)
	s.updates.mu.Lock()
	defer s.updates.mu.Unlock()
	s.updates.checkedAt = time.Now()
	if err != nil {
		s.updates.err = err.Error()
		return
	}
	s.updates.err = ""
	s.updates.latest = r
}

func (s *Server) updateLoop() {
	defer s.wg.Done()
	// Give a fresh start a moment, then check now and every updateInterval.
	select {
	case <-s.ctx.Done():
		return
	case <-time.After(10 * time.Second):
	case <-s.updates.kick:
	}
	for {
		if s.updatesEnabled(s.ctx) {
			s.checkUpdate(s.ctx)
		}
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(updateInterval):
		case <-s.updates.kick:
		}
	}
}

func (s *Server) updateView(ctx context.Context) updateView {
	v := updateView{
		Current:    s.cfg.Version,
		Comparable: update.Comparable(s.cfg.Version),
		Enabled:    s.updatesEnabled(ctx),
		CanInstall: s.canInstallUpdate(),
	}
	s.updates.mu.Lock()
	defer s.updates.mu.Unlock()
	if !s.updates.checkedAt.IsZero() {
		t := s.updates.checkedAt
		v.CheckedAt = &t
	}
	v.Error, v.Installing, v.InstallErr = s.updates.err, s.updates.installing, s.updates.installErr
	if r := s.updates.latest; r != nil {
		v.Latest = r
		v.Available = update.Newer(s.cfg.Version, r.Version)
		if v.CanInstall && (r.Installer == nil || r.Checksum == nil) {
			v.CanInstall = false
		}
	}
	return v
}

func (s *Server) handleGetUpdate(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.updateView(r.Context()))
}

// handleCheckUpdate checks now and waits for the answer.
func (s *Server) handleCheckUpdate(w http.ResponseWriter, r *http.Request) {
	if s.updater == nil {
		writeError(w, http.StatusNotImplemented, "update checks are not available in this build")
		return
	}
	s.checkUpdate(r.Context())
	writeJSON(w, 200, s.updateView(r.Context()))
}

// handleSetUpdate turns automatic checks on or off.
func (s *Server) handleSetUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &req) {
		return
	}
	val := map[bool]string{true: "true", false: "false"}[req.Enabled]
	if err := s.store.SetSetting(r.Context(), settingUpdateCheck, val); err != nil {
		s.internalError(w, err)
		return
	}
	if req.Enabled {
		select {
		case s.updates.kick <- struct{}{}:
		default:
		}
	}
	writeJSON(w, 200, s.updateView(r.Context()))
}

// handleInstallUpdate downloads the new installer, verifies it, starts it
// and quits LPS; the installer restarts LPS when it is done.
func (s *Server) handleInstallUpdate(w http.ResponseWriter, r *http.Request) {
	v := s.updateView(r.Context())
	switch {
	case !v.CanInstall:
		writeError(w, http.StatusConflict, "this copy of LPS can't install updates itself; download the new version from the release page")
		return
	case !v.Available:
		writeError(w, http.StatusConflict, "LPS is up to date")
		return
	case v.Installing != "":
		writeError(w, http.StatusConflict, "an update is already being installed")
		return
	}
	rel := v.Latest
	s.updates.mu.Lock()
	s.updates.installing, s.updates.installErr = "downloading", ""
	s.updates.mu.Unlock()
	s.info(r, "system.update", "system", 0, "", "Installing LPS %s (was %s)", rel.Version, s.cfg.Version)

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		err := func() error {
			ctx, cancel := context.WithTimeout(s.ctx, 15*time.Minute)
			defer cancel()
			path, err := s.updater.Download(ctx, rel, filepath.Join(s.dataDir(), "updates"))
			if err != nil {
				return err
			}
			s.updates.mu.Lock()
			s.updates.installing = "starting"
			s.updates.mu.Unlock()
			if err := s.hooks.startInstaller(path); err != nil {
				return errors.New("could not start the installer: " + err.Error())
			}
			return nil
		}()
		if err != nil {
			s.updates.mu.Lock()
			s.updates.installing, s.updates.installErr = "", err.Error()
			s.updates.mu.Unlock()
			s.fail(nil, "system.update_failed", "system", 0, "", "Update to %s failed: %s", rel.Version, firstLine(err.Error()))
			return
		}
		// Let the dashboard see "starting", then quit so the installer can
		// replace the files. It starts the new version when it is done.
		time.Sleep(time.Second)
		s.cfg.Quit()
	}()
	writeJSON(w, http.StatusAccepted, s.updateView(r.Context()))
}

func (s *Server) dataDir() string {
	if s.cfg.DataDir != "" {
		return s.cfg.DataDir
	}
	return "."
}

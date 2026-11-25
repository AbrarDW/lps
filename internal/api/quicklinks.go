package api

// Cloudflare quick links: a public https://<random>.trycloudflare.com address
// for an app, without a Cloudflare account or Tailscale. LPS runs
// cloudflared in a container next to the app (see docker/quicklink.go) and
// keeps it running; the address stays the same until the link is turned off
// or cloudflared restarts.

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/store"
)

// quickState holds the last error setting up each app's quick link.
type quickState struct {
	sync sync.Mutex // serialises reconciles
	mu   sync.Mutex
	errs map[int64]string
	kick chan struct{}
}

var quickInterval = time.Minute

func (s *Server) kickQuick() {
	select {
	case s.quick.kick <- struct{}{}:
	default:
	}
}

var errQuickNotDeployed = errors.New("the link starts after the app's first successful deploy")

// quickSpec is where an app's quick link sends visitors: the app's own
// container, or the VPS it runs in.
func (s *Server) quickSpec(ctx context.Context, a *store.App) (docker.QuickLinkSpec, error) {
	if a.ContainerID == "" {
		return docker.QuickLinkSpec{}, errQuickNotDeployed
	}
	if a.InVPS() {
		v, err := s.store.GetVPS(ctx, a.VPSID)
		if err != nil {
			return docker.QuickLinkSpec{}, err
		}
		return docker.QuickLinkSpec{App: a.Name, Target: docker.VPSContainerName(v.Name), Port: a.ContainerPort}, nil
	}
	return docker.QuickLinkSpec{App: a.Name, Target: docker.AppContainerName(a.Name), Port: a.ContainerPort}, nil
}

// reconcileQuickLinks starts the links that should exist and removes the rest.
func (s *Server) reconcileQuickLinks(ctx context.Context) {
	s.quick.sync.Lock()
	defer s.quick.sync.Unlock()
	apps, err := s.store.ListApps(ctx)
	if err != nil {
		s.log.Warn("quick links", "err", err)
		return
	}
	errs := map[int64]string{}
	want := map[string]bool{}
	for _, a := range apps {
		if !a.QuickLink {
			continue
		}
		want[a.Name] = true
		spec, err := s.quickSpec(ctx, a)
		if err == nil {
			err = s.engine.EnsureQuickLink(ctx, spec)
		}
		if err != nil {
			errs[a.ID] = err.Error()
		}
	}
	if have, err := s.engine.QuickLinkApps(ctx); err == nil {
		for _, name := range have {
			if !want[name] {
				if err := s.engine.RemoveQuickLink(ctx, name); err != nil {
					s.log.Warn("remove quick link", "app", name, "err", err)
				}
			}
		}
	}
	s.quick.mu.Lock()
	s.quick.errs = errs
	s.quick.mu.Unlock()
}

func (s *Server) quickLoop() {
	defer s.wg.Done()
	t := time.NewTicker(quickInterval)
	defer t.Stop()
	s.reconcileQuickLinks(s.ctx)
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.quick.kick:
		case <-t.C:
		}
		s.reconcileQuickLinks(s.ctx)
	}
}

// quickView fills an app view's quick link fields.
func (s *Server) quickView(ctx context.Context, a *store.App, v *appView) {
	if !a.QuickLink {
		return
	}
	s.quick.mu.Lock()
	setupErr := s.quick.errs[a.ID]
	s.quick.mu.Unlock()
	if a.ContainerID == "" {
		v.QuickState, v.QuickError = docker.QuickStarting, errQuickNotDeployed.Error()
		return
	}
	ql, err := s.engine.QuickLinkStatus(ctx, a.Name)
	switch {
	case err != nil:
		v.QuickState, v.QuickError = docker.QuickError, err.Error()
	case ql.State == docker.QuickMissing && setupErr != "":
		v.QuickState, v.QuickError = docker.QuickError, setupErr
	case ql.State == docker.QuickMissing:
		v.QuickState = docker.QuickStarting // the loop is creating it
	default:
		v.QuickState, v.QuickURL, v.QuickError = ql.State, ql.URL, ql.Error
	}
}

// handleSetQuickLink turns an app's Cloudflare quick link on or off.
func (s *Server) handleSetQuickLink(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Enabled == a.QuickLink {
		writeJSON(w, 200, s.appView(r, a))
		return
	}
	if err := s.store.SetAppQuickLink(r.Context(), a.ID, req.Enabled); err != nil {
		s.internalError(w, err)
		return
	}
	a.QuickLink = req.Enabled
	if req.Enabled {
		s.warn(r, "app.quick_link_on", subjApp, a.ID, a.Name, "Cloudflare quick link turned on for %s (public)", a.Name)
	} else {
		// Remove it now so the address stops working right away.
		if err := s.engine.RemoveQuickLink(r.Context(), a.Name); err != nil {
			writeError(w, http.StatusBadGateway, "remove quick link: "+err.Error())
			return
		}
		s.info(r, "app.quick_link_off", subjApp, a.ID, a.Name, "Cloudflare quick link turned off for %s", a.Name)
	}
	s.kickQuick()
	writeJSON(w, 200, s.appView(r, a))
}

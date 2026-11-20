package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/abrardw/lps/internal/store"
	"github.com/abrardw/lps/internal/tunnel"
)

// dashboardRemotePort is where the dashboard is published on the tailnet.
// It is never made public, so Funnel cannot use it for apps.
const dashboardRemotePort = 443

// remoteState is the outcome of the last reconcile, shown in the UI.
type remoteState struct {
	sync     sync.Mutex // serialises reconciles
	mu       sync.Mutex // guards the fields below
	status   tunnel.Status
	needs    *tunnel.NeedsActionError
	lastErr  string
	portErrs map[int]string // per remote port
	lastSync time.Time
}

// exposureView is one row of the remote-access page.
type exposureView struct {
	tunnel.Exposure
	Label string `json:"label"`
	URL   string `json:"url"`
	State string `json:"state"` // active | pending | error
	Error string `json:"error,omitempty"`
}

type remoteView struct {
	Provider     string                   `json:"provider"`
	Available    bool                     `json:"available"` // a provider is configured in lpsd
	Enabled      bool                     `json:"enabled"`
	Status       tunnel.Status            `json:"status"`
	DashboardURL string                   `json:"dashboardUrl,omitempty"`
	NeedsAction  *tunnel.NeedsActionError `json:"needsAction,omitempty"`
	Error        string                   `json:"error,omitempty"`
	LastSync     *time.Time               `json:"lastSync,omitempty"`
	Exposures    []exposureView           `json:"exposures"`
}

type desiredExposure struct {
	tunnel.Exposure
	label string
}

func (s *Server) remoteEnabled(ctx context.Context) bool {
	if s.tunnel == nil {
		return false
	}
	v, err := s.store.GetSetting(ctx, store.SettingRemoteEnabled, "false")
	return err == nil && v == "true"
}

// desiredExposures derives what should be published from the database.
func (s *Server) desiredExposures(ctx context.Context) (map[int]desiredExposure, error) {
	out := map[int]desiredExposure{}
	if !s.remoteEnabled(ctx) {
		return out, nil
	}
	add := func(e tunnel.Exposure, label string) { out[e.Port] = desiredExposure{e, label} }
	if s.cfg.DashboardPort > 0 {
		add(tunnel.Exposure{Proto: tunnel.ProtoHTTPS, Port: dashboardRemotePort, LocalPort: s.cfg.DashboardPort}, "Dashboard")
	}
	vpses, err := s.store.ListVPS(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range vpses {
		add(tunnel.Exposure{Proto: tunnel.ProtoTCP, Port: v.SSHPort, LocalPort: v.SSHPort}, "VPS "+v.Name+" (SSH)")
	}
	apps, err := s.store.ListApps(ctx)
	if err != nil {
		return nil, err
	}
	if s.cfg.GatewayPort > 0 && len(apps) > 0 {
		add(tunnel.Exposure{Proto: tunnel.ProtoHTTPS, Port: appsRemotePort, LocalPort: s.cfg.GatewayPort}, "Apps by name (/<app>/)")
	}
	for _, a := range apps {
		add(tunnel.Exposure{Proto: tunnel.ProtoHTTPS, Port: a.HostPort, LocalPort: a.HostPort}, "App "+a.Name)
		// Funnel only ever applies to apps, never the dashboard or SSH.
		if a.PublicPort > 0 && a.PublicPort != dashboardRemotePort {
			add(tunnel.Exposure{Proto: tunnel.ProtoHTTPS, Port: a.PublicPort, LocalPort: a.HostPort, Public: true}, "App "+a.Name+" (public)")
		}
	}
	return out, nil
}

func sameExposure(a store.AppliedExposure, e tunnel.Exposure) bool {
	return a.Proto == e.Proto && a.Port == e.Port && a.LocalPort == e.LocalPort && a.Public == e.Public
}

func toExposure(a store.AppliedExposure) tunnel.Exposure {
	return tunnel.Exposure{Proto: a.Proto, Port: a.Port, LocalPort: a.LocalPort, Public: a.Public}
}

// reconcile makes the provider match the desired exposures. It only removes
// exposures LPS itself created (recorded in the exposures table).
func (s *Server) reconcile(ctx context.Context) {
	if s.tunnel == nil {
		return
	}
	s.remote.sync.Lock()
	defer s.remote.sync.Unlock()

	var needs *tunnel.NeedsActionError
	portErrs := map[int]string{}
	lastErr := ""
	setNeeds := func(err error) bool {
		if na, ok := tunnel.AsNeedsAction(err); ok {
			if needs == nil {
				needs = na
			}
			return true
		}
		return false
	}

	status, err := s.tunnel.Status(ctx)
	if err != nil {
		lastErr = err.Error()
	}
	desired, err := s.desiredExposures(ctx)
	if err != nil {
		lastErr = err.Error()
	}
	applied, err := s.store.ListExposures(ctx)
	if err != nil {
		lastErr = err.Error()
	}
	appliedByPort := map[int]store.AppliedExposure{}
	for _, a := range applied {
		appliedByPort[a.Port] = a
	}

	ready := status.Installed && status.Running
	if len(desired) > 0 && !ready {
		setNeeds(tunnel.CheckReady(status, tunnel.Exposure{Proto: tunnel.ProtoTCP}))
	}

	if ready {
		// 1. Remove what is no longer wanted (or changed).
		for _, a := range applied {
			if d, ok := desired[a.Port]; ok && sameExposure(a, d.Exposure) {
				continue
			}
			if err := s.tunnel.Remove(ctx, toExposure(a)); err != nil {
				portErrs[a.Port] = "remove: " + err.Error()
				continue
			}
			_ = s.store.DeleteExposure(ctx, a.Proto, a.Port)
			delete(appliedByPort, a.Port)
		}

		// 2. Apply what is missing.
		used, err := s.tunnel.UsedPorts(ctx)
		if err != nil {
			lastErr = err.Error()
			used = map[int]bool{}
		}
		ports := make([]int, 0, len(desired))
		for p := range desired {
			ports = append(ports, p)
		}
		sort.Ints(ports)
		for _, p := range ports {
			d := desired[p]
			if a, ok := appliedByPort[p]; ok && sameExposure(a, d.Exposure) {
				if !used[p] {
					// Someone removed it outside LPS: re-apply below.
					_ = s.store.DeleteExposure(ctx, a.Proto, a.Port)
				} else {
					continue
				}
			} else if used[p] {
				portErrs[p] = fmt.Sprintf("port %d is already used by another Tailscale serve/funnel setting on this PC; LPS left it alone", p)
				continue
			}
			if err := s.tunnel.Apply(ctx, d.Exposure); err != nil {
				if !setNeeds(err) {
					portErrs[p] = err.Error()
				} else {
					portErrs[p] = "waiting: " + err.Error()
				}
				continue
			}
			if err := s.store.SaveExposure(ctx, store.AppliedExposure{
				Proto: d.Proto, Port: d.Port, LocalPort: d.LocalPort, Public: d.Public,
			}); err != nil {
				portErrs[p] = err.Error()
			}
		}
	}

	if lastErr != "" {
		s.log.Warn("remote access sync", "err", lastErr)
	}
	s.remote.mu.Lock()
	s.remote.status, s.remote.needs, s.remote.lastErr, s.remote.portErrs = status, needs, lastErr, portErrs
	s.remote.lastSync = time.Now()
	s.remote.mu.Unlock()
}

// kickRemote schedules an asynchronous reconcile (non-blocking).
func (s *Server) kickRemote() {
	if s.tunnel == nil {
		return
	}
	select {
	case s.remoteKick <- struct{}{}:
	default: // one is already pending
	}
}

// remoteLoop reconciles on demand and every minute, so changes made in the
// Tailscale admin console (enabling HTTPS/Funnel) are picked up.
func (s *Server) remoteLoop() {
	defer s.wg.Done()
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	s.reconcile(s.ctx)
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-s.remoteKick:
		case <-t.C:
		}
		s.reconcile(s.ctx)
	}
}

// remoteSnapshot returns the cached DNS name and the set of active remote ports.
func (s *Server) remoteSnapshot(ctx context.Context) (dns string, active map[int]store.AppliedExposure) {
	active = map[int]store.AppliedExposure{}
	if !s.remoteEnabled(ctx) {
		return "", active
	}
	s.remote.mu.Lock()
	dns = s.remote.status.DNSName
	errs := s.remote.portErrs
	s.remote.mu.Unlock()
	applied, err := s.store.ListExposures(ctx)
	if err != nil {
		return dns, active
	}
	for _, a := range applied {
		if _, failed := errs[a.Port]; !failed {
			active[a.Port] = a
		}
	}
	return dns, active
}

func remoteURL(dns string, e tunnel.Exposure) string {
	if dns == "" {
		return ""
	}
	if e.Proto == tunnel.ProtoTCP {
		return fmt.Sprintf("%s:%d", dns, e.Port)
	}
	if e.Port == 443 {
		return "https://" + dns
	}
	return fmt.Sprintf("https://%s:%d", dns, e.Port)
}

func (s *Server) buildRemoteView(ctx context.Context, freshStatus bool) remoteView {
	v := remoteView{Exposures: []exposureView{}}
	if s.tunnel == nil {
		return v
	}
	v.Provider, v.Available, v.Enabled = s.tunnel.Name(), true, s.remoteEnabled(ctx)

	s.remote.mu.Lock()
	v.Status, v.NeedsAction, v.Error = s.remote.status, s.remote.needs, s.remote.lastErr
	portErrs := s.remote.portErrs
	if !s.remote.lastSync.IsZero() {
		t := s.remote.lastSync
		v.LastSync = &t
	}
	s.remote.mu.Unlock()

	if freshStatus {
		if st, err := s.tunnel.Status(ctx); err == nil {
			v.Status = st
			s.remote.mu.Lock()
			s.remote.status = st
			s.remote.mu.Unlock()
		}
	}
	// Show setup guidance even when remote access is off.
	if v.NeedsAction == nil && (!v.Status.Installed || !v.Status.Running) {
		v.NeedsAction, _ = tunnel.AsNeedsAction(tunnel.CheckReady(v.Status, tunnel.Exposure{Proto: tunnel.ProtoTCP}))
	}
	if v.NeedsAction == nil && v.Status.Running && !v.Status.HTTPSEnabled {
		v.NeedsAction, _ = tunnel.AsNeedsAction(tunnel.CheckReady(v.Status, tunnel.Exposure{Proto: tunnel.ProtoHTTPS}))
	}

	desired, _ := s.desiredExposures(ctx)
	applied, _ := s.store.ListExposures(ctx)
	appliedByPort := map[int]store.AppliedExposure{}
	for _, a := range applied {
		appliedByPort[a.Port] = a
	}
	ports := make([]int, 0, len(desired))
	for p := range desired {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	for _, p := range ports {
		d := desired[p]
		ev := exposureView{Exposure: d.Exposure, Label: d.label, State: "pending", URL: remoteURL(v.Status.DNSName, d.Exposure)}
		if a, ok := appliedByPort[p]; ok && sameExposure(a, d.Exposure) {
			ev.State = "active"
		}
		if e, ok := portErrs[p]; ok {
			ev.State, ev.Error = "error", e
		}
		if p == dashboardRemotePort && ev.State == "active" {
			v.DashboardURL = ev.URL
		}
		v.Exposures = append(v.Exposures, ev)
	}
	return v
}

func (s *Server) handleGetRemote(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, s.buildRemoteView(r.Context(), true))
}

func (s *Server) handleSetRemote(w http.ResponseWriter, r *http.Request) {
	if s.tunnel == nil {
		writeError(w, http.StatusNotImplemented, "remote access is not available in this build")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if !decode(w, r, &req) {
		return
	}
	val := "false"
	if req.Enabled {
		val = "true"
	}
	if err := s.store.SetSetting(r.Context(), store.SettingRemoteEnabled, val); err != nil {
		s.internalError(w, err)
		return
	}
	s.freshStatus()
	s.reconcile(r.Context())
	if req.Enabled {
		s.info(r, "remote.enabled", subjRemote, 0, "", "Remote access turned on")
	} else {
		s.info(r, "remote.disabled", subjRemote, 0, "", "Remote access turned off")
	}
	writeJSON(w, 200, s.buildRemoteView(r.Context(), false))
}

// freshStatus makes the next provider Status call bypass its cache, if any.
func (s *Server) freshStatus() {
	if inv, ok := s.tunnel.(interface{ InvalidateStatus() }); ok {
		inv.InvalidateStatus()
	}
}

func (s *Server) handleSyncRemote(w http.ResponseWriter, r *http.Request) {
	if s.tunnel != nil {
		s.freshStatus()
	}
	s.reconcile(r.Context())
	writeJSON(w, 200, s.buildRemoteView(r.Context(), false))
}

// handleSetAppPublic turns an app's public (Funnel) link on or off.
func (s *Server) handleSetAppPublic(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req struct {
		Public bool `json:"public"`
	}
	if !decode(w, r, &req) {
		return
	}
	ctx := r.Context()
	if req.Public && a.PublicPort == 0 {
		if !s.remoteEnabled(ctx) {
			writeError(w, http.StatusConflict, "turn on remote access first")
			return
		}
		st, err := s.tunnel.Status(ctx)
		if err != nil {
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		if err := tunnel.CheckReady(st, tunnel.Exposure{Proto: tunnel.ProtoHTTPS, Public: true}); err != nil {
			if na, ok := tunnel.AsNeedsAction(err); ok {
				writeJSON(w, http.StatusPreconditionFailed, map[string]any{"error": na.Message, "needsAction": na})
				return
			}
		}
		port, err := s.pickPublicPort(ctx, st.FunnelPorts)
		if err != nil {
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		a.PublicPort = port
	} else if !req.Public {
		a.PublicPort = 0
	}
	if err := s.store.SetAppPublicPort(ctx, a.ID, a.PublicPort); err != nil {
		s.internalError(w, err)
		return
	}
	s.reconcile(ctx)
	if req.Public {
		s.warn(r, "app.public_on", subjApp, a.ID, a.Name, "Public link turned on for %s", a.Name)
	} else {
		s.info(r, "app.public_off", subjApp, a.ID, a.Name, "Public link turned off for %s", a.Name)
	}
	writeJSON(w, 200, s.appView(r, a))
}

var errNoPublicPort = errors.New("no free public port: Tailscale Funnel allows only a few public ports per PC, and they are all in use. Turn off another app's public link first")

func (s *Server) pickPublicPort(ctx context.Context, allowed []int) (int, error) {
	apps, err := s.store.ListApps(ctx)
	if err != nil {
		return 0, err
	}
	taken := map[int]bool{dashboardRemotePort: true}
	for _, a := range apps {
		taken[a.PublicPort] = true
	}
	for _, p := range allowed {
		if !taken[p] {
			return p, nil
		}
	}
	return 0, errNoPublicPort
}

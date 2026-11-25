package api

// Custom domains: apps at addresses on the user's own domain
// (https://blog.example.com), through a Cloudflare Tunnel in their Cloudflare
// account. The user gives LPS an API token once; LPS creates the tunnel, runs
// its connector (cloudflared) in Docker, and for each app adds a proxied DNS
// record and a route to the app's container. Nothing is published on the PC.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/abrardw/lps/internal/cloudflare"
	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/store"
)

type cfState struct {
	sync       sync.Mutex // serialises config pushes and reconciles
	mu         sync.Mutex
	pushed     string // the routes last sent to Cloudflare (JSON)
	lastErr    string
	zones      []cloudflare.Zone
	zonesToken string
	zonesAt    time.Time
	kick       chan struct{}
}

var cfInterval = time.Minute

const zonesTTL = 2 * time.Minute

func (s *Server) kickCF() {
	select {
	case s.cf.kick <- struct{}{}:
	default:
	}
}

func (s *Server) cfClient(token string) *cloudflare.Client {
	return cloudflare.New(token, s.cfg.CloudflareAPI)
}

// cfZones lists the user's domains, cached for a short while.
func (s *Server) cfZones(ctx context.Context, token string, fresh bool) ([]cloudflare.Zone, error) {
	s.cf.mu.Lock()
	if !fresh && s.cf.zonesToken == token && time.Since(s.cf.zonesAt) < zonesTTL {
		z := s.cf.zones
		s.cf.mu.Unlock()
		return z, nil
	}
	s.cf.mu.Unlock()
	zones, err := s.cfClient(token).Zones(ctx)
	if err != nil {
		return nil, err
	}
	s.cf.mu.Lock()
	s.cf.zones, s.cf.zonesToken, s.cf.zonesAt = zones, token, time.Now()
	s.cf.mu.Unlock()
	return zones, nil
}

// desiredRoutes are the tunnel routes for every app with a domain.
func (s *Server) desiredRoutes(ctx context.Context) ([]cloudflare.Ingress, error) {
	domains, err := s.store.AppDomains(ctx)
	if err != nil {
		return nil, err
	}
	routes := []cloudflare.Ingress{}
	for _, d := range domains {
		a, err := s.store.GetApp(ctx, d.AppID)
		if err != nil {
			continue
		}
		spec, err := s.originSpec(ctx, a)
		if err != nil {
			continue
		}
		routes = append(routes, cloudflare.Ingress{Hostname: d.Hostname, Service: spec.Origin()})
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].Hostname < routes[j].Hostname })
	return routes, nil
}

// pushRoutes sends the routes to Cloudflare if they changed (or force).
// The caller holds s.cf.sync.
func (s *Server) pushRoutes(ctx context.Context, c store.CloudflareSettings, force bool) error {
	routes, err := s.desiredRoutes(ctx)
	if err != nil {
		return err
	}
	b, _ := json.Marshal(routes)
	s.cf.mu.Lock()
	same := s.cf.pushed == string(b)
	s.cf.mu.Unlock()
	if same && !force {
		return nil
	}
	if err := s.cfClient(c.Token).PutConfig(ctx, c.Account, c.Tunnel, routes); err != nil {
		return err
	}
	s.cf.mu.Lock()
	s.cf.pushed = string(b)
	s.cf.mu.Unlock()
	return nil
}

// reconcileCF keeps the connector running, apps reachable on lps-net and
// the tunnel's routes up to date.
func (s *Server) reconcileCF(ctx context.Context) {
	s.cf.sync.Lock()
	defer s.cf.sync.Unlock()
	c, err := s.store.Cloudflare(ctx)
	if err != nil {
		return
	}
	setErr := func(e string) {
		s.cf.mu.Lock()
		s.cf.lastErr = e
		s.cf.mu.Unlock()
	}
	if !c.Connected() {
		_ = s.engine.RemoveTunnelConnector(ctx)
		setErr("")
		return
	}
	var errs []string
	if err := s.engine.EnsureTunnelConnector(ctx, c.TunnelToken); err != nil {
		errs = append(errs, "start the connector: "+err.Error())
	}
	domains, _ := s.store.AppDomains(ctx)
	for _, d := range domains {
		a, err := s.store.GetApp(ctx, d.AppID)
		if err != nil || a.ContainerID == "" {
			continue // not deployed yet: the route waits for it
		}
		if spec, err := s.originSpec(ctx, a); err == nil {
			// A redeployed app container must join lps-net again.
			if err := s.engine.EnsureNetwork(ctx, spec.Target); err != nil {
				errs = append(errs, a.Name+": "+err.Error())
			}
		}
	}
	if err := s.pushRoutes(ctx, c, false); err != nil {
		errs = append(errs, "update the tunnel's routes: "+err.Error())
	}
	setErr(strings.Join(errs, "; "))
	if len(errs) > 0 && ctx.Err() == nil {
		s.log.Warn("custom domains", "err", strings.Join(errs, "; "))
	}
}

func (s *Server) cfLoop() {
	defer s.wg.Done()
	t := time.NewTicker(cfInterval)
	defer t.Stop()
	for {
		s.reconcileCF(s.ctx)
		select {
		case <-s.ctx.Done():
			return
		case <-s.cf.kick:
		case <-t.C:
		}
	}
}

// ---- views ----

type cfZoneView struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type cfDomainView struct {
	AppID    int64  `json:"appId"`
	AppName  string `json:"appName"`
	Hostname string `json:"hostname"`
	URL      string `json:"url"`
}

type cfView struct {
	Connected   bool              `json:"connected"`
	AccountName string            `json:"accountName,omitempty"`
	TunnelName  string            `json:"tunnelName,omitempty"`
	TunnelID    string            `json:"tunnelId,omitempty"`
	Zones       []cfZoneView      `json:"zones"`
	ZonesError  string            `json:"zonesError,omitempty"`
	Connector   *docker.Connector `json:"connector,omitempty"`
	Error       string            `json:"error,omitempty"`
	Domains     []cfDomainView    `json:"domains"`
	Accounts    []cfAccountChoice `json:"accounts,omitempty"` // when the token sees several accounts
}

type cfAccountChoice struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

func (s *Server) buildCFView(ctx context.Context) (cfView, error) {
	v := cfView{Zones: []cfZoneView{}, Domains: []cfDomainView{}}
	c, err := s.store.Cloudflare(ctx)
	if err != nil {
		return v, err
	}
	if !c.Connected() {
		return v, nil
	}
	v.Connected, v.AccountName, v.TunnelName, v.TunnelID = true, c.AccountName, c.TunnelName, c.Tunnel
	if zones, err := s.cfZones(ctx, c.Token, false); err != nil {
		v.ZonesError = err.Error()
	} else {
		for _, z := range zones {
			if z.Account.ID == c.Account {
				v.Zones = append(v.Zones, cfZoneView{ID: z.ID, Name: z.Name})
			}
		}
	}
	if st, err := s.engine.TunnelConnectorStatus(ctx); err == nil {
		v.Connector = &st
	} else {
		v.Connector = &docker.Connector{State: docker.QuickError, Error: err.Error()}
	}
	s.cf.mu.Lock()
	v.Error = s.cf.lastErr
	s.cf.mu.Unlock()
	domains, err := s.store.AppDomains(ctx)
	if err != nil {
		return v, err
	}
	for _, d := range domains {
		name := ""
		if a, err := s.store.GetApp(ctx, d.AppID); err == nil {
			name = a.Name
		}
		v.Domains = append(v.Domains, cfDomainView{AppID: d.AppID, AppName: name, Hostname: d.Hostname, URL: "https://" + d.Hostname})
	}
	return v, nil
}

func (s *Server) handleGetCloudflare(w http.ResponseWriter, r *http.Request) {
	v, err := s.buildCFView(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, v)
}

// ---- connect / disconnect ----

var tunnelNameRe = regexp.MustCompile(`[^a-z0-9-]+`)

func tunnelName() string {
	h, _ := os.Hostname()
	h = strings.Trim(tunnelNameRe.ReplaceAllString(strings.ToLower(h), "-"), "-")
	if len(h) > 30 {
		h = h[:30]
	}
	if h == "" {
		h = "pc"
	}
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return "lps-" + h + "-" + hex.EncodeToString(b)
}

// handleConnectCloudflare checks the token, creates a tunnel and starts its connector.
func (s *Server) handleConnectCloudflare(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token     string `json:"token"`
		AccountID string `json:"accountId"`
	}
	if !decode(w, r, &req) {
		return
	}
	req.Token = strings.TrimSpace(req.Token)
	if req.Token == "" || len(req.Token) > 512 || strings.ContainsAny(req.Token, " \t\r\n") {
		writeError(w, http.StatusBadRequest, "paste the API token you created in Cloudflare")
		return
	}
	ctx := r.Context()
	s.cf.sync.Lock()
	defer s.cf.sync.Unlock()
	if c, err := s.store.Cloudflare(ctx); err != nil {
		s.internalError(w, err)
		return
	} else if c.Connected() {
		writeError(w, http.StatusConflict, "already connected to Cloudflare; disconnect first to use another account")
		return
	}
	zones, err := s.cfZones(ctx, req.Token, true)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	accounts := map[string]string{}
	var order []string
	for _, z := range zones {
		if _, ok := accounts[z.Account.ID]; !ok {
			accounts[z.Account.ID] = z.Account.Name
			order = append(order, z.Account.ID)
		}
	}
	if len(zones) == 0 {
		writeError(w, http.StatusBadRequest, "this token can't see any of your domains. In Cloudflare, give it Zone → Zone → Read and Zone → DNS → Edit for your domain (Zone Resources), and make sure the domain is added to your Cloudflare account")
		return
	}
	account := req.AccountID
	if account == "" {
		if len(order) > 1 {
			choices := []cfAccountChoice{}
			for _, id := range order {
				choices = append(choices, cfAccountChoice{ID: id, Name: accounts[id]})
			}
			writeJSON(w, http.StatusConflict, map[string]any{"error": "this token can see domains in several Cloudflare accounts: choose one", "accounts": choices})
			return
		}
		account = order[0]
	} else if _, ok := accounts[account]; !ok {
		writeError(w, http.StatusBadRequest, "unknown account")
		return
	}

	cf := s.cfClient(req.Token)
	t, err := cf.CreateTunnel(ctx, account, tunnelName())
	if err != nil {
		writeError(w, http.StatusBadGateway, "create the tunnel: "+err.Error())
		return
	}
	tok, err := cf.TunnelToken(ctx, account, t.ID)
	if err == nil {
		err = cf.PutConfig(ctx, account, t.ID, nil)
	}
	if err != nil {
		_ = cf.DeleteTunnel(context.WithoutCancel(ctx), account, t.ID)
		writeError(w, http.StatusBadGateway, "set up the tunnel: "+err.Error())
		return
	}
	c := store.CloudflareSettings{Token: req.Token, Account: account, AccountName: accounts[account], Tunnel: t.ID, TunnelName: t.Name, TunnelToken: tok}
	if err := s.store.DeleteAllAppDomains(ctx); err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.store.SetCloudflare(ctx, c); err != nil {
		s.internalError(w, err)
		return
	}
	s.cf.mu.Lock()
	s.cf.pushed = "[]"
	s.cf.mu.Unlock()
	s.info(r, "cloudflare.connected", subjRemote, 0, "", "Connected Cloudflare account %s (tunnel %s)", accounts[account], t.Name)
	s.kickCF()
	v, err := s.buildCFView(ctx)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, 200, v)
}

// handleDisconnectCloudflare removes LPS's DNS records, connector and tunnel.
func (s *Server) handleDisconnectCloudflare(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	s.cf.sync.Lock()
	defer s.cf.sync.Unlock()
	c, err := s.store.Cloudflare(ctx)
	if err != nil {
		s.internalError(w, err)
		return
	}
	var problems []string
	if c.Token != "" {
		cf := s.cfClient(c.Token)
		domains, _ := s.store.AppDomains(ctx)
		for _, d := range domains {
			if d.RecordID == "" {
				continue
			}
			if err := cf.DeleteRecord(ctx, d.ZoneID, d.RecordID); err != nil {
				problems = append(problems, fmt.Sprintf("the DNS record for %s (%v)", d.Hostname, err))
			}
		}
		if err := s.engine.RemoveTunnelConnector(ctx); err != nil {
			problems = append(problems, "the connector container ("+err.Error()+")")
		}
		if c.Tunnel != "" {
			// Cloudflare refuses while the connector's connections linger: retry briefly.
			var err error
			for i := 0; i < 5; i++ {
				if err = cf.DeleteTunnel(ctx, c.Account, c.Tunnel); err == nil {
					break
				}
				time.Sleep(2 * time.Second)
			}
			if err != nil {
				problems = append(problems, fmt.Sprintf("the tunnel %s (%v); delete it in Cloudflare under Networks → Tunnels", c.TunnelName, err))
			}
		}
	} else {
		_ = s.engine.RemoveTunnelConnector(ctx)
	}
	if err := s.store.DeleteAllAppDomains(ctx); err != nil {
		s.internalError(w, err)
		return
	}
	if err := s.store.SetCloudflare(ctx, store.CloudflareSettings{}); err != nil {
		s.internalError(w, err)
		return
	}
	s.cf.mu.Lock()
	s.cf.pushed, s.cf.lastErr, s.cf.zones, s.cf.zonesToken = "", "", nil, ""
	s.cf.mu.Unlock()
	s.info(r, "cloudflare.disconnected", subjRemote, 0, "", "Disconnected Cloudflare account %s", c.AccountName)
	v, _ := s.buildCFView(ctx)
	out := map[string]any{"view": v}
	if len(problems) > 0 {
		out["warning"] = "LPS couldn't remove " + strings.Join(problems, ", ")
	}
	writeJSON(w, 200, out)
}

// ---- app domains ----

var hostLabelRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// normalizeHostname accepts "blog.example.com" (or a pasted URL) and
// returns the lower-case host name.
func normalizeHostname(in string) (string, error) {
	h := strings.ToLower(strings.TrimSpace(in))
	h = strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://")
	if i := strings.IndexAny(h, "/?#"); i >= 0 {
		h = h[:i]
	}
	h = strings.TrimSuffix(h, ".")
	if h == "" {
		return "", errors.New("enter an address such as blog.example.com")
	}
	if len(h) > 253 || strings.Contains(h, ":") {
		return "", fmt.Errorf("%q isn't a valid address", in)
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 {
		return "", errors.New("enter a full address such as blog.example.com")
	}
	for _, l := range labels {
		if !hostLabelRe.MatchString(l) {
			return "", fmt.Errorf("%q isn't a valid address: use letters, digits and hyphens between the dots", in)
		}
	}
	return h, nil
}

// zoneFor finds the user's domain a hostname belongs to (the longest match).
func zoneFor(host string, zones []cloudflare.Zone) *cloudflare.Zone {
	var best *cloudflare.Zone
	for i, z := range zones {
		n := strings.ToLower(z.Name)
		if host == n || strings.HasSuffix(host, "."+n) {
			if best == nil || len(n) > len(best.Name) {
				best = &zones[i]
			}
		}
	}
	return best
}

type domainError struct {
	status int
	msg    string
}

func (e *domainError) Error() string { return e.msg }

// setAppDomain points host at the app. The caller holds s.cf.sync.
func (s *Server) setAppDomain(ctx context.Context, c store.CloudflareSettings, a *store.App, host string) error {
	zones, err := s.cfZones(ctx, c.Token, false)
	if err != nil {
		return &domainError{http.StatusBadGateway, err.Error()}
	}
	var own []cloudflare.Zone
	var names []string
	for _, z := range zones {
		if z.Account.ID == c.Account {
			own = append(own, z)
			names = append(names, z.Name)
		}
	}
	z := zoneFor(host, own)
	if z == nil {
		return &domainError{http.StatusBadRequest, fmt.Sprintf("%s isn't on one of your Cloudflare domains (%s)", host, strings.Join(names, ", "))}
	}
	if old, _ := s.store.AppDomain(ctx, a.ID); old != nil && old.Hostname == host {
		return nil
	}
	cf := s.cfClient(c.Token)
	recs, err := cf.Records(ctx, z.ID, host)
	if err != nil {
		return &domainError{http.StatusBadGateway, err.Error()}
	}
	target := cloudflare.TunnelTarget(c.Tunnel)
	recordID, created := "", false
	for _, rec := range recs {
		if strings.EqualFold(rec.Type, "CNAME") && strings.EqualFold(rec.Content, target) {
			if rec.Comment == cloudflare.RecordComment {
				recordID = rec.ID // ours from before: take it over
			} else {
				recordID = "-" // the user's own record already points here: keep it
			}
			continue
		}
		return &domainError{http.StatusConflict, fmt.Sprintf("%s already has a DNS record in Cloudflare (%s → %s). Delete it there first, or choose another address", host, rec.Type, rec.Content)}
	}
	if recordID == "" {
		recordID, err = cf.CreateCNAME(ctx, z.ID, host, target)
		if err != nil {
			return &domainError{http.StatusBadGateway, "add the DNS record: " + err.Error()}
		}
		created = true
	}
	if recordID == "-" {
		recordID = ""
	}
	old, _ := s.store.AppDomain(ctx, a.ID)
	undo := func() {
		if created {
			_ = cf.DeleteRecord(context.WithoutCancel(ctx), z.ID, recordID)
		}
	}
	if err := s.store.SaveAppDomain(ctx, &store.AppDomain{AppID: a.ID, Hostname: host, ZoneID: z.ID, RecordID: recordID}); err != nil {
		undo()
		if errors.Is(err, store.ErrDomainTaken) {
			return &domainError{http.StatusConflict, host + ": " + err.Error()}
		}
		return err
	}
	if err := s.pushRoutes(ctx, c, false); err != nil {
		// Put things back as they were.
		if old != nil {
			_ = s.store.SaveAppDomain(ctx, old)
		} else {
			_ = s.store.DeleteAppDomain(ctx, a.ID)
		}
		undo()
		return &domainError{http.StatusBadGateway, "update the tunnel's routes: " + err.Error()}
	}
	if old != nil && old.RecordID != "" {
		if err := cf.DeleteRecord(ctx, old.ZoneID, old.RecordID); err != nil {
			s.log.Warn("remove old DNS record", "host", old.Hostname, "err", err)
		}
	}
	return nil
}

// removeAppDomain stops serving the app's domain. The caller holds s.cf.sync.
func (s *Server) removeAppDomain(ctx context.Context, c store.CloudflareSettings, a *store.App) (*store.AppDomain, error) {
	d, err := s.store.AppDomain(ctx, a.ID)
	if err != nil || d == nil {
		return nil, err
	}
	if err := s.store.DeleteAppDomain(ctx, a.ID); err != nil {
		return nil, err
	}
	if !c.Connected() {
		return d, nil
	}
	if err := s.pushRoutes(ctx, c, false); err != nil {
		s.cf.mu.Lock()
		s.cf.lastErr = "update the tunnel's routes: " + err.Error()
		s.cf.mu.Unlock()
	}
	if d.RecordID != "" {
		if err := s.cfClient(c.Token).DeleteRecord(ctx, d.ZoneID, d.RecordID); err != nil {
			return d, fmt.Errorf("the address is off, but LPS couldn't delete its DNS record in Cloudflare: %w", err)
		}
	}
	return d, nil
}

func (s *Server) handleSetAppDomain(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	var req struct {
		Hostname string `json:"hostname"`
	}
	if !decode(w, r, &req) {
		return
	}
	host, err := normalizeHostname(req.Hostname)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx := r.Context()
	s.cf.sync.Lock()
	c, err := s.store.Cloudflare(ctx)
	if err == nil && !c.Connected() {
		err = &domainError{http.StatusConflict, "connect your Cloudflare account first (Remote access → Your domain)"}
	}
	if err == nil {
		err = s.setAppDomain(ctx, c, a, host)
	}
	s.cf.sync.Unlock()
	var de *domainError
	switch {
	case errors.As(err, &de):
		writeError(w, de.status, de.msg)
		return
	case err != nil:
		s.internalError(w, err)
		return
	}
	s.warn(r, "app.domain_on", subjApp, a.ID, a.Name, "%s is public at https://%s", a.Name, host)
	s.kickCF()
	writeJSON(w, 200, s.appView(r, a))
}

func (s *Server) handleDeleteAppDomain(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	s.cf.sync.Lock()
	c, err := s.store.Cloudflare(ctx)
	var d *store.AppDomain
	if err == nil {
		d, err = s.removeAppDomain(ctx, c, a)
	}
	s.cf.sync.Unlock()
	if d != nil {
		s.info(r, "app.domain_off", subjApp, a.ID, a.Name, "Removed address https://%s from %s", d.Hostname, a.Name)
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, 200, s.appView(r, a))
}

// dropAppDomain is used when an app is deleted.
func (s *Server) dropAppDomain(ctx context.Context, a *store.App) {
	s.cf.sync.Lock()
	defer s.cf.sync.Unlock()
	c, err := s.store.Cloudflare(ctx)
	if err != nil {
		return
	}
	if _, err := s.removeAppDomain(ctx, c, a); err != nil {
		s.log.Warn("remove app domain", "app", a.Name, "err", err)
	}
}

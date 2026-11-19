package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/abrardw/lps/internal/tunnel"
)

// fakeTunnel is an in-memory tunnel.Provider.
type fakeTunnel struct {
	mu       sync.Mutex
	status   tunnel.Status
	live     map[int]tunnel.Exposure
	external map[int]bool // ports configured outside LPS
	applies  []tunnel.Exposure
}

func newFakeTunnel() *fakeTunnel {
	return &fakeTunnel{
		status: tunnel.Status{Installed: true, Running: true, State: "Running", DNSName: "home-pc.tail1234.ts.net",
			HTTPSEnabled: true, FunnelEnabled: true, FunnelPorts: []int{443, 8443, 10000}},
		live: map[int]tunnel.Exposure{}, external: map[int]bool{},
	}
}

func (f *fakeTunnel) Name() string { return "tailscale" }
func (f *fakeTunnel) Status(context.Context) (tunnel.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status, nil
}
func (f *fakeTunnel) Apply(_ context.Context, e tunnel.Exposure) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := tunnel.CheckReady(f.status, e); err != nil {
		return err
	}
	f.applies = append(f.applies, e)
	f.live[e.Port] = e
	return nil
}
func (f *fakeTunnel) Remove(_ context.Context, e tunnel.Exposure) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.live, e.Port)
	return nil
}
func (f *fakeTunnel) UsedPorts(context.Context) (map[int]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	used := map[int]bool{}
	for p := range f.live {
		used[p] = true
	}
	for p := range f.external {
		used[p] = true
	}
	return used, nil
}
func (f *fakeTunnel) snapshot() map[int]tunnel.Exposure {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[int]tunnel.Exposure{}
	for k, v := range f.live {
		out[k] = v
	}
	return out
}

func appDir(t *testing.T) string {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM nginx\n"), 0o644)
	return dir
}

func TestRemoteLifecycle(t *testing.T) {
	ft := newFakeTunnel()
	h := setupWith(t, ft)
	h.login()

	var v vpsView
	h.do("POST", "/api/vps", createVPSReq{Name: "box", SSHUser: "dev", Password: "password1"}, &v)
	var a appView
	h.do("POST", "/api/apps", createAppReq{Name: "site", SourcePath: appDir(t), ContainerPort: 80}, &a)

	// Off by default: nothing exposed.
	h.do("POST", "/api/remote/sync", nil, nil)
	if n := len(ft.snapshot()); n != 0 {
		t.Fatalf("exposed while disabled: %d", n)
	}

	var rv remoteView
	if code := h.do("PUT", "/api/remote", map[string]bool{"enabled": true}, &rv); code != 200 {
		t.Fatalf("enable: %d", code)
	}
	live := ft.snapshot()
	want := map[int]tunnel.Exposure{
		443:  {Proto: "https", Port: 443, LocalPort: 7070},
		2201: {Proto: "tcp", Port: 2201, LocalPort: 2201},
		8100: {Proto: "https", Port: 8100, LocalPort: 8100},
	}
	if fmt.Sprint(live) != fmt.Sprint(want) {
		t.Fatalf("live = %v\nwant %v", live, want)
	}
	if rv.DashboardURL != "https://home-pc.tail1234.ts.net" {
		t.Fatalf("dashboard url = %q", rv.DashboardURL)
	}
	for _, e := range rv.Exposures {
		if e.State != "active" {
			t.Fatalf("exposure not active: %+v", e)
		}
	}

	var gotV vpsView
	h.do("GET", fmt.Sprintf("/api/vps/%d", v.ID), nil, &gotV)
	if gotV.RemoteSSH != "ssh dev@home-pc.tail1234.ts.net -p 2201" {
		t.Fatalf("remote ssh = %q", gotV.RemoteSSH)
	}
	var gotA appView
	h.do("GET", fmt.Sprintf("/api/apps/%d", a.ID), nil, &gotA)
	if gotA.RemoteURL != "https://home-pc.tail1234.ts.net:8100" || gotA.PublicURL != "" {
		t.Fatalf("app urls = %q %q", gotA.RemoteURL, gotA.PublicURL)
	}

	// Deleting a VPS removes its exposure.
	h.do("DELETE", fmt.Sprintf("/api/vps/%d", v.ID), nil, nil)
	h.do("POST", "/api/remote/sync", nil, nil)
	if _, ok := ft.snapshot()[2201]; ok {
		t.Fatal("ssh exposure not removed after delete")
	}

	// Disabling removes everything LPS created.
	h.do("PUT", "/api/remote", map[string]bool{"enabled": false}, nil)
	if n := len(ft.snapshot()); n != 0 {
		t.Fatalf("still exposed after disable: %v", ft.snapshot())
	}
}

func TestRemotePublicLinks(t *testing.T) {
	ft := newFakeTunnel()
	h := setupWith(t, ft)
	h.login()

	var apps [3]appView
	for i := range apps {
		h.do("POST", "/api/apps", createAppReq{Name: fmt.Sprintf("site%d", i), SourcePath: appDir(t), ContainerPort: 80}, &apps[i])
	}
	if code := h.do("PUT", fmt.Sprintf("/api/apps/%d/public", apps[0].ID), map[string]bool{"public": true}, nil); code != 409 {
		t.Fatalf("public while remote disabled: %d", code)
	}
	h.do("PUT", "/api/remote", map[string]bool{"enabled": true}, nil)

	var a0, a1 appView
	if code := h.do("PUT", fmt.Sprintf("/api/apps/%d/public", apps[0].ID), map[string]bool{"public": true}, &a0); code != 200 {
		t.Fatalf("public 0: %d", code)
	}
	h.do("PUT", fmt.Sprintf("/api/apps/%d/public", apps[1].ID), map[string]bool{"public": true}, &a1)
	if a0.PublicPort != 8443 || a1.PublicPort != 10000 {
		t.Fatalf("public ports = %d, %d", a0.PublicPort, a1.PublicPort)
	}
	if a0.PublicURL != "https://home-pc.tail1234.ts.net:8443" {
		t.Fatalf("public url = %q", a0.PublicURL)
	}
	if code := h.do("PUT", fmt.Sprintf("/api/apps/%d/public", apps[2].ID), map[string]bool{"public": true}, nil); code != 409 {
		t.Fatalf("third public app: %d", code)
	}
	live := ft.snapshot()
	if !live[8443].Public || live[8443].LocalPort != apps[0].HostPort || live[443].Public {
		t.Fatalf("live = %v", live)
	}
	// Funnel is never used for the dashboard or SSH.
	for _, e := range ft.applies {
		if e.Public && (e.Port == 443 || e.Proto == tunnel.ProtoTCP) {
			t.Fatalf("funnel applied to %v", e)
		}
	}
	// Turning it off frees the port for another app.
	h.do("PUT", fmt.Sprintf("/api/apps/%d/public", apps[0].ID), map[string]bool{"public": false}, nil)
	if _, ok := ft.snapshot()[8443]; ok {
		t.Fatal("public exposure not removed")
	}
	var a2 appView
	h.do("PUT", fmt.Sprintf("/api/apps/%d/public", apps[2].ID), map[string]bool{"public": true}, &a2)
	if a2.PublicPort != 8443 {
		t.Fatalf("reused port = %d", a2.PublicPort)
	}
}

func TestRemoteNeedsActionAndConflicts(t *testing.T) {
	ft := newFakeTunnel()
	ft.status.HTTPSEnabled, ft.status.FunnelEnabled = false, false
	ft.external[2201] = true // the user already serves something on 2201
	h := setupWith(t, ft)
	h.login()
	h.do("POST", "/api/vps", createVPSReq{Name: "box", SSHUser: "dev", Password: "password1"}, nil)

	var rv remoteView
	h.do("PUT", "/api/remote", map[string]bool{"enabled": true}, &rv)
	if rv.NeedsAction == nil || rv.NeedsAction.URL != tunnel.TailscaleHTTPSURL {
		t.Fatalf("needs action = %+v", rv.NeedsAction)
	}
	states := map[int]string{}
	for _, e := range rv.Exposures {
		states[e.Port] = e.State
	}
	if states[443] != "error" || states[2201] != "error" {
		t.Fatalf("states = %v", states)
	}
	if _, ok := ft.snapshot()[2201]; ok {
		t.Fatal("LPS must not touch a port configured outside LPS")
	}

	// The user enables HTTPS in the admin console; the next sync applies it.
	ft.mu.Lock()
	ft.status.HTTPSEnabled = true
	ft.mu.Unlock()
	var after remoteView
	h.do("POST", "/api/remote/sync", nil, &after)
	if _, ok := ft.snapshot()[443]; !ok || after.NeedsAction != nil {
		t.Fatalf("dashboard not exposed after enabling https: %v %+v", ft.snapshot(), after.NeedsAction)
	}
}

func TestRemoteNotInstalled(t *testing.T) {
	ft := newFakeTunnel()
	ft.status = tunnel.Status{}
	h := setupWith(t, ft)
	h.login()
	var rv remoteView
	h.do("GET", "/api/remote", nil, &rv)
	if !rv.Available || rv.Status.Installed || rv.NeedsAction == nil || rv.NeedsAction.URL != tunnel.TailscaleDownloadURL {
		t.Fatalf("view = %+v", rv)
	}
}

func TestClientIPAndHTTPS(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "127.0.0.1:5555"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 100.64.0.7")
	r.Header.Set("X-Forwarded-Proto", "https")
	if clientIP(r) != "100.64.0.7" || !isHTTPS(r) {
		t.Fatalf("via local proxy: ip=%s https=%v", clientIP(r), isHTTPS(r))
	}
	// Forwarding headers from non-loopback clients are ignored.
	r2 := httptest.NewRequest("GET", "/", nil)
	r2.RemoteAddr = "192.168.1.50:5555"
	r2.Header.Set("X-Forwarded-For", "100.64.0.7")
	r2.Header.Set("X-Forwarded-Proto", "https")
	if clientIP(r2) != "192.168.1.50" || isHTTPS(r2) {
		t.Fatalf("spoofed headers trusted: ip=%s https=%v", clientIP(r2), isHTTPS(r2))
	}
}

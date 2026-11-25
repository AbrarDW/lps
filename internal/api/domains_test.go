package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/abrardw/lps/internal/cloudflare"
	"github.com/abrardw/lps/internal/cloudflare/cftest"
	"github.com/abrardw/lps/internal/docker"
)

func (f *fakeEngine) EnsureTunnelConnector(_ context.Context, token string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connector = token
	return nil
}

func (f *fakeEngine) TunnelConnectorStatus(context.Context) (docker.Connector, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.connector == "" {
		return docker.Connector{State: docker.QuickMissing}, nil
	}
	return docker.Connector{State: docker.ConnectorConnected}, nil
}

func (f *fakeEngine) RemoveTunnelConnector(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.connector = ""
	return nil
}

func (f *fakeEngine) connectorToken() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.connector
}

type cfHarness struct {
	*harness
	cf *cftest.Fake
}

func setupCF(t *testing.T) *cfHarness {
	fake := cftest.New("good-token")
	ts := httptest.NewServer(fake)
	t.Cleanup(ts.Close)
	h := setupCfg(t, nil, func(c *Config) { c.CloudflareAPI = ts.URL + "/client/v4" })
	h.login()
	return &cfHarness{h, fake}
}

func (h *cfHarness) connect() cfView {
	h.t.Helper()
	var v cfView
	if code := h.do("POST", "/api/cloudflare", map[string]string{"token": " good-token\n"}, &v); code != 200 {
		h.t.Fatalf("connect: %d", code)
	}
	return v
}

func (h *cfHarness) app(name string) (appView, string) {
	h.t.Helper()
	dir := h.t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM nginx\n"), 0o644)
	var a appView
	h.do("POST", "/api/apps", createAppReq{Name: name, SourcePath: dir, ContainerPort: 80}, &a)
	path := fmt.Sprintf("/api/apps/%d", a.ID)
	return h.waitDeployed(path), path
}

func TestCloudflareConnect(t *testing.T) {
	h := setupCF(t)
	var v cfView
	h.do("GET", "/api/cloudflare", nil, &v)
	if v.Connected {
		t.Fatal("connected before connecting")
	}
	// A wrong token is explained.
	var e struct{ Error string }
	if code := h.do("POST", "/api/cloudflare", map[string]string{"token": "nope"}, &e); code != 400 || !strings.Contains(e.Error, "rejected the API token") {
		t.Fatalf("bad token: %d %q", code, e.Error)
	}

	v = h.connect()
	tunnels, configs, _ := h.cf.Snapshot()
	if len(tunnels) != 1 || !v.Connected || v.AccountName != "Home" || !strings.HasPrefix(v.TunnelName, "lps-") {
		t.Fatalf("view = %+v, tunnels = %v", v, tunnels)
	}
	if got, ok := configs[v.TunnelID]; !ok || len(got) != 0 {
		t.Fatalf("routes = %v", configs)
	}
	if len(v.Zones) != 2 || v.Zones[0].Name != "example.com" {
		t.Fatalf("zones = %+v", v.Zones)
	}
	waitFor(t, func() bool { return h.eng.connectorToken() == "token-for-"+v.TunnelID })
	// The token is never sent back.
	var raw map[string]any
	h.do("GET", "/api/cloudflare", nil, &raw)
	if strings.Contains(fmt.Sprint(raw), "good-token") || strings.Contains(fmt.Sprint(raw), "token-for-") {
		t.Fatalf("token leaked: %v", raw)
	}
	// Connecting twice is refused.
	if code := h.do("POST", "/api/cloudflare", map[string]string{"token": "good-token"}, nil); code != 409 {
		t.Fatalf("second connect: %d", code)
	}
}

func TestCloudflareSeveralAccounts(t *testing.T) {
	h := setupCF(t)
	var other cloudflare.Zone
	other.ID, other.Name, other.Status = "z9", "work.dev", "active"
	other.Account.ID, other.Account.Name = "acc2", "Work"
	h.cf.ZoneSet = append(h.cf.ZoneSet, other)
	var e struct {
		Error    string
		Accounts []cfAccountChoice
	}
	if code := h.do("POST", "/api/cloudflare", map[string]string{"token": "good-token"}, &e); code != 409 || len(e.Accounts) != 2 {
		t.Fatalf("%d %+v", code, e)
	}
	var v cfView
	if code := h.do("POST", "/api/cloudflare", map[string]string{"token": "good-token", "accountId": "acc2"}, &v); code != 200 {
		t.Fatalf("choose account: %d", code)
	}
	if v.AccountName != "Work" || len(v.Zones) != 1 || v.Zones[0].Name != "work.dev" {
		t.Fatalf("view = %+v", v)
	}
}

func TestAppDomainLifecycle(t *testing.T) {
	h := setupCF(t)
	v := h.connect()
	a, path := h.app("site")

	// Before anything else, the address must be on one of the user's domains.
	for host, want := range map[string]int{"blog.notmine.net": 400, "bad_host.example.com": 400, "example": 400} {
		if code := h.do("PUT", path+"/domain", map[string]string{"hostname": host}, nil); code != want {
			t.Fatalf("%s: %d, want %d", host, code, want)
		}
	}

	var got appView
	if code := h.do("PUT", path+"/domain", map[string]string{"hostname": "https://Blog.Example.com/"}, &got); code != 200 {
		t.Fatalf("set domain: %d", code)
	}
	if got.Domain != "blog.example.com" {
		t.Fatalf("domain = %q", got.Domain)
	}
	_, configs, records := h.cf.Snapshot()
	want := []cloudflare.Ingress{{Hostname: "blog.example.com", Service: "http://lps-app-site:80"}}
	if !slices.Equal(configs[v.TunnelID], want) {
		t.Fatalf("routes = %v", configs[v.TunnelID])
	}
	if len(records) != 1 {
		t.Fatalf("records = %v", records)
	}
	for _, r := range records {
		if r.Type != "CNAME" || r.Name != "blog.example.com" || r.Content != v.TunnelID+".cfargotunnel.com" || !r.Proxied || r.Comment != cloudflare.RecordComment {
			t.Fatalf("record = %+v", r)
		}
	}
	// The app joins lps-net so the connector can reach it.
	waitFor(t, func() bool {
		return slices.Contains(h.eng.snapshot().networks, docker.AppContainerName("site"))
	})

	// Another app can't take the same address.
	_, path2 := h.app("other")
	if code := h.do("PUT", path2+"/domain", map[string]string{"hostname": "blog.example.com"}, nil); code != 409 {
		t.Fatalf("taken: %d", code)
	}
	// An address with someone's own record is left alone.
	h.cf.AddRecord("z1", cloudflare.Record{Type: "A", Name: "shop.example.com", Content: "1.2.3.4"})
	var e struct{ Error string }
	if code := h.do("PUT", path2+"/domain", map[string]string{"hostname": "shop.example.com"}, &e); code != 409 || !strings.Contains(e.Error, "A → 1.2.3.4") {
		t.Fatalf("existing record: %d %q", code, e.Error)
	}

	// Moving the app to another address replaces the record and the route.
	if code := h.do("PUT", path+"/domain", map[string]string{"hostname": "example.org"}, &got); code != 200 || got.Domain != "example.org" {
		t.Fatalf("move: %d %q", code, got.Domain)
	}
	_, configs, records = h.cf.Snapshot()
	if len(configs[v.TunnelID]) != 1 || configs[v.TunnelID][0].Hostname != "example.org" {
		t.Fatalf("routes = %v", configs[v.TunnelID])
	}
	names := []string{}
	for _, r := range records {
		names = append(names, r.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"example.org", "shop.example.com"}) {
		t.Fatalf("records = %v", names)
	}

	// Removing it deletes the record and the route.
	var off appView
	if code := h.do("DELETE", path+"/domain", nil, &off); code != 200 || off.Domain != "" {
		t.Fatalf("remove: %d %+v", code, off.Domain)
	}
	_, configs, records = h.cf.Snapshot()
	if len(configs[v.TunnelID]) != 0 || len(records) != 1 {
		t.Fatalf("after remove: routes=%v records=%v", configs[v.TunnelID], records)
	}

	// Deleting an app removes its address too.
	h.do("PUT", path2+"/domain", map[string]string{"hostname": "other.example.com"}, nil)
	if code := h.do("DELETE", path2, nil, nil); code != 204 {
		t.Fatalf("delete app: %d", code)
	}
	_, configs, records = h.cf.Snapshot()
	if len(configs[v.TunnelID]) != 0 || len(records) != 1 {
		t.Fatalf("after app delete: routes=%v records=%v", configs[v.TunnelID], records)
	}
	_ = a
}

func TestAppDomainRollsBackWhenRoutesFail(t *testing.T) {
	h := setupCF(t)
	h.connect()
	_, path := h.app("site")
	h.cf.PutConfigErr = "config rejected"
	var e struct{ Error string }
	if code := h.do("PUT", path+"/domain", map[string]string{"hostname": "blog.example.com"}, &e); code != 502 || !strings.Contains(e.Error, "config rejected") {
		t.Fatalf("%d %q", code, e.Error)
	}
	_, _, records := h.cf.Snapshot()
	var got appView
	h.do("GET", path, nil, &got)
	if len(records) != 0 || got.Domain != "" {
		t.Fatalf("not rolled back: records=%v domain=%q", records, got.Domain)
	}
}

func TestCloudflareDisconnect(t *testing.T) {
	h := setupCF(t)
	v := h.connect()
	_, path := h.app("site")
	h.do("PUT", path+"/domain", map[string]string{"hostname": "blog.example.com"}, nil)
	// The user's own record pointing at the tunnel is used but never deleted.
	h.cf.AddRecord("z1", cloudflare.Record{Type: "CNAME", Name: "mine.example.com", Content: v.TunnelID + ".cfargotunnel.com"})
	_, path2 := h.app("two")
	if code := h.do("PUT", path2+"/domain", map[string]string{"hostname": "mine.example.com"}, nil); code != 200 {
		t.Fatalf("adopt: %d", code)
	}
	waitFor(t, func() bool { return h.eng.connectorToken() != "" })

	h.cf.DeleteTunnelFails = 1 // connections linger for a moment
	var out struct {
		View    cfView
		Warning string
	}
	if code := h.do("DELETE", "/api/cloudflare", nil, &out); code != 200 {
		t.Fatalf("disconnect: %d", code)
	}
	tunnels, _, records := h.cf.Snapshot()
	if out.View.Connected || out.Warning != "" || len(tunnels) != 0 || h.eng.connectorToken() != "" {
		t.Fatalf("after disconnect: view=%+v warning=%q tunnels=%v connector=%q", out.View, out.Warning, tunnels, h.eng.connectorToken())
	}
	if len(records) != 1 {
		t.Fatalf("records = %v (only the user's own should remain)", records)
	}
	var a appView
	h.do("GET", path, nil, &a)
	if a.Domain != "" {
		t.Fatalf("domain kept: %q", a.Domain)
	}
	// Setting a domain now asks to connect first.
	if code := h.do("PUT", path+"/domain", map[string]string{"hostname": "blog.example.com"}, nil); code != 409 {
		t.Fatalf("not connected: %d", code)
	}
}

func TestNormalizeHostname(t *testing.T) {
	ok := map[string]string{"Blog.Example.COM": "blog.example.com", "https://a.b.example.com/path?x": "a.b.example.com", "example.com.": "example.com", "x-1.example.io": "x-1.example.io"}
	for in, want := range ok {
		if got, err := normalizeHostname(in); err != nil || got != want {
			t.Fatalf("%q -> %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "example", "-a.example.com", "a..example.com", "*.example.com", "a_b.example.com", "example.com:8080", "ex ample.com"} {
		if _, err := normalizeHostname(in); err == nil {
			t.Fatalf("%q accepted", in)
		}
	}
}

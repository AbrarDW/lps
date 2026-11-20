package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/abrardw/lps/internal/store"
)

// upstream records what an app receives and answers with a few behaviours.
func upstream(t *testing.T) (*httptest.Server, *http.Request) {
	t.Helper()
	got := &http.Request{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = *r.Clone(context.Background())
		switch r.URL.Path {
		case "/redirect":
			http.Redirect(w, r, "/login", http.StatusFound)
		case "/absolute":
			http.Redirect(w, r, "http://"+r.Context().Value(http.LocalAddrContextKey).(interface{ String() string }).String()+"/home", http.StatusFound)
		default:
			io.WriteString(w, "app:"+r.URL.Path+"?"+r.URL.RawQuery)
		}
	}))
	t.Cleanup(ts.Close)
	return ts, got
}

func addApp(t *testing.T, h *harness, name string, port int) *store.App {
	t.Helper()
	a := &store.App{Name: name, SourcePath: "/x", ContainerPort: 80, HostPort: port}
	if err := h.srv.store.CreateApp(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	return a
}

func portOf(t *testing.T, ts *httptest.Server) int {
	u, _ := url.Parse(ts.URL)
	p, _ := strconv.Atoi(u.Port())
	return p
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func TestAppHostName(t *testing.T) {
	for host, want := range map[string]string{
		"site.localhost:7070": "site", "site.localhost": "site", "My-App.LOCALHOST.:7070": "my-app",
		"localhost:7070": "", "127.0.0.1:7070": "", "a.b.localhost": "", "x.localhost": "", "site.example.com": "",
	} {
		got, ok := appHostName(host)
		if got != want || ok != (want != "") {
			t.Errorf("%s: got %q %v, want %q", host, got, ok, want)
		}
	}
}

func TestGatewayByHost(t *testing.T) {
	h := setup(t)
	h.login()
	app, got := upstream(t)
	addApp(t, h, "site", portOf(t, app))

	req, _ := http.NewRequest("GET", h.ts.URL+"/page?q=1", nil)
	req.Host = "site.localhost:7070"
	req.Header.Set("Cookie", "lps_session=SECRET; theme=dark")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "app:/page?q=1" {
		t.Fatalf("body = %q", body)
	}
	if got.Host != "site.localhost:7070" || got.Header.Get("X-Forwarded-Prefix") != "" {
		t.Fatalf("app saw host %q prefix %q", got.Host, got.Header.Get("X-Forwarded-Prefix"))
	}
	if c := got.Header.Get("Cookie"); strings.Contains(c, "SECRET") || !strings.Contains(c, "theme=dark") {
		t.Fatalf("app saw cookies %q", c)
	}

	// Unknown app: friendly 404, never the dashboard.
	req, _ = http.NewRequest("GET", h.ts.URL+"/", nil)
	req.Host = "nope.localhost:7070"
	res, _ = http.DefaultClient.Do(req)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 404 || !strings.Contains(string(body), "no app called <b>nope</b>") {
		t.Fatalf("unknown app: %d %s", res.StatusCode, body)
	}
	// The API is not reachable through an app host.
	req, _ = http.NewRequest("GET", h.ts.URL+"/api/health", nil)
	req.Host = "site.localhost:7070"
	res, _ = http.DefaultClient.Do(req)
	body, _ = io.ReadAll(res.Body)
	res.Body.Close()
	if string(body) != "app:/api/health?" {
		t.Fatalf("api via app host: %s", body)
	}
	// The dashboard host is unaffected.
	if code := h.do("GET", "/api/health", nil, nil); code != 200 {
		t.Fatalf("dashboard: %d", code)
	}
}

func TestGatewayByPath(t *testing.T) {
	h := setup(t)
	h.login()
	app, got := upstream(t)
	addApp(t, h, "site", portOf(t, app))
	addApp(t, h, "down", 1) // nothing listens on port 1
	gw := httptest.NewServer(h.srv.GatewayHandler())
	defer gw.Close()
	c := &http.Client{CheckRedirect: noRedirect}
	get := func(path string) (*http.Response, string) {
		res, err := c.Get(gw.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res, string(b)
	}

	if res, _ := get("/site?x=1"); res.StatusCode != 301 || res.Header.Get("Location") != "/site/?x=1" {
		t.Fatalf("no slash: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	if _, body := get("/site/a/b?x=1"); body != "app:/a/b?x=1" {
		t.Fatalf("proxied body = %q", body)
	}
	if got.Header.Get("X-Forwarded-Prefix") != "/site" {
		t.Fatalf("prefix header = %q", got.Header.Get("X-Forwarded-Prefix"))
	}
	if res, _ := get("/site/redirect"); res.Header.Get("Location") != "/site/login" {
		t.Fatalf("redirect = %s", res.Header.Get("Location"))
	}
	if res, _ := get("/site/absolute"); res.Header.Get("Location") != "/site/home" {
		t.Fatalf("absolute redirect = %s", res.Header.Get("Location"))
	}
	if res, body := get("/down/"); res.StatusCode != 502 || !strings.Contains(body, "could not reach <b>down</b>") {
		t.Fatalf("down app: %d %s", res.StatusCode, body)
	}
	if res, _ := get("/nope/"); res.StatusCode != 404 {
		t.Fatalf("unknown: %d", res.StatusCode)
	}
	if _, body := get("/"); !strings.Contains(body, `<a href="/site/">site</a>`) {
		t.Fatalf("index = %s", body)
	}
}

func TestGatewayAddresses(t *testing.T) {
	h := setupWith(t, newFakeTunnel())
	h.login()
	h.srv.cfg.GatewayPort = 7071
	a := addApp(t, h, "site", 8150)
	var v appView
	h.do("GET", "/api/apps/"+strconv.FormatInt(a.ID, 10), nil, &v)
	if v.LocalURL != "http://site.localhost:7070" {
		t.Fatalf("local url = %q", v.LocalURL)
	}
	h.srv.store.SetSetting(context.Background(), store.SettingRemoteEnabled, "true")
	d, err := h.srv.desiredExposures(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if e, ok := d[appsRemotePort]; !ok || e.LocalPort != 7071 || e.Public {
		t.Fatalf("gateway exposure = %+v", d)
	}
}

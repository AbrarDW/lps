package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/store"
)

// fakeQuick records quick links in the fake engine.
type fakeQuick struct {
	links     map[string]docker.QuickLinkSpec
	ensured   []docker.QuickLinkSpec
	ensureErr error
}

func (f *fakeEngine) q() *fakeQuick {
	if f.quick == nil {
		f.quick = &fakeQuick{links: map[string]docker.QuickLinkSpec{}}
	}
	return f.quick
}

func (f *fakeEngine) EnsureQuickLink(_ context.Context, s docker.QuickLinkSpec) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	q := f.q()
	if q.ensureErr != nil {
		return q.ensureErr
	}
	q.ensured = append(q.ensured, s)
	q.links[s.App] = s
	return nil
}

func (f *fakeEngine) QuickLinkStatus(_ context.Context, app string) (docker.QuickLink, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.q().links[app]; !ok {
		return docker.QuickLink{State: docker.QuickMissing}, nil
	}
	return docker.QuickLink{State: docker.QuickActive, URL: "https://" + app + "-words.trycloudflare.com"}, nil
}

func (f *fakeEngine) RemoveQuickLink(_ context.Context, app string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.q().links, app)
	return nil
}

func (f *fakeEngine) QuickLinkApps(context.Context) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for n := range f.q().links {
		out = append(out, n)
	}
	slices.Sort(out)
	return out, nil
}

func (f *fakeEngine) quickLinks() map[string]docker.QuickLinkSpec {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]docker.QuickLinkSpec{}
	for k, v := range f.q().links {
		out[k] = v
	}
	return out
}

func TestQuickLinkOnOff(t *testing.T) {
	h := setup(t)
	h.login()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM nginx\n"), 0o644)
	var a appView
	h.do("POST", "/api/apps", createAppReq{Name: "site", SourcePath: dir, ContainerPort: 80}, &a)
	path := fmt.Sprintf("/api/apps/%d", a.ID)
	h.waitDeployed(path)

	if code := h.do("PUT", path+"/quick-link", map[string]bool{"enabled": true}, &a); code != 200 {
		t.Fatalf("enable: %d", code)
	}
	if !a.QuickLink || a.QuickState == "" {
		t.Fatalf("view after enable = %+v", a)
	}
	waitFor(t, func() bool {
		h.do("GET", path, nil, &a)
		return a.QuickState == docker.QuickActive
	})
	if a.QuickURL != "https://site-words.trycloudflare.com" {
		t.Fatalf("url = %q", a.QuickURL)
	}
	want := docker.QuickLinkSpec{App: "site", Target: docker.AppContainerName("site"), Port: 80}
	if got := h.eng.quickLinks()["site"]; got != want {
		t.Fatalf("spec = %+v, want %+v", got, want)
	}

	// A redeploy re-checks the link (the new container must join lps-net).
	h.eng.mu.Lock()
	before := len(h.eng.q().ensured)
	h.eng.mu.Unlock()
	h.do("POST", path+"/deploy", nil, nil)
	h.waitDeployed(path)
	waitFor(t, func() bool {
		h.eng.mu.Lock()
		defer h.eng.mu.Unlock()
		return len(h.eng.q().ensured) > before
	})

	// Turning it off removes the container right away.
	var off appView
	if code := h.do("PUT", path+"/quick-link", map[string]bool{"enabled": false}, &off); code != 200 {
		t.Fatalf("disable: %d", code)
	}
	if len(h.eng.quickLinks()) != 0 || off.QuickLink || off.QuickURL != "" || off.QuickState != "" {
		t.Fatalf("after disable: links=%v view=%+v", h.eng.quickLinks(), off)
	}

	var evs []store.Event
	h.do("GET", "/api/events?subject=app", nil, &evs)
	kinds := []string{}
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	if !slices.Contains(kinds, "app.quick_link_on") || !slices.Contains(kinds, "app.quick_link_off") {
		t.Fatalf("events = %v", kinds)
	}
}

func TestQuickLinkDeleteAndOrphans(t *testing.T) {
	h := setup(t)
	h.login()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM nginx\n"), 0o644)
	var a appView
	h.do("POST", "/api/apps", createAppReq{Name: "site", SourcePath: dir, ContainerPort: 80}, &a)
	path := fmt.Sprintf("/api/apps/%d", a.ID)
	h.waitDeployed(path)
	h.do("PUT", path+"/quick-link", map[string]bool{"enabled": true}, nil)
	waitFor(t, func() bool { _, ok := h.eng.quickLinks()["site"]; return ok })

	// A leftover link of an app LPS no longer has is removed.
	h.eng.EnsureQuickLink(t.Context(), docker.QuickLinkSpec{App: "gone", Target: "lps-app-gone", Port: 1})
	h.srv.reconcileQuickLinks(t.Context())
	if _, ok := h.eng.quickLinks()["gone"]; ok {
		t.Fatal("orphan link kept")
	}

	if code := h.do("DELETE", path, nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if len(h.eng.quickLinks()) != 0 {
		t.Fatalf("links after delete: %v", h.eng.quickLinks())
	}
}

func TestQuickLinkErrorsAndVPSTarget(t *testing.T) {
	h := setup(t)
	h.login()
	ctx := t.Context()

	// Not deployed yet: explained, nothing started.
	a := &store.App{Name: "later", SourcePath: t.TempDir(), ContainerPort: 3000, HostPort: 18080, QuickLink: true}
	if err := h.srv.store.CreateApp(ctx, a); err != nil {
		t.Fatal(err)
	}
	h.srv.store.SetAppQuickLink(ctx, a.ID, true)
	h.srv.reconcileQuickLinks(ctx)
	var v appView
	h.do("GET", fmt.Sprintf("/api/apps/%d", a.ID), nil, &v)
	if v.QuickState != docker.QuickStarting || !strings.Contains(v.QuickError, "first successful deploy") {
		t.Fatalf("view = %+v", v)
	}

	// Setup errors are shown.
	h.srv.store.SetAppContainer(ctx, a.ID, "c-later")
	h.eng.mu.Lock()
	h.eng.q().ensureErr = errors.New("download cloudflare/cloudflared: no internet")
	h.eng.mu.Unlock()
	h.srv.reconcileQuickLinks(ctx)
	h.do("GET", fmt.Sprintf("/api/apps/%d", a.ID), nil, &v)
	if v.QuickState != docker.QuickError || !strings.Contains(v.QuickError, "no internet") {
		t.Fatalf("view = %+v", v)
	}

	// Apps in a VPS go to the VPS container.
	vps := &store.VPS{Name: "box", SSHUser: "dev", SSHPort: 2222}
	if err := h.srv.store.CreateVPS(ctx, vps); err != nil {
		t.Fatal(err)
	}
	in := &store.App{Name: "api", VPSID: vps.ID, ContainerPort: 5000, ContainerID: "relay"}
	spec, err := h.srv.quickSpec(ctx, in)
	if err != nil || spec != (docker.QuickLinkSpec{App: "api", Target: docker.VPSContainerName("box"), Port: 5000}) {
		t.Fatalf("spec = %+v, %v", spec, err)
	}
}

package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/store"
)

// Usage fakes a container using a quarter of a core and 100 MB of 1 GB.
func (f *fakeEngine) Usage(_ context.Context, id string) (docker.Usage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.containers[id] != docker.StateRunning {
		return docker.Usage{}, fmt.Errorf("not running")
	}
	if f.cpuNanos == nil {
		f.cpuNanos = map[string]uint64{}
		f.cpuAt = map[string]time.Time{}
	}
	now := time.Now()
	if last, ok := f.cpuAt[id]; ok {
		f.cpuNanos[id] += uint64(now.Sub(last).Nanoseconds()) / 4
	}
	f.cpuAt[id] = now
	return docker.Usage{At: now, CPUNanos: f.cpuNanos[id], MemBytes: 100 << 20, MemLimit: 1 << 30}, nil
}

func (f *fakeEngine) HostInfo(context.Context) (docker.HostInfo, error) {
	return docker.HostInfo{CPUs: 4, MemBytes: 8 << 30}, nil
}

func TestActivityFeed(t *testing.T) {
	h := setup(t)
	h.login()
	v := h.newRunningVPS()
	h.do("POST", fmt.Sprintf("/api/vps/%d/stop", v.ID), nil, nil)

	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM nginx\n"), 0o644)
	var a appView
	h.do("POST", "/api/apps", createAppReq{Name: "site", SourcePath: dir, ContainerPort: 80}, &a)
	h.waitDeployed(fmt.Sprintf("/api/apps/%d", a.ID))
	h.eng.mu.Lock()
	h.eng.buildErr = fmt.Errorf("npm ci exited 1\nmore detail")
	h.eng.mu.Unlock()
	h.do("POST", fmt.Sprintf("/api/apps/%d/deploy", a.ID), nil, nil)
	waitFor(t, func() bool {
		var evs []store.Event
		h.do("GET", "/api/events?subject=app", nil, &evs)
		return len(evs) > 0 && evs[0].Kind == "app.deploy_failed"
	})

	var evs []store.Event
	h.do("GET", "/api/events", nil, &evs)
	kinds := []string{}
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	got := strings.Join(kinds, ",")
	for _, want := range []string{"auth.setup", "vps.created", "vps.ready", "vps.stopped", "app.created", "app.deployed", "app.redeploy", "app.deploy_failed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in %s", want, got)
		}
	}
	if evs[0].Kind != "app.deploy_failed" || evs[0].Level != store.LevelError || evs[0].Message != "Deploy #2 of site failed: build: npm ci exited 1" {
		t.Fatalf("newest = %+v", evs[0])
	}
	var redeploy store.Event
	for _, e := range evs {
		if e.Kind == "app.redeploy" {
			redeploy = e
		}
	}
	if redeploy.Actor != "admin" || redeploy.IP != "127.0.0.1" || redeploy.SubjectID != a.ID {
		t.Fatalf("redeploy event = %+v", redeploy)
	}

	// Filters and paging.
	var vpsOnly []store.Event
	h.do("GET", fmt.Sprintf("/api/events?subject=vps&id=%d", v.ID), nil, &vpsOnly)
	for _, e := range vpsOnly {
		if e.Subject != "vps" || e.SubjectID != v.ID {
			t.Fatalf("filter leaked %+v", e)
		}
	}
	var page []store.Event
	h.do("GET", fmt.Sprintf("/api/events?limit=2&before=%d", evs[0].ID), nil, &page)
	if len(page) != 2 || page[0].ID != evs[1].ID {
		t.Fatalf("paging = %+v", page)
	}
	if code := h.do("GET", "/api/events?subject=nope", nil, nil); code != 400 {
		t.Fatalf("bad subject: %d", code)
	}

	// Failed sign-in.
	code := h.do("POST", "/api/auth/login", credentials{"admin", "wrong-password"}, nil)
	if code != 401 {
		t.Fatalf("bad login: %d", code)
	}
	h.do("GET", "/api/events?subject=auth&limit=1", nil, &evs)
	if evs[0].Kind != "auth.login_failed" || evs[0].Level != store.LevelWarning {
		t.Fatalf("auth event = %+v", evs[0])
	}
}

func TestLiveStats(t *testing.T) {
	old := statsFast
	statsFast = 50 * time.Millisecond
	defer func() { statsFast = old }()
	h := setup(t)
	h.login()
	v := h.newRunningVPS()
	var a appView
	h.do("POST", "/api/apps", createAppReq{Name: "api", VPSID: v.ID, SourceKind: "vps", VPSPath: "~/api", ContainerPort: 3000, StartCmd: "npm start"}, &a)
	h.waitDeployed(fmt.Sprintf("/api/apps/%d", a.ID))

	type statsResp struct {
		Host struct{ CPUs int } `json:"host"`
		VPS  map[string]statView
		Apps map[string]statView
	}
	var got statsResp
	vk, ak := fmt.Sprint(v.ID), fmt.Sprint(a.ID)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.do("GET", "/api/stats", nil, &got)
		if len(got.VPS[vk].Points) >= 3 && len(got.Apps[ak].Points) >= 2 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	vs := got.VPS[vk]
	if len(vs.Points) < 3 || vs.CPU < 15 || vs.CPU > 35 || vs.Mem != 100<<20 || vs.MemLimit != 1<<30 || vs.CPULimit != 4 {
		t.Fatalf("vps stats = %+v (host %+v)", vs, got.Host)
	}
	as := got.Apps[ak]
	if len(as.Points) < 2 || as.Mem != 2560*4096 || as.CPU <= 0 || as.MemLimit != 8<<30 {
		t.Fatalf("app stats = %+v", as)
	}

	// A stopped VPS (and its app) drop out.
	h.do("POST", fmt.Sprintf("/api/vps/%d/stop", v.ID), nil, nil)
	waitFor(t, func() bool {
		var s statsResp
		h.do("GET", "/api/stats", nil, &s)
		_, okV := s.VPS[vk]
		_, okA := s.Apps[ak]
		return !okV && !okA
	})
}

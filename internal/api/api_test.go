package api

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/store"
	"github.com/abrardw/lps/internal/tunnel"
)

// fakeEngine is an in-memory docker.Engine.
type fakeEngine struct {
	mu            sync.Mutex
	n             int
	containers    map[string]string // id -> state
	volumes       map[string]bool
	vpsSpecs      []docker.VPSSpec
	builds        []string
	buildFiles    []string // dockerfile name used for each build
	buildCtx      [][]string
	buildErr      error
	pingErr       error
	lastTerm      *fakeTerm
	fv            *fakeVPS
	removedImages []string
	buildErrAt    map[int]bool // builds that failed (no image)
	cpuNanos      map[string]uint64
	cpuAt         map[string]time.Time
}

func newFake() *fakeEngine {
	return &fakeEngine{containers: map[string]string{}, volumes: map[string]bool{}}
}

func (f *fakeEngine) Ping(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.pingErr != nil {
		return "", f.pingErr
	}
	return "fake", nil
}
func (f *fakeEngine) EnsureVPSImage(context.Context, io.Writer) error   { return nil }
func (f *fakeEngine) newID() string                                     { f.n++; return fmt.Sprintf("c%d", f.n) }
func (f *fakeEngine) Logs(context.Context, string, int) (string, error) { return "hello\n", nil }
func (f *fakeEngine) RemoveImage(_ context.Context, ref string) error {
	f.mu.Lock()
	f.removedImages = append(f.removedImages, ref)
	f.mu.Unlock()
	return nil
}

// ImageTags reports images built and not yet removed.
func (f *fakeEngine) ImageTags(_ context.Context, repo string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	gone := map[string]bool{}
	for _, r := range f.removedImages {
		gone[r] = true
	}
	var out []string
	for i, b := range f.builds {
		if strings.HasPrefix(b, repo+":") && !gone[b] && (f.buildErrAt == nil || !f.buildErrAt[i]) {
			out = append(out, b)
		}
	}
	return out, nil
}
func (f *fakeEngine) RemoveVolume(_ context.Context, name string) error {
	f.mu.Lock()
	delete(f.volumes, name)
	f.mu.Unlock()
	return nil
}
func (f *fakeEngine) CreateApp(_ context.Context, s docker.AppSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.newID()
	f.containers[id] = docker.StateStopped
	return id, nil
}
func (f *fakeEngine) CreateVPS(_ context.Context, s docker.VPSSpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vpsSpecs = append(f.vpsSpecs, s)
	f.volumes[docker.VPSVolumeName(s.Name)] = true
	id := f.newID()
	f.containers[id] = docker.StateStopped
	return id, nil
}
func (f *fakeEngine) BuildImage(_ context.Context, r io.Reader, tag, dockerfile string, log io.Writer) error {
	var names []string
	tr := tar.NewReader(r)
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		names = append(names, h.Name)
	}
	io.Copy(io.Discard, r)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.buildErr != nil {
		if f.buildErrAt == nil {
			f.buildErrAt = map[int]bool{}
		}
		f.buildErrAt[len(f.builds)] = true
	}
	f.builds = append(f.builds, tag)
	f.buildFiles = append(f.buildFiles, dockerfile)
	f.buildCtx = append(f.buildCtx, names)
	fmt.Fprintln(log, "Step 1/1")
	return f.buildErr
}
func (f *fakeEngine) set(id, st string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.containers[id]; !ok {
		return fmt.Errorf("no such container %s", id)
	}
	f.containers[id] = st
	return nil
}

// fakeTerm echoes input back as "echo:<input>" and records resizes.
type fakeTerm struct {
	in      chan []byte
	mu      sync.Mutex
	resizes [][2]uint
	user    string
	closed  chan struct{}
	once    sync.Once
}

func (t *fakeTerm) Read(p []byte) (int, error) {
	select {
	case b := <-t.in:
		return copy(p, append([]byte("echo:"), b...)), nil
	case <-t.closed:
		return 0, io.EOF
	}
}
func (t *fakeTerm) Write(p []byte) (int, error) {
	if string(p) == "exit" {
		t.Close()
		return len(p), nil
	}
	select {
	case t.in <- append([]byte(nil), p...):
	case <-t.closed:
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}
func (t *fakeTerm) Close() error { t.once.Do(func() { close(t.closed) }); return nil }
func (t *fakeTerm) Resize(_ context.Context, c, r uint) error {
	t.mu.Lock()
	t.resizes = append(t.resizes, [2]uint{c, r})
	t.mu.Unlock()
	return nil
}

func (f *fakeEngine) OpenTerminal(_ context.Context, id, user string, cols, rows uint) (docker.Terminal, error) {
	t := &fakeTerm{in: make(chan []byte, 8), closed: make(chan struct{}), user: user, resizes: [][2]uint{{cols, rows}}}
	f.mu.Lock()
	f.lastTerm = t
	f.mu.Unlock()
	return t, nil
}

func (f *fakeEngine) Start(_ context.Context, id string) error { return f.set(id, docker.StateRunning) }
func (f *fakeEngine) Stop(_ context.Context, id string) error  { return f.set(id, docker.StateStopped) }
func (f *fakeEngine) Remove(_ context.Context, id string) error {
	f.mu.Lock()
	delete(f.containers, id)
	f.mu.Unlock()
	return nil
}
func (f *fakeEngine) State(_ context.Context, id string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if st, ok := f.containers[id]; ok {
		return st, nil
	}
	return docker.StateMissing, nil
}

type harness struct {
	t   *testing.T
	ts  *httptest.Server
	c   *http.Client
	eng *fakeEngine
	srv *Server
}

func setup(t *testing.T) *harness { return setupWith(t, nil) }

func setupWith(t *testing.T, tp tunnel.Provider) *harness {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	eng := newFake()
	cfg := DefaultConfig()
	ui := fstest.MapFS{"index.html": {Data: []byte("<html>lps</html>")}}
	cfg.DashboardPort = 7070
	srv := New(cfg, st, eng, tp, ui, nil)
	ts := httptest.NewServer(srv.Handler())
	jar, _ := cookiejar.New(nil)
	t.Cleanup(func() { ts.Close(); srv.Shutdown(); st.Close() })
	return &harness{t: t, ts: ts, c: &http.Client{Jar: jar}, eng: eng, srv: srv}
}

func (h *harness) do(method, path string, body any, out any) int {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.ts.URL+path, rd)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-LPS-Request", "1")
	res, err := h.c.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer res.Body.Close()
	if out != nil {
		json.NewDecoder(res.Body).Decode(out)
	}
	return res.StatusCode
}

func (h *harness) login() {
	h.t.Helper()
	if code := h.do("POST", "/api/setup", credentials{"admin", "supersecret1"}, nil); code != 201 {
		h.t.Fatalf("setup: %d", code)
	}
}

// waitFor polls cond until true or timeout (background jobs run asynchronously).
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

func TestSetupAndAuth(t *testing.T) {
	h := setup(t)
	var st map[string]bool
	h.do("GET", "/api/setup", nil, &st)
	if !st["needsSetup"] {
		t.Fatal("expected needsSetup")
	}
	if code := h.do("GET", "/api/vps", nil, nil); code != 401 {
		t.Fatalf("unauthenticated: %d", code)
	}
	if code := h.do("POST", "/api/setup", credentials{"admin", "short"}, nil); code != 400 {
		t.Fatalf("weak password: %d", code)
	}
	h.login()
	if code := h.do("POST", "/api/setup", credentials{"evil", "supersecret2"}, nil); code != 409 {
		t.Fatalf("second setup: %d", code)
	}
	if code := h.do("GET", "/api/auth/me", nil, nil); code != 200 {
		t.Fatalf("me: %d", code)
	}
	h.do("POST", "/api/auth/logout", nil, nil)
	if code := h.do("GET", "/api/auth/me", nil, nil); code != 401 {
		t.Fatalf("after logout: %d", code)
	}
	if code := h.do("POST", "/api/auth/login", credentials{"admin", "wrongpassword"}, nil); code != 401 {
		t.Fatalf("bad login: %d", code)
	}
	if code := h.do("POST", "/api/auth/login", credentials{"admin", "supersecret1"}, nil); code != 200 {
		t.Fatalf("login: %d", code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	h := setup(t)
	h.login()
	for i := 0; i < 5; i++ {
		h.do("POST", "/api/auth/login", credentials{"admin", "wrongpassword"}, nil)
	}
	if code := h.do("POST", "/api/auth/login", credentials{"admin", "supersecret1"}, nil); code != 429 {
		t.Fatalf("expected 429, got %d", code)
	}
}

func TestCSRFHeaderRequired(t *testing.T) {
	h := setup(t)
	res, err := h.c.Post(h.ts.URL+"/api/setup", "application/json", bytes.NewReader([]byte(`{"username":"admin","password":"supersecret1"}`)))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("missing header: %d", res.StatusCode)
	}
}

func TestVPSLifecycle(t *testing.T) {
	h := setup(t)
	h.login()

	bad := []createVPSReq{
		{Name: "Bad Name", SSHUser: "dev", Password: "password1"},
		{Name: "box", SSHUser: "root", Password: "password1"},
		{Name: "box", SSHUser: "dev"},
		{Name: "box", SSHUser: "dev", SSHPublicKey: "not-a-key"},
		{Name: "box", SSHUser: "dev", Password: "password1", MemoryMB: 10},
	}
	for _, b := range bad {
		if code := h.do("POST", "/api/vps", b, nil); code != 400 {
			t.Fatalf("expected 400 for %+v, got %d", b, code)
		}
	}

	var v vpsView
	req := createVPSReq{Name: "box", SSHUser: "dev", SSHPublicKey: "ssh-ed25519 AAAAC3Nza test@pc", Password: "password1", CPUs: 1, MemoryMB: 512}
	if code := h.do("POST", "/api/vps", req, &v); code != 202 {
		t.Fatalf("create: %d", code)
	}
	if v.SSHPort != 2201 {
		t.Fatalf("ssh port = %d", v.SSHPort)
	}
	waitFor(t, func() bool {
		var got vpsView
		h.do("GET", fmt.Sprintf("/api/vps/%d", v.ID), nil, &got)
		return got.State == docker.StateRunning
	})
	if h.eng.vpsSpecs[0].Password != "password1" {
		t.Fatal("password not passed to engine")
	}
	if code := h.do("POST", "/api/vps", createVPSReq{Name: "box", SSHUser: "dev", Password: "password1"}, nil); code != 409 {
		t.Fatalf("duplicate: %d", code)
	}

	var stopped vpsView
	h.do("POST", fmt.Sprintf("/api/vps/%d/stop", v.ID), nil, &stopped)
	if stopped.State != docker.StateStopped {
		t.Fatalf("after stop: %s", stopped.State)
	}
	var logs map[string]string
	h.do("GET", fmt.Sprintf("/api/vps/%d/logs", v.ID), nil, &logs)
	if logs["logs"] != "hello\n" {
		t.Fatalf("logs = %q", logs["logs"])
	}
	if code := h.do("DELETE", fmt.Sprintf("/api/vps/%d", v.ID), nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if len(h.eng.volumes) != 0 {
		t.Fatal("volume not removed")
	}
	var list []vpsView
	h.do("GET", "/api/vps", nil, &list)
	if len(list) != 0 {
		t.Fatalf("list after delete: %d", len(list))
	}
}

func TestAppDeploy(t *testing.T) {
	h := setup(t)
	h.login()

	dir := t.TempDir()
	if code := h.do("POST", "/api/apps", createAppReq{Name: "site", SourcePath: dir, ContainerPort: 80}, nil); code != 400 {
		t.Fatalf("no Dockerfile should be 400, got %d", code)
	}
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM nginx:alpine\n"), 0o644)

	var a appView
	if code := h.do("POST", "/api/apps", createAppReq{Name: "site", SourcePath: dir, ContainerPort: 80}, &a); code != 202 {
		t.Fatalf("create app: %d", code)
	}
	if a.HostPort != 8100 {
		t.Fatalf("host port = %d", a.HostPort)
	}
	path := fmt.Sprintf("/api/apps/%d", a.ID)
	waitFor(t, func() bool {
		var got appView
		h.do("GET", path, nil, &got)
		return !got.Deploying && got.State == docker.StateRunning
	})
	firstContainer := func() string { var g appView; h.do("GET", path, nil, &g); return g.ContainerID }()

	// Redeploy replaces the container.
	if code := h.do("POST", path+"/deploy", nil, nil); code != 202 {
		t.Fatalf("redeploy: %d", code)
	}
	waitFor(t, func() bool {
		var got appView
		h.do("GET", path, nil, &got)
		return !got.Deploying && got.ContainerID != firstContainer && got.State == docker.StateRunning
	})
	var ds []store.Deployment
	h.do("GET", path+"/deployments", nil, &ds)
	if len(ds) != 2 || ds[0].Status != store.DeployRunning || ds[0].Log == "" || ds[1].Status != store.DeployReplaced {
		t.Fatalf("deployments = %+v", ds)
	}
	if _, ok := h.eng.containers[firstContainer]; ok {
		t.Fatal("old container not removed")
	}

	// A failing build is recorded and leaves the running container alone.
	h.eng.buildErr = fmt.Errorf("boom")
	h.do("POST", path+"/deploy", nil, nil)
	waitFor(t, func() bool {
		var got []store.Deployment
		h.do("GET", path+"/deployments", nil, &got)
		return len(got) == 3 && got[0].Status == store.DeployFailed
	})
	var after appView
	h.do("GET", path, nil, &after)
	if after.State != docker.StateRunning {
		t.Fatalf("failed build should not stop app, state=%s", after.State)
	}

	if code := h.do("DELETE", path, nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
}

func TestSPAFallback(t *testing.T) {
	h := setup(t)
	res, err := http.Get(h.ts.URL + "/vps/123")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || string(b) != "<html>lps</html>" {
		t.Fatalf("spa fallback: %d %q", res.StatusCode, b)
	}
	if res.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatal("security headers missing")
	}
}

// Regression: every redeploy used to leave the earlier deployments "running"
// and their images on disk.
func TestRedeployReplacesOldDeployments(t *testing.T) {
	h := setup(t)
	h.login()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM node:22-alpine\n"), 0o644)
	var a appView
	if code := h.do("POST", "/api/apps", createAppReq{Name: "node", SourcePath: dir, ContainerPort: 3000}, &a); code != 202 {
		t.Fatalf("create: %d", code)
	}
	path := fmt.Sprintf("/api/apps/%d", a.ID)
	idle := func() {
		waitFor(t, func() bool {
			var got appView
			h.do("GET", path, nil, &got)
			return !got.Deploying && got.LastDeploy != nil && got.LastDeploy.Status != store.DeployBuilding
		})
	}
	idle()
	for i := 0; i < 3; i++ {
		if code := h.do("POST", path+"/deploy", nil, nil); code != 202 {
			t.Fatalf("redeploy %d: %d", i, code)
		}
		idle()
	}
	var ds []store.Deployment
	h.do("GET", path+"/deployments", nil, &ds)
	running := 0
	for _, d := range ds {
		if d.Status == store.DeployRunning {
			running++
		}
	}
	if len(ds) != 4 || running != 1 || ds[0].Status != store.DeployRunning {
		t.Fatalf("want exactly the newest running, got %+v", ds)
	}
	for _, d := range ds[1:] {
		if d.Status != store.DeployReplaced {
			t.Fatalf("deployment #%d = %s, want replaced", d.ID, d.Status)
		}
	}
	// Images of #1 and #2 are removed; #3 (previous) and #4 (live) are kept.
	h.eng.mu.Lock()
	removed := strings.Join(h.eng.removedImages, ",")
	h.eng.mu.Unlock()
	if removed != "lps-app-node:1,lps-app-node:2" {
		t.Fatalf("removed images = %q", removed)
	}
	if code := h.do("DELETE", path, nil, nil); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	h.eng.mu.Lock()
	removed = strings.Join(h.eng.removedImages, ",")
	h.eng.mu.Unlock()
	if !strings.HasSuffix(removed, "lps-app-node:4,lps-app-node:3") {
		t.Fatalf("delete should remove the remaining images, removed = %q", removed)
	}
}

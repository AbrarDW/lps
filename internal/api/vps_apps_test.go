package api

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/store"
)

// fakeVPS simulates what runs inside VPS containers: lps-app processes,
// copied files and executed commands.
type fakeVPS struct {
	procs    map[string]int // app name -> port, while running
	execs    []string       // "user: cmd args..."
	copies   map[string][]string
	relays   []docker.RelaySpec
	networks []string
	execFail map[string]int // command prefix -> exit code
	loopback bool           // apps listen on 127.0.0.1 only
}

func (f *fakeEngine) vps() *fakeVPS {
	if f.fv == nil {
		f.fv = &fakeVPS{procs: map[string]int{}, copies: map[string][]string{}, execFail: map[string]int{}}
	}
	return f.fv
}

func envValue(env []string, k string) string {
	for _, e := range env {
		if strings.HasPrefix(e, k+"=") {
			return e[len(k)+1:]
		}
	}
	return ""
}

func (f *fakeEngine) Exec(_ context.Context, id, user, dir string, cmd, env []string, out io.Writer) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.containers[id] != docker.StateRunning {
		return -1, fmt.Errorf("container %s is not running", id)
	}
	v := f.vps()
	line := strings.Join(cmd, " ")
	v.execs = append(v.execs, user+": "+line)
	if out == nil {
		out = io.Discard
	}
	for prefix, code := range v.execFail {
		if strings.HasPrefix(line, prefix) {
			return code, nil
		}
	}
	if cmd[0] == docker.AppHelperPath {
		name := cmd[2]
		switch cmd[1] {
		case "start":
			if envValue(env, "LPS_START") == "" {
				return 2, nil
			}
			var port int
			fmt.Sscan(envValue(env, "LPS_PORT"), &port)
			v.procs[name] = port
			fmt.Fprintln(out, "started")
		case "stop":
			delete(v.procs, name)
			fmt.Fprintln(out, "stopped")
		case "status":
			if _, ok := v.procs[name]; ok {
				fmt.Fprintln(out, "running")
			} else {
				fmt.Fprintln(out, "stopped")
			}
		case "logs":
			fmt.Fprintln(out, "app says hi")
		}
		return 0, nil
	}
	if cmd[0] == "ss" {
		host := "0.0.0.0"
		if v.loopback {
			host = "127.0.0.1"
		}
		for _, p := range v.procs {
			fmt.Fprintf(out, "LISTEN 0 4096 %s:%d 0.0.0.0:*\n", host, p)
		}
	}
	return 0, nil
}

func (f *fakeEngine) CopyTo(_ context.Context, id, dir string, r io.Reader) error {
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
	f.vps().copies[dir] = names
	f.mu.Unlock()
	return nil
}

func (f *fakeEngine) InstallAppHelper(context.Context, string) error { return nil }

func (f *fakeEngine) EnsureNetwork(_ context.Context, id string) error {
	f.mu.Lock()
	f.vps().networks = append(f.vps().networks, id)
	f.mu.Unlock()
	return nil
}

func (f *fakeEngine) CreateRelay(_ context.Context, s docker.RelaySpec) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.vps().relays = append(f.vps().relays, s)
	id := docker.RelayContainerName(s.App)
	f.containers[id] = docker.StateStopped
	return id, nil
}

func (f *fakeEngine) snapshot() fakeVPS {
	f.mu.Lock()
	defer f.mu.Unlock()
	v := *f.vps()
	v.execs = append([]string(nil), v.execs...)
	procs := map[string]int{}
	for k, p := range v.procs {
		procs[k] = p
	}
	v.procs = procs
	return v
}

// newRunningVPS creates a VPS named "box" (user dev) and waits for it.
func (h *harness) newRunningVPS() vpsView {
	h.t.Helper()
	var v vpsView
	if code := h.do("POST", "/api/vps", createVPSReq{Name: "box", SSHUser: "dev", Password: "password1"}, &v); code != 202 {
		h.t.Fatalf("create vps: %d", code)
	}
	waitFor(h.t, func() bool {
		var got vpsView
		h.do("GET", fmt.Sprintf("/api/vps/%d", v.ID), nil, &got)
		v = got
		return got.State == docker.StateRunning
	})
	return v
}

func (h *harness) waitDeployed(path string) appView {
	h.t.Helper()
	var got appView
	waitFor(h.t, func() bool {
		h.do("GET", path, nil, &got)
		return !got.Deploying && got.LastDeploy != nil && got.LastDeploy.Status != store.DeployBuilding
	})
	return got
}

func TestVPSAppValidation(t *testing.T) {
	h := setup(t)
	h.login()
	v := h.newRunningVPS()
	dir := t.TempDir()
	bad := []createAppReq{
		{Name: "site", VPSID: v.ID, SourcePath: dir, ContainerPort: 8080},                                    // no start command
		{Name: "site", VPSID: v.ID, SourcePath: "relative", ContainerPort: 8080, StartCmd: "x"},              // relative PC path
		{Name: "site", VPSID: v.ID, SourceKind: "vps", VPSPath: "site", ContainerPort: 8080, StartCmd: "x"},  // relative VPS path
		{Name: "site", VPSID: v.ID, SourceKind: "vps", VPSPath: "~/", ContainerPort: 8080, StartCmd: "x"},    // home itself
		{Name: "site", VPSID: v.ID, SourceKind: "ftp", ContainerPort: 8080, StartCmd: "x"},                   // bad kind
		{Name: "site", VPSID: v.ID, SourcePath: dir, ContainerPort: 22, StartCmd: "x"},                       // ssh port
		{Name: "site", VPSID: v.ID, SourcePath: dir, ContainerPort: 8080, StartCmd: "x", Packages: "rm -rf"}, // bad package
		{Name: "site", VPSID: 999, SourcePath: dir, ContainerPort: 8080, StartCmd: "x"},                      // no such VPS
	}
	for _, b := range bad {
		if code := h.do("POST", "/api/apps", b, nil); code != 400 {
			t.Fatalf("expected 400 for %+v, got %d", b, code)
		}
	}
	// No Dockerfile is needed for a VPS app.
	if code := h.do("POST", "/api/apps", createAppReq{Name: "site", VPSID: v.ID, SourcePath: dir, ContainerPort: 8080, StartCmd: "x"}, nil); code != 202 {
		t.Fatalf("valid VPS app: %d", code)
	}
}

func TestVPSAppDeployFromPC(t *testing.T) {
	h := setup(t)
	h.login()
	v := h.newRunningVPS()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("hi"), 0o644)
	os.MkdirAll(filepath.Join(dir, "node_modules", "x"), 0o755)

	var a appView
	req := createAppReq{Name: "site", VPSID: v.ID, SourcePath: dir, ContainerPort: 8080,
		Packages: "python3  nodejs", InstallCmd: "echo install", StartCmd: "python3 -m http.server $PORT"}
	if code := h.do("POST", "/api/apps", req, &a); code != 202 {
		t.Fatalf("create: %d", code)
	}
	path := fmt.Sprintf("/api/apps/%d", a.ID)
	got := h.waitDeployed(path)
	if got.LastDeploy.Status != store.DeployRunning || got.State != docker.StateRunning {
		var ds []store.Deployment
		h.do("GET", path+"/deployments", nil, &ds)
		t.Fatalf("deploy: status=%s state=%s log:\n%s", got.LastDeploy.Status, got.State, ds[0].Log)
	}
	if got.VPSName != "box" || got.DockerfileSource != "none" || got.Packages != "python3 nodejs" {
		t.Fatalf("view = %+v", got)
	}
	fv := h.eng.snapshot()
	files := fv.copies["/home/dev/apps/site.new"]
	if strings.Join(files, ",") != "index.html" {
		t.Fatalf("copied %v (node_modules must be skipped)", files)
	}
	if len(fv.relays) != 1 || fv.relays[0] != (docker.RelaySpec{App: "site", VPS: "box", Port: 8080, HostPort: got.HostPort}) {
		t.Fatalf("relays = %+v", fv.relays)
	}
	if h.eng.containers[docker.RelayContainerName("site")] != docker.StateRunning {
		t.Fatal("relay not running")
	}
	// Order: packages (root) → copy → stop → swap → install (user) → start.
	order := []string{"root: sh -c missing", "dev: " + docker.AppHelperPath + " stop site", "root: sh -c rm -rf \"$1\" && mv",
		"dev: bash -lc echo install", "dev: " + docker.AppHelperPath + " start site"}
	i := 0
	for _, e := range fv.execs {
		if i < len(order) && strings.HasPrefix(e, order[i]) {
			i++
		}
	}
	if i != len(order) {
		t.Fatalf("exec order wrong (matched %d):\n%s", i, strings.Join(fv.execs, "\n"))
	}

	var logs map[string]string
	h.do("GET", path+"/logs", nil, &logs)
	if logs["logs"] != "app says hi\n" {
		t.Fatalf("logs = %q", logs)
	}

	// Stop keeps it stopped; reconcile does not restart it.
	var stopped appView
	h.do("POST", path+"/stop", nil, &stopped)
	if stopped.State != docker.StateStopped || stopped.WantRunning {
		t.Fatalf("after stop: %+v", stopped)
	}
	h.srv.reconcileVPSApps(context.Background())
	if _, ok := h.eng.snapshot().procs["site"]; ok {
		t.Fatal("reconcile restarted a stopped app")
	}
	h.do("POST", path+"/start", nil, nil)
	if _, ok := h.eng.snapshot().procs["site"]; !ok {
		t.Fatal("start did not start the app")
	}

	// The VPS cannot be deleted while it runs apps.
	if code := h.do("DELETE", fmt.Sprintf("/api/vps/%d", v.ID), nil, nil); code != 409 {
		t.Fatalf("delete VPS with apps: %d", code)
	}
	var vv vpsView
	h.do("GET", fmt.Sprintf("/api/vps/%d", v.ID), nil, &vv)
	if len(vv.Apps) != 1 || vv.Apps[0].Name != "site" {
		t.Fatalf("vps apps = %+v", vv.Apps)
	}
	if code := h.do("DELETE", path, nil, nil); code != 204 {
		t.Fatalf("delete app: %d", code)
	}
	if _, ok := h.eng.containers[docker.RelayContainerName("site")]; ok {
		t.Fatal("relay not removed")
	}
	if code := h.do("DELETE", fmt.Sprintf("/api/vps/%d", v.ID), nil, nil); code != 204 {
		t.Fatalf("delete VPS: %d", code)
	}
}

func TestVPSAppRestartsAfterVPSRestart(t *testing.T) {
	h := setup(t)
	h.login()
	v := h.newRunningVPS()
	var a appView
	req := createAppReq{Name: "api", VPSID: v.ID, SourceKind: "vps", VPSPath: "~/code/api/", ContainerPort: 3000, StartCmd: "npm start"}
	if code := h.do("POST", "/api/apps", req, &a); code != 202 {
		t.Fatalf("create: %d", code)
	}
	path := fmt.Sprintf("/api/apps/%d", a.ID)
	if got := h.waitDeployed(path); got.State != docker.StateRunning || got.VPSPath != "~/code/api" {
		t.Fatalf("deploy: %+v", got)
	}
	fv := h.eng.snapshot()
	if len(fv.copies) != 0 {
		t.Fatalf("code already in the VPS must not be copied: %v", fv.copies)
	}
	if !strings.Contains(strings.Join(fv.execs, "\n"), "dev: test -d /home/dev/code/api") {
		t.Fatalf("folder not checked:\n%s", strings.Join(fv.execs, "\n"))
	}

	// Simulate a VPS restart: its processes are gone.
	vpsPath := fmt.Sprintf("/api/vps/%d", v.ID)
	h.do("POST", vpsPath+"/stop", nil, nil)
	h.eng.mu.Lock()
	delete(h.eng.vps().procs, "api")
	h.eng.mu.Unlock()
	var st appView
	h.do("GET", path, nil, &st)
	if st.State != docker.StateStopped {
		t.Fatalf("app on stopped VPS: %s", st.State)
	}
	h.do("POST", vpsPath+"/start", nil, nil)
	waitFor(t, func() bool { _, ok := h.eng.snapshot().procs["api"]; return ok })
}

func TestVPSAppDeployFailures(t *testing.T) {
	h := setup(t)
	h.login()
	v := h.newRunningVPS()

	// A missing folder in the VPS fails the deploy with a clear message.
	h.eng.mu.Lock()
	h.eng.vps().execFail["test -d"] = 1
	h.eng.mu.Unlock()
	var a appView
	h.do("POST", "/api/apps", createAppReq{Name: "gone", VPSID: v.ID, SourceKind: "vps", VPSPath: "/srv/gone", ContainerPort: 3000, StartCmd: "x"}, &a)
	path := fmt.Sprintf("/api/apps/%d", a.ID)
	got := h.waitDeployed(path)
	var ds []store.Deployment
	h.do("GET", path+"/deployments", nil, &ds)
	if got.LastDeploy.Status != store.DeployFailed || !strings.Contains(ds[0].Log, "/srv/gone does not exist") {
		t.Fatalf("expected failure, got %s:\n%s", got.LastDeploy.Status, ds[0].Log)
	}

	// A failing install command fails the deploy.
	h.eng.mu.Lock()
	h.eng.vps().execFail = map[string]int{"bash -lc npm ci": 1}
	h.eng.vps().loopback = true
	h.eng.mu.Unlock()
	var fixed appView
	h.do("PUT", path+"/run-config", map[string]string{"installCmd": "npm ci", "startCmd": "npm start"}, &fixed)
	h.do("POST", path+"/deploy", nil, nil) // may 409 while the run-config deploy runs
	got = h.waitDeployed(path)
	h.do("GET", path+"/deployments", nil, &ds)
	if got.LastDeploy.Status != store.DeployFailed || !strings.Contains(ds[0].Log, "install command failed") {
		t.Fatalf("expected install failure:\n%s", ds[0].Log)
	}

	// An app listening on 127.0.0.1 only deploys, with a warning.
	h.eng.mu.Lock()
	h.eng.vps().execFail = map[string]int{}
	h.eng.mu.Unlock()
	if code := h.do("PUT", path+"/run-config", map[string]string{"startCmd": "npm start"}, nil); code != 200 {
		t.Fatalf("run-config: %d", code)
	}
	got = h.waitDeployed(path)
	h.do("GET", path+"/deployments", nil, &ds)
	if got.LastDeploy.Status != store.DeployRunning || !strings.Contains(ds[0].Log, "listens on 127.0.0.1:3000 only") {
		t.Fatalf("expected loopback warning:\n%s", ds[0].Log)
	}

	// Run config is only for VPS apps, and needs a start command.
	if code := h.do("PUT", path+"/run-config", map[string]string{"startCmd": ""}, nil); code != 400 {
		t.Fatalf("empty start: %d", code)
	}
}

func TestVPSAppTemplates(t *testing.T) {
	h := setup(t)
	h.login()
	var ts []struct {
		Type string `json:"type"`
		Run  struct {
			Start string `json:"start"`
			Port  int    `json:"port"`
		} `json:"run"`
	}
	h.do("GET", "/api/apps/templates", nil, &ts)
	found := false
	for _, x := range ts {
		if x.Type == "static" && strings.Contains(x.Run.Start, "http.server $PORT") && x.Run.Port == 8080 {
			found = true
		}
	}
	if !found {
		t.Fatalf("templates = %+v", ts)
	}
}

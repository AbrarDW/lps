package api

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/abrardw/lps/internal/docker"
	"github.com/abrardw/lps/internal/store"
)

func TestAppEnvContainer(t *testing.T) {
	h := setup(t)
	h.login()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM nginx\n"), 0o644)
	var a appView
	h.do("POST", "/api/apps", createAppReq{Name: "site", SourcePath: dir, ContainerPort: 80}, &a)
	path := fmt.Sprintf("/api/apps/%d", a.ID)
	h.waitDeployed(path)

	bad := [][]envInput{
		{{Name: "1BAD", Value: "x"}},
		{{Name: "A-B", Value: "x"}},
		{{Name: "DUP", Value: "1"}, {Name: "DUP", Value: "2"}},
		{{Name: "LPS_DIR", Value: "x"}},
		{{Name: "NUL", Value: "a\x00b"}},
		{{Name: "NEW_SECRET", Secret: true, Keep: true}}, // keep needs a stored secret
	}
	for _, b := range bad {
		if code := h.do("PUT", path+"/env", map[string]any{"vars": b}, nil); code != 400 {
			t.Fatalf("expected 400 for %+v, got %d", b, code)
		}
	}

	vars := []envInput{
		{Name: "NODE_ENV", Value: "production"},
		{Name: "DATABASE_URL", Value: "postgres://u:hunter2@db/app", Secret: true},
		{Name: "PORT", Value: "80"}, // allowed for container apps
	}
	var got []envView
	if code := h.do("PUT", path+"/env", map[string]any{"vars": vars}, &got); code != 200 {
		t.Fatalf("set env: %d", code)
	}
	// Secrets never come back.
	h.do("GET", path+"/env", nil, &got)
	if len(got) != 3 || got[0].Value != "production" || got[1].Value != "" || !got[1].Secret || !got[1].HasValue {
		t.Fatalf("env view = %+v", got)
	}
	// The save redeploys, and the new container gets the variables.
	waitFor(t, func() bool {
		h.eng.mu.Lock()
		defer h.eng.mu.Unlock()
		return len(h.eng.appSpecs) == 2
	})
	h.waitDeployed(path)
	h.eng.mu.Lock()
	env := h.eng.appSpecs[1].Env
	h.eng.mu.Unlock()
	if !slices.Equal(env, []string{"NODE_ENV=production", "DATABASE_URL=postgres://u:hunter2@db/app", "PORT=80"}) {
		t.Fatalf("container env = %v", env)
	}

	// Keeping a secret without resending it, renaming nothing, dropping PORT.
	keep := []envInput{
		{Name: "NODE_ENV", Value: "staging"},
		{Name: "DATABASE_URL", Secret: true, Keep: true},
	}
	if code := h.do("PUT", path+"/env", map[string]any{"vars": keep}, nil); code != 200 {
		t.Fatalf("keep: %d", code)
	}
	h.waitDeployed(path)
	stored, _ := h.srv.store.AppEnv(t.Context(), a.ID)
	if len(stored) != 2 || stored[1].Value != "postgres://u:hunter2@db/app" || stored[0].Value != "staging" {
		t.Fatalf("stored = %+v", stored)
	}

	// The activity feed names variables but never shows values.
	var evs []store.Event
	h.do("GET", fmt.Sprintf("/api/events?subject=app&id=%d", a.ID), nil, &evs)
	msgs := []string{}
	for _, e := range evs {
		if e.Kind == "app.env" {
			msgs = append(msgs, e.Message)
		}
	}
	all := strings.Join(msgs, " | ")
	if !strings.Contains(all, "added NODE_ENV, DATABASE_URL, PORT") || !strings.Contains(all, "changed NODE_ENV; removed PORT") {
		t.Fatalf("env events = %s", all)
	}
	if strings.Contains(all, "hunter2") || strings.Contains(all, "production") || strings.Contains(all, "staging") {
		t.Fatalf("values leaked into the activity feed: %s", all)
	}

	// Deleting the app deletes its variables.
	h.do("DELETE", path, nil, nil)
	if left, _ := h.srv.store.AppEnv(t.Context(), a.ID); len(left) != 0 {
		t.Fatalf("env left after delete: %+v", left)
	}
}

func TestAppEnvVPS(t *testing.T) {
	h := setup(t)
	h.login()
	v := h.newRunningVPS()
	var a appView
	h.do("POST", "/api/apps", createAppReq{Name: "api", VPSID: v.ID, SourceKind: "vps", VPSPath: "~/api", ContainerPort: 3000,
		InstallCmd: "npm ci", StartCmd: "npm start"}, &a)
	path := fmt.Sprintf("/api/apps/%d", a.ID)
	h.waitDeployed(path)

	for _, name := range []string{"PORT", "HOST", "HOME"} {
		if code := h.do("PUT", path+"/env", map[string]any{"vars": []envInput{{Name: name, Value: "x"}}}, nil); code != 400 {
			t.Fatalf("%s should be reserved for VPS apps, got %d", name, code)
		}
	}
	if code := h.do("PUT", path+"/env", map[string]any{"vars": []envInput{{Name: "API_KEY", Value: "s3cret", Secret: true}}}, nil); code != 200 {
		t.Fatal("set env failed")
	}
	h.waitDeployed(path)
	waitFor(t, func() bool { _, ok := h.eng.snapshot().procs["api"]; return ok })

	// The install and start commands got the variable; stop/status calls did not.
	fv := h.eng.snapshot()
	h.eng.mu.Lock()
	envs := h.eng.fv.execEnv
	h.eng.mu.Unlock()
	var install, start, stop bool
	for i, e := range fv.execs {
		has := slices.Contains(envs[i], "API_KEY=s3cret")
		switch {
		case strings.HasPrefix(e, "dev: bash -lc npm ci"):
			install = install || has
		case strings.HasPrefix(e, "dev: "+docker.AppHelperPath+" start api"):
			start = start || has
			if !slices.Contains(envs[i], "LPS_PORT=3000") {
				t.Fatalf("LPS vars missing: %v", envs[i])
			}
		case strings.HasPrefix(e, "dev: "+docker.AppHelperPath+" stop api"):
			stop = stop || has
		}
	}
	if !install || !start || stop {
		t.Fatalf("env reach: install=%v start=%v stop=%v", install, start, stop)
	}
}

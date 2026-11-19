package api

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/abrardw/lps/internal/builder"
)

func TestDetectEndpoint(t *testing.T) {
	h := setup(t)
	h.login()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hi</h1>"), 0o644)

	var resp detectResp
	if code := h.do("POST", "/api/apps/detect", map[string]string{"sourcePath": dir}, &resp); code != 200 {
		t.Fatalf("detect: %d", code)
	}
	if resp.HasDockerfile || resp.Detection.Type != "static" || resp.Detection.Port != 80 || len(resp.Templates) < 5 {
		t.Fatalf("resp = %+v", resp)
	}
	if code := h.do("POST", "/api/apps/detect", map[string]string{"sourcePath": "relative/path"}, nil); code != 400 {
		t.Fatalf("relative path: %d", code)
	}
}

func TestCreateAppWithGeneratedDockerfile(t *testing.T) {
	h := setup(t)
	h.login()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hi</h1>"), 0o644)

	if code := h.do("POST", "/api/apps", createAppReq{Name: "site", SourcePath: dir, ContainerPort: 80}, nil); code != 400 {
		t.Fatalf("no dockerfile at all should be 400, got %d", code)
	}
	if code := h.do("POST", "/api/apps", createAppReq{Name: "site", SourcePath: dir, ContainerPort: 80, Dockerfile: "nonsense"}, nil); code != 400 {
		t.Fatalf("dockerfile without FROM should be 400, got %d", code)
	}

	df := builder.Detect(dir).Dockerfile
	var a appView
	if code := h.do("POST", "/api/apps", createAppReq{Name: "site", SourcePath: dir, ContainerPort: 80, Dockerfile: df}, &a); code != 202 {
		t.Fatalf("create: %d", code)
	}
	if a.DockerfileSource != "lps" {
		t.Fatalf("source = %s", a.DockerfileSource)
	}
	waitFor(t, func() bool {
		var got appView
		h.do("GET", fmt.Sprintf("/api/apps/%d", a.ID), nil, &got)
		return !got.Deploying && got.State == "running"
	})
	h.eng.mu.Lock()
	file, ctxFiles := h.eng.buildFiles[0], h.eng.buildCtx[0]
	h.eng.mu.Unlock()
	if file != builder.GeneratedDockerfile || !slices.Contains(ctxFiles, builder.GeneratedDockerfile) {
		t.Fatalf("built with %q, context %v", file, ctxFiles)
	}
	if _, err := os.Stat(filepath.Join(dir, "Dockerfile")); err == nil {
		t.Fatal("project folder must not be modified")
	}

	// Editing the Dockerfile saves it and redeploys.
	var after appView
	newDF := "FROM nginx:1.27-alpine\nCOPY . /usr/share/nginx/html\n"
	if code := h.do("PUT", fmt.Sprintf("/api/apps/%d/dockerfile", a.ID), map[string]string{"dockerfile": newDF}, &after); code != 200 {
		t.Fatalf("set dockerfile: %d", code)
	}
	if after.Dockerfile != newDF {
		t.Fatalf("dockerfile not saved")
	}
	waitFor(t, func() bool {
		h.eng.mu.Lock()
		defer h.eng.mu.Unlock()
		return len(h.eng.builds) == 2
	})
	waitFor(t, func() bool {
		var got appView
		h.do("GET", fmt.Sprintf("/api/apps/%d", a.ID), nil, &got)
		return !got.Deploying
	})
	// Clearing it is refused while the project has no Dockerfile of its own.
	if code := h.do("PUT", fmt.Sprintf("/api/apps/%d/dockerfile", a.ID), map[string]string{"dockerfile": ""}, nil); code != 400 {
		t.Fatalf("clear without project dockerfile: %d", code)
	}
}

func TestCreateAppSaveToProject(t *testing.T) {
	h := setup(t)
	h.login()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<h1>hi</h1>"), 0o644)
	df := builder.Detect(dir).Dockerfile

	var a appView
	if code := h.do("POST", "/api/apps", createAppReq{Name: "site", SourcePath: dir, ContainerPort: 80, Dockerfile: df, SaveToProject: true}, &a); code != 202 {
		t.Fatalf("create: %d", code)
	}
	got, err := os.ReadFile(filepath.Join(dir, "Dockerfile"))
	if err != nil || string(got) != df {
		t.Fatalf("Dockerfile not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".dockerignore")); err != nil {
		t.Fatal(".dockerignore not written")
	}
	if a.DockerfileSource != "project" || a.Dockerfile != "" {
		t.Fatalf("app should use the project's Dockerfile: %+v", a.App)
	}
	waitFor(t, func() bool {
		var g appView
		h.do("GET", fmt.Sprintf("/api/apps/%d", a.ID), nil, &g)
		return !g.Deploying
	})
	h.eng.mu.Lock()
	file := h.eng.buildFiles[0]
	h.eng.mu.Unlock()
	if file != "Dockerfile" {
		t.Fatalf("built with %q", file)
	}

	// Never overwrite an existing Dockerfile.
	if code := h.do("POST", "/api/apps", createAppReq{Name: "site2", SourcePath: dir, ContainerPort: 80, Dockerfile: "FROM scratch\n", SaveToProject: true}, nil); code != 409 {
		t.Fatalf("overwrite should be refused, got %d", code)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "Dockerfile")); string(b) != df {
		t.Fatal("existing Dockerfile was modified")
	}
}

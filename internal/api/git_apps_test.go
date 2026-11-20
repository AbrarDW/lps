package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"

	"github.com/abrardw/lps/internal/gitsrc"
	"github.com/abrardw/lps/internal/store"
)

// gitRepoFixture is a local repository on branch main with a Dockerfile.
type gitRepoFixture struct {
	url string
	dir string
	wt  *git.Worktree
	t   *testing.T
}

func newGitRepo(t *testing.T, files map[string]string) *gitRepoFixture {
	t.Helper()
	gitsrc.AllowFileURLs = true
	t.Cleanup(func() { gitsrc.AllowFileURLs = false })
	dir := t.TempDir()
	repo, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")}})
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	g := &gitRepoFixture{url: "file://" + filepath.ToSlash(dir), dir: dir, wt: wt, t: t}
	g.commit(files, "Initial commit")
	return g
}

func (g *gitRepoFixture) commit(files map[string]string, msg string) string {
	for name, content := range files {
		os.WriteFile(filepath.Join(g.dir, name), []byte(content), 0o644)
		g.wt.Add(name)
	}
	h, err := g.wt.Commit(msg, &git.CommitOptions{AllowEmptyCommits: true, Author: &object.Signature{Name: "Dev", Email: "d@example.com", When: time.Now()}})
	if err != nil {
		g.t.Fatal(err)
	}
	return h.String()
}

func TestGitAppDeploy(t *testing.T) {
	h := setup(t)
	h.login()
	repo := newGitRepo(t, map[string]string{"Dockerfile": "FROM nginx\n", "index.html": "v1"})

	bad := []createAppReq{
		{Name: "g", SourceKind: "git", GitURL: "git@github.com:a/b.git", ContainerPort: 80},
		{Name: "g", SourceKind: "git", GitURL: "https://u:p@github.com/a/b", ContainerPort: 80},
		{Name: "g", SourceKind: "git", GitURL: repo.url, GitBranch: "../x", ContainerPort: 80},
		{Name: "g", SourceKind: "git", GitURL: repo.url, GitToken: "two words", ContainerPort: 80},
	}
	for _, b := range bad {
		if code := h.do("POST", "/api/apps", b, nil); code != 400 {
			t.Fatalf("expected 400 for %+v, got %d", b, code)
		}
	}

	// "Check repository" in the form.
	var det struct {
		HasDockerfile bool
		Commit        struct{ Hash, Message, Branch string }
	}
	if code := h.do("POST", "/api/apps/detect-git", map[string]string{"url": repo.url}, &det); code != 200 || !det.HasDockerfile || det.Commit.Branch != "main" || det.Commit.Message != "Initial commit" {
		t.Fatalf("detect-git: %d %+v", code, det)
	}
	if code := h.do("POST", "/api/apps/detect-git", map[string]string{"url": repo.url, "branch": "nope"}, nil); code != 400 {
		t.Fatalf("detect-git missing branch: %d", code)
	}

	var a appView
	if code := h.do("POST", "/api/apps", createAppReq{Name: "site", SourceKind: "git", GitURL: repo.url, GitToken: "tok123", ContainerPort: 80}, &a); code != 202 {
		t.Fatalf("create: %d", code)
	}
	path := fmt.Sprintf("/api/apps/%d", a.ID)
	got := h.waitDeployed(path)
	if got.LastDeploy.Status != store.DeployRunning {
		var ds []store.Deployment
		h.do("GET", path+"/deployments", nil, &ds)
		t.Fatalf("deploy failed:\n%s", ds[0].Log)
	}
	first := repo.commit(nil, "unused") // make sure hashes differ below
	_ = first
	var ds []store.Deployment
	h.do("GET", path+"/deployments", nil, &ds)
	if ds[0].CommitHash == "" || ds[0].CommitMessage != "Initial commit" || !strings.Contains(ds[0].Log, "commit "+ds[0].CommitHash[:7]+" on main by Dev: Initial commit") {
		t.Fatalf("deployment = %+v", ds[0])
	}
	h.do("GET", path, nil, &got)
	if got.LastCommit != ds[0].CommitHash || !got.HasGitToken || got.SourceKind != "git" || got.GitURL != repo.url {
		t.Fatalf("app view = %+v", got)
	}
	// The token never comes back, and the build context came from the repo.
	raw := h.rawGet(path)
	if strings.Contains(raw, "tok123") {
		t.Fatal("token leaked in the app view")
	}
	h.eng.mu.Lock()
	ctx := h.eng.buildCtx[len(h.eng.buildCtx)-1]
	h.eng.mu.Unlock()
	if !slices.Contains(ctx, "index.html") || slices.Contains(ctx, ".git") {
		t.Fatalf("build context = %v", ctx)
	}

	// Auto-deploy: a new commit on the branch is deployed by the poller.
	if code := h.do("PUT", path+"/git", map[string]any{"branch": "main", "autoDeploy": true}, &got); code != 200 || !got.AutoDeploy || !got.HasGitToken {
		t.Fatalf("set git: %d %+v", code, got)
	}
	h.srv.pollGit(context.Background()) // "unused" commit is newer than the deployed one
	h.waitDeployed(path)
	v2 := repo.commit(map[string]string{"index.html": "v2"}, "Second version")
	h.srv.pollGit(context.Background())
	waitFor(t, func() bool {
		var g appView
		h.do("GET", path, nil, &g)
		return !g.Deploying && g.LastCommit == v2
	})
	h.do("GET", path+"/deployments", nil, &ds)
	if ds[0].CommitMessage != "Second version" {
		t.Fatalf("auto-deployed %+v", ds[0])
	}
	// Nothing new: no deploy.
	n := len(ds)
	h.srv.pollGit(context.Background())
	h.do("GET", path+"/deployments", nil, &ds)
	if len(ds) != n {
		t.Fatal("poll deployed without a new commit")
	}

	// A commit that fails to build is tried once, not on every poll.
	h.eng.mu.Lock()
	h.eng.buildErr = fmt.Errorf("broken")
	h.eng.mu.Unlock()
	repo.commit(map[string]string{"index.html": "v3"}, "Broken version")
	h.srv.pollGit(context.Background())
	h.waitDeployed(path)
	h.srv.pollGit(context.Background())
	h.waitDeployed(path)
	h.do("GET", path+"/deployments", nil, &ds)
	if len(ds) != n+1 || ds[0].Status != store.DeployFailed {
		t.Fatalf("failed commit retried: %d deployments, newest %s", len(ds), ds[0].Status)
	}

	// Clearing the token, and the activity feed never shows it.
	var cleared appView
	h.do("PUT", path+"/git", map[string]any{"branch": "main", "clearToken": true, "autoDeploy": false}, &cleared)
	if cleared.HasGitToken || cleared.AutoDeploy {
		t.Fatal("token not cleared")
	}
	var evs []store.Event
	h.do("GET", fmt.Sprintf("/api/events?subject=app&id=%d&limit=100", a.ID), nil, &evs)
	all := ""
	for _, e := range evs {
		all += e.Message + "\n"
	}
	if strings.Contains(all, "tok123") || !strings.Contains(all, "New commit "+v2[:7]+" on main of site: deploying") || !strings.Contains(all, "access token removed") {
		t.Fatalf("events:\n%s", all)
	}

	// Deleting the app removes LPS's clone.
	clone := h.srv.gitDir("site")
	if _, err := os.Stat(filepath.Join(clone, "index.html")); err != nil {
		t.Fatal("clone missing before delete")
	}
	h.do("DELETE", path, nil, nil)
	if _, err := os.Stat(clone); !os.IsNotExist(err) {
		t.Fatal("clone left after delete")
	}
}

func TestGitAppInVPS(t *testing.T) {
	h := setup(t)
	h.login()
	v := h.newRunningVPS()
	repo := newGitRepo(t, map[string]string{"server.js": "x", "package.json": "{}"})
	var a appView
	req := createAppReq{Name: "api", VPSID: v.ID, SourceKind: "git", GitURL: repo.url, GitBranch: "main", ContainerPort: 3000, StartCmd: "npm start"}
	if code := h.do("POST", "/api/apps", req, &a); code != 202 {
		t.Fatalf("create: %d", code)
	}
	got := h.waitDeployed(fmt.Sprintf("/api/apps/%d", a.ID))
	if got.LastDeploy.Status != store.DeployRunning || got.LastCommit == "" {
		t.Fatalf("deploy: %+v", got)
	}
	files := h.eng.snapshot().copies["/home/dev/apps/api.new"]
	slices.Sort(files)
	if strings.Join(files, ",") != "package.json,server.js" {
		t.Fatalf("copied %v", files)
	}
}

// rawGet returns a response body as text.
func (h *harness) rawGet(path string) string {
	h.t.Helper()
	var m map[string]any
	h.do("GET", path, nil, &m)
	return fmt.Sprint(m)
}

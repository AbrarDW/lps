package api

// Apps deployed from a git repository. On every deploy LPS makes a fresh
// shallow clone of the branch into <data>/git/<app> (the app's SourcePath),
// then builds or copies it like a folder on this PC. With auto-deploy on, a
// background check deploys new commits on the branch.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/abrardw/lps/internal/builder"
	"github.com/abrardw/lps/internal/gitsrc"
	"github.com/abrardw/lps/internal/store"
)

var (
	branchRe = regexp.MustCompile(`^[A-Za-z0-9._/-]{1,200}$`)
	// gitPollInterval is how often auto-deploy checks branches for new commits.
	gitPollInterval = 5 * time.Minute
	gitTimeout      = 5 * time.Minute
)

const maxGitToken = 1024

func cleanBranch(b string) (string, error) {
	b = strings.TrimSpace(b)
	if b == "" {
		return "", nil
	}
	if !branchRe.MatchString(b) || strings.Contains(b, "..") || strings.HasPrefix(b, "-") || strings.HasPrefix(b, "/") || strings.HasSuffix(b, "/") {
		return "", fmt.Errorf("%q is not a valid branch name", b)
	}
	return b, nil
}

func cleanToken(t string) (string, error) {
	t = strings.TrimSpace(t)
	if len(t) > maxGitToken || strings.ContainsAny(t, " \r\n\t\x00") {
		return "", errors.New("the access token looks wrong: paste just the token")
	}
	return t, nil
}

// validateGit checks the git fields of a create request.
func (req *createAppReq) validateGit() error {
	var err error
	if req.GitURL, err = gitsrc.ValidateURL(req.GitURL); err != nil {
		return err
	}
	if req.GitBranch, err = cleanBranch(req.GitBranch); err != nil {
		return err
	}
	if req.GitToken, err = cleanToken(req.GitToken); err != nil {
		return err
	}
	req.SourcePath, req.VPSPath, req.SaveToProject = "", "", false
	return nil
}

// gitDir is where an app's repository is checked out.
func (s *Server) gitDir(name string) string {
	base := s.cfg.DataDir
	if base == "" {
		base = filepath.Join(os.TempDir(), "lps")
	}
	return filepath.Join(base, "git", name)
}

func gitRepo(a *store.App) gitsrc.Repo {
	return gitsrc.Repo{URL: a.GitURL, Branch: a.GitBranch, Token: a.GitToken}
}

// prepareSource fetches the code for a deployment. For git apps it clones
// the branch into SourcePath and records the commit on the deployment.
func (s *Server) prepareSource(a *store.App, d *store.Deployment, log io.Writer) error {
	if a.SourceKind != store.SourceGit {
		return nil
	}
	branch := a.GitBranch
	if branch == "" {
		branch = "default branch"
	}
	fmt.Fprintf(log, "[lps] fetching %s (%s)\n", a.GitURL, branch)
	ctx, cancel := context.WithTimeout(s.ctx, gitTimeout)
	defer cancel()
	c, err := gitsrc.Checkout(ctx, gitRepo(a), a.SourcePath, nil)
	if err != nil {
		return fmt.Errorf("git: %w", err)
	}
	fmt.Fprintf(log, "[lps] commit %s on %s by %s: %s\n", c.Short(), c.Branch, c.Author, c.Message)
	return s.store.SetDeploymentCommit(s.ctx, d.ID, c.Hash, c.Message)
}

// finishGitDeploy records the deployed commit after a successful deploy.
func (s *Server) finishGitDeploy(a *store.App, d *store.Deployment) string {
	if a.SourceKind != store.SourceGit {
		return ""
	}
	ds, err := s.store.ListDeployments(s.ctx, a.ID, 5)
	if err != nil {
		return ""
	}
	for _, x := range ds {
		if x.ID == d.ID && x.CommitHash != "" {
			_ = s.store.SetAppLastCommit(s.ctx, a.ID, x.CommitHash)
			return x.CommitHash
		}
	}
	return ""
}

func shortHash(h string) string {
	if len(h) > 7 {
		return h[:7]
	}
	return h
}

// handleDetectGit clones a repository into a temporary folder and proposes
// how to build it, like /apps/detect does for a folder.
func (s *Server) handleDetectGit(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL    string `json:"url"`
		Branch string `json:"branch"`
		Token  string `json:"token"`
	}
	if !decode(w, r, &req) {
		return
	}
	var err error
	repo := gitsrc.Repo{}
	if repo.URL, err = gitsrc.ValidateURL(req.URL); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if repo.Branch, err = cleanBranch(req.Branch); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if repo.Token, err = cleanToken(req.Token); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	tmp, err := os.MkdirTemp("", "lps-detect-")
	if err != nil {
		s.internalError(w, err)
		return
	}
	defer os.RemoveAll(tmp)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	dir := filepath.Join(tmp, "repo")
	c, err := gitsrc.Checkout(ctx, repo, dir, nil)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, struct {
		detectResp
		Commit gitsrc.Commit `json:"commit"`
	}{detectResp{
		HasDockerfile:     builder.HasDockerfile(dir),
		ProjectDockerfile: readProjectDockerfile(dir),
		Detection:         builder.Detect(dir),
		Templates:         builder.Templates(dir),
	}, c})
}

// handleSetAppGit changes a git app's branch, token and auto-deploy setting.
func (s *Server) handleSetAppGit(w http.ResponseWriter, r *http.Request) {
	a, ok := s.loadApp(w, r)
	if !ok {
		return
	}
	if a.SourceKind != store.SourceGit {
		writeError(w, 400, "this app isn't deployed from a git repository")
		return
	}
	var req struct {
		Branch     string `json:"branch"`
		Token      string `json:"token"`      // new token; "" keeps the current one
		ClearToken bool   `json:"clearToken"` // remove the token
		AutoDeploy bool   `json:"autoDeploy"`
	}
	if !decode(w, r, &req) {
		return
	}
	branch, err := cleanBranch(req.Branch)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	token, err := cleanToken(req.Token)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	var changes []string
	switch {
	case req.ClearToken:
		if a.GitToken != "" {
			changes = append(changes, "access token removed")
		}
		token = ""
	case token == "":
		token = a.GitToken
	default:
		changes = append(changes, "access token replaced")
	}
	if branch != a.GitBranch {
		changes = append(changes, fmt.Sprintf("branch %s", orDefault(branch)))
	}
	if req.AutoDeploy != a.AutoDeploy {
		changes = append(changes, map[bool]string{true: "auto-deploy on", false: "auto-deploy off"}[req.AutoDeploy])
	}
	if err := s.store.SetAppGit(r.Context(), a.ID, branch, token, req.AutoDeploy); err != nil {
		s.internalError(w, err)
		return
	}
	if branch != a.GitBranch {
		s.mu.Lock()
		delete(s.gitTried, a.ID)
		s.mu.Unlock()
	}
	a.GitBranch, a.GitToken, a.AutoDeploy = branch, token, req.AutoDeploy
	if len(changes) > 0 {
		s.info(r, "app.git", subjApp, a.ID, a.Name, "Git settings of %s changed: %s", a.Name, strings.Join(changes, ", "))
	}
	if req.AutoDeploy {
		s.kickGit()
	}
	writeJSON(w, 200, s.appView(r, a))
}

func orDefault(branch string) string {
	if branch == "" {
		return "default branch"
	}
	return branch
}

// kickGit asks gitPollLoop to check for new commits now.
func (s *Server) kickGit() {
	select {
	case s.gitKick <- struct{}{}:
	default:
	}
}

func (s *Server) gitPollLoop() {
	defer s.wg.Done()
	t := time.NewTicker(gitPollInterval)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		case <-s.gitKick:
		}
		s.pollGit(s.ctx)
	}
}

// pollGit deploys new commits for apps with auto-deploy on. A commit that
// failed to deploy is not retried automatically (Redeploy retries it).
func (s *Server) pollGit(ctx context.Context) {
	apps, err := s.store.ListApps(ctx)
	if err != nil {
		return
	}
	for _, a := range apps {
		if a.SourceKind != store.SourceGit || !a.AutoDeploy {
			continue
		}
		s.mu.Lock()
		busy, tried := s.deploying[a.ID], s.gitTried[a.ID]
		s.mu.Unlock()
		if busy {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		head, err := gitsrc.Head(cctx, gitRepo(a))
		cancel()
		if err != nil {
			s.log.Warn("auto-deploy: check repository", "app", a.Name, "err", err)
			continue
		}
		if head == a.LastCommit || head == tried {
			continue
		}
		s.mu.Lock()
		s.gitTried[a.ID] = head
		s.mu.Unlock()
		s.info(nil, "app.auto_deploy", subjApp, a.ID, a.Name, "New commit %s on %s of %s: deploying", shortHash(head), orDefault(a.GitBranch), a.Name)
		if _, err := s.startDeploy(a); err != nil && !errors.Is(err, errDeployInProgress) {
			s.log.Warn("auto-deploy", "app", a.Name, "err", err)
		}
	}
}

// Package gitsrc fetches app source code from git repositories over HTTPS,
// with a pure-Go git client so nothing has to be installed on the PC.
package gitsrc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/transport"
	"github.com/go-git/go-git/v5/plumbing/transport/http"
	"github.com/go-git/go-git/v5/storage/memory"
)

// Repo is where to fetch code from.
type Repo struct {
	URL    string
	Branch string // "" = the repository's default branch
	Token  string // optional access token for private repositories
}

// Commit describes what was checked out.
type Commit struct {
	Hash    string    `json:"hash"`
	Message string    `json:"message"` // first line
	Author  string    `json:"author"`
	When    time.Time `json:"when"`
	Branch  string    `json:"branch"`
}

// Short is the abbreviated hash shown in the UI.
func (c Commit) Short() string {
	if len(c.Hash) > 7 {
		return c.Hash[:7]
	}
	return c.Hash
}

// AllowFileURLs lets tests use file:// repositories.
var AllowFileURLs = false

var (
	// scpLike matches SSH shorthand such as git@github.com:you/app.git.
	scpLike = regexp.MustCompile(`^[\w.-]+@[\w.-]+:[^/]`)
	errSSH  = errors.New("SSH URLs (like git@github.com:you/app) need keys; use the HTTPS URL, plus an access token for a private repository")
)

// ValidateURL accepts http(s) clone URLs without embedded credentials.
func ValidateURL(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if scpLike.MatchString(raw) {
		return "", errSSH
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" && u.Scheme != "file" {
		return "", errors.New("enter the repository's HTTPS clone URL, e.g. https://github.com/you/app.git")
	}
	switch u.Scheme {
	case "https", "http":
	case "file":
		if !AllowFileURLs {
			return "", errors.New("only http(s) repository URLs are supported")
		}
	case "ssh", "git+ssh":
		return "", errSSH
	default:
		return "", errors.New("only http(s) repository URLs are supported")
	}
	if u.User != nil {
		return "", errors.New("don't put credentials in the URL; use the access token field instead")
	}
	return raw, nil
}

func (r Repo) auth() transport.AuthMethod {
	if r.Token == "" {
		return nil
	}
	// GitHub, GitLab and Gitea accept a token as the password with any user name.
	return &http.BasicAuth{Username: "x-access-token", Password: r.Token}
}

// friendly turns go-git errors into advice.
func friendly(err error, r Repo) error {
	switch {
	case errors.Is(err, transport.ErrAuthenticationRequired), errors.Is(err, transport.ErrAuthorizationFailed):
		if r.Token == "" {
			// GitHub answers this for missing repositories too, so as not to
			// reveal which private ones exist.
			return errors.New("repository not found, or private: check the URL, and add an access token if the repository is private")
		}
		return errors.New("the access token was refused: check that it is valid and can read this repository")
	case errors.Is(err, transport.ErrRepositoryNotFound):
		return errors.New("repository not found: check the URL (private repositories also need an access token)")
	case errors.Is(err, transport.ErrEmptyRemoteRepository):
		return errors.New("the repository is empty: push some code first")
	case errors.Is(err, plumbing.ErrReferenceNotFound), errors.As(err, new(git.NoMatchingRefSpecError)):
		return fmt.Errorf("branch %q not found in the repository", r.Branch)
	}
	return err
}

// Checkout makes dir a fresh shallow copy of the branch's latest commit.
// Whatever was in dir is replaced.
func Checkout(ctx context.Context, r Repo, dir string, progress io.Writer) (Commit, error) {
	tmp := dir + ".tmp"
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return Commit{}, err
	}
	opts := &git.CloneOptions{URL: r.URL, Auth: r.auth(), Depth: 1, SingleBranch: true, Tags: git.NoTags, Progress: progress}
	if r.Branch != "" {
		opts.ReferenceName = plumbing.NewBranchReferenceName(r.Branch)
	}
	repo, err := git.PlainCloneContext(ctx, tmp, false, opts)
	if err != nil {
		_ = os.RemoveAll(tmp)
		return Commit{}, friendly(err, r)
	}
	head, err := repo.Head()
	if err != nil {
		_ = os.RemoveAll(tmp)
		return Commit{}, err
	}
	c, err := repo.CommitObject(head.Hash())
	if err != nil {
		_ = os.RemoveAll(tmp)
		return Commit{}, err
	}
	if err := os.RemoveAll(dir); err != nil {
		return Commit{}, err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return Commit{}, err
	}
	msg, _, _ := strings.Cut(strings.TrimSpace(c.Message), "\n")
	return Commit{Hash: c.Hash.String(), Message: msg, Author: c.Author.Name, When: c.Author.When, Branch: head.Name().Short()}, nil
}

// Head returns the latest commit hash of the branch without cloning.
func Head(ctx context.Context, r Repo) (string, error) {
	rem := git.NewRemote(memory.NewStorage(), &config.RemoteConfig{Name: "origin", URLs: []string{r.URL}})
	refs, err := rem.ListContext(ctx, &git.ListOptions{Auth: r.auth()})
	if err != nil {
		return "", friendly(err, r)
	}
	want := plumbing.HEAD
	if r.Branch != "" {
		want = plumbing.NewBranchReferenceName(r.Branch)
	}
	byName := map[plumbing.ReferenceName]*plumbing.Reference{}
	for _, ref := range refs {
		byName[ref.Name()] = ref
	}
	ref, ok := byName[want]
	for ok && ref.Type() == plumbing.SymbolicReference { // HEAD -> refs/heads/main
		ref, ok = byName[ref.Target()]
	}
	if !ok {
		return "", fmt.Errorf("branch %q not found in the repository", r.Branch)
	}
	return ref.Hash().String(), nil
}

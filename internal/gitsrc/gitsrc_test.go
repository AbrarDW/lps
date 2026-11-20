package gitsrc

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// makeRepo creates a repository with one commit on main and returns its URL
// and a function that adds commits.
func makeRepo(t *testing.T) (string, func(file, content, msg string) string) {
	t.Helper()
	AllowFileURLs = true
	t.Cleanup(func() { AllowFileURLs = false })
	dir := t.TempDir()
	repo, err := git.PlainInitWithOptions(dir, &git.PlainInitOptions{InitOptions: git.InitOptions{DefaultBranch: plumbing.NewBranchReferenceName("main")}})
	if err != nil {
		t.Fatal(err)
	}
	wt, _ := repo.Worktree()
	commit := func(file, content, msg string) string {
		os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644)
		wt.Add(file)
		h, err := wt.Commit(msg, &git.CommitOptions{Author: &object.Signature{Name: "Dev", Email: "dev@example.com", When: time.Now()}})
		if err != nil {
			t.Fatal(err)
		}
		return h.String()
	}
	commit("index.html", "v1", "First version\n\nmore text")
	return "file://" + filepath.ToSlash(dir), commit
}

func TestCheckoutAndHead(t *testing.T) {
	ctx := context.Background()
	url, commit := makeRepo(t)
	out := filepath.Join(t.TempDir(), "app")

	c, err := Checkout(ctx, Repo{URL: url}, out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Branch != "main" || c.Message != "First version" || c.Author != "Dev" || len(c.Short()) != 7 {
		t.Fatalf("commit = %+v", c)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "index.html")); string(b) != "v1" {
		t.Fatalf("file = %q", b)
	}
	h, err := Head(ctx, Repo{URL: url})
	if err != nil || h != c.Hash {
		t.Fatalf("head = %s %v, want %s", h, err, c.Hash)
	}

	// A new commit: Head sees it, Checkout replaces the folder.
	newHash := commit("index.html", "v2", "Second version")
	if h, _ := Head(ctx, Repo{URL: url, Branch: "main"}); h != newHash {
		t.Fatalf("head after push = %s", h)
	}
	os.WriteFile(filepath.Join(out, "stale.txt"), []byte("x"), 0o644)
	c, err = Checkout(ctx, Repo{URL: url, Branch: "main"}, out, nil)
	if err != nil || c.Hash != newHash {
		t.Fatalf("second checkout: %+v %v", c, err)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "index.html")); string(b) != "v2" {
		t.Fatalf("file = %q", b)
	}
	if _, err := os.Stat(filepath.Join(out, "stale.txt")); !os.IsNotExist(err) {
		t.Fatal("old files must not survive a checkout")
	}

	// Missing branch: clear message, folder untouched.
	if _, err := Checkout(ctx, Repo{URL: url, Branch: "nope"}, out, nil); err == nil || !strings.Contains(err.Error(), `branch "nope" not found`) {
		t.Fatalf("missing branch: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "index.html")); string(b) != "v2" {
		t.Fatal("a failed checkout must leave the previous one in place")
	}
	if _, err := Head(ctx, Repo{URL: url, Branch: "nope"}); err == nil {
		t.Fatal("head of missing branch")
	}
}

func TestValidateURL(t *testing.T) {
	for _, ok := range []string{"https://github.com/a/b.git", " https://gitlab.com/a/b ", "http://192.168.1.5:3000/a/b.git"} {
		if _, err := ValidateURL(ok); err != nil {
			t.Errorf("%q: %v", ok, err)
		}
	}
	for _, ssh := range []string{"git@github.com:a/b.git", "ssh://git@github.com/a/b"} {
		if _, err := ValidateURL(ssh); err == nil || !strings.Contains(err.Error(), "SSH URLs") {
			t.Errorf("%q: want SSH advice, got %v", ssh, err)
		}
	}
	for _, bad := range []string{"git@github.com:a/b.git", "ssh://git@github.com/a/b", "https://user:pw@github.com/a/b", "file:///etc", "github.com/a/b", ""} {
		if _, err := ValidateURL(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
}

// TestPublicGitHub clones a real public repository (needs network).
func TestPublicGitHub(t *testing.T) {
	if os.Getenv("LPS_NET_TESTS") == "" {
		t.Skip("set LPS_NET_TESTS=1 to clone from GitHub")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	r := Repo{URL: "https://github.com/octocat/Hello-World.git"}
	c, err := Checkout(ctx, r, filepath.Join(t.TempDir(), "hw"), nil)
	if err != nil {
		t.Fatal(err)
	}
	h, err := Head(ctx, r)
	if err != nil || h != c.Hash {
		t.Fatalf("head %s %v vs %s", h, err, c.Hash)
	}
	t.Logf("cloned %s %s %q", c.Branch, c.Short(), c.Message)
	if _, err := Head(ctx, Repo{URL: "https://github.com/octocat/this-repo-does-not-exist-lps.git"}); err == nil {
		t.Fatal("missing repo")
	} else {
		t.Logf("missing repo error: %v", err)
	}
}

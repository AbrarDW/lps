// Package update finds new LPS releases on GitHub and downloads their
// installer, verified against the checksum published with it.
package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// DefaultRepo is where LPS releases are published.
const DefaultRepo = "AbrarDW/lps"

// maxInstaller caps the download; the installer is ~12 MB.
const maxInstaller = 300 << 20

// Asset is a file attached to a release.
type Asset struct {
	Name string `json:"name"`
	URL  string `json:"url"`
	Size int64  `json:"size"`
}

// Release is a published LPS version.
type Release struct {
	Version   string    `json:"version"` // without the leading v
	Tag       string    `json:"tag"`
	Name      string    `json:"name"`
	Notes     string    `json:"notes"`
	URL       string    `json:"url"` // the release page
	Published time.Time `json:"published"`
	Installer *Asset    `json:"installer,omitempty"`
	Checksum  *Asset    `json:"-"`
}

// Checker talks to the GitHub releases API.
type Checker struct {
	APIBase string // https://api.github.com (tests point it elsewhere)
	Repo    string
	Client  *http.Client
}

// NewChecker returns a checker for repo with sensible timeouts.
func NewChecker(repo string) *Checker {
	return &Checker{APIBase: "https://api.github.com", Repo: repo, Client: &http.Client{Timeout: 5 * time.Minute}}
}

// Latest returns the newest published (non-draft, non-prerelease) release.
func (c *Checker) Latest(ctx context.Context) (*Release, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", fmt.Sprintf("%s/repos/%s/releases/latest", c.APIBase, c.Repo), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "lps-update-check")
	res, err := c.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("could not reach GitHub: %w", err)
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, errors.New("no releases published yet")
	case http.StatusForbidden, http.StatusTooManyRequests:
		return nil, errors.New("GitHub is rate-limiting update checks; LPS will try again later")
	default:
		return nil, fmt.Errorf("GitHub answered %s", res.Status)
	}
	var gh struct {
		TagName     string    `json:"tag_name"`
		Name        string    `json:"name"`
		Body        string    `json:"body"`
		HTMLURL     string    `json:"html_url"`
		PublishedAt time.Time `json:"published_at"`
		Assets      []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
			Size int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&gh); err != nil {
		return nil, fmt.Errorf("reading the release: %w", err)
	}
	r := &Release{Version: strings.TrimPrefix(gh.TagName, "v"), Tag: gh.TagName, Name: gh.Name, Notes: gh.Body, URL: gh.HTMLURL, Published: gh.PublishedAt}
	for _, a := range gh.Assets {
		a := Asset{Name: a.Name, URL: a.URL, Size: a.Size}
		switch {
		case strings.HasPrefix(a.Name, "LPS-Setup-") && strings.HasSuffix(a.Name, ".exe"):
			r.Installer = &a
		case strings.HasPrefix(a.Name, "LPS-Setup-") && strings.HasSuffix(a.Name, ".exe.sha256"):
			r.Checksum = &a
		}
	}
	return r, nil
}

var (
	semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?$`)
	// git describe output for a build after a tag: v0.1.0-3-gabc1234[-dirty]
	describeRe = regexp.MustCompile(`-\d+-g[0-9a-f]+(-dirty)?$`)
)

type version struct {
	nums [3]int
	pre  string // pre-release ("beta.1"); "" sorts after any pre-release
}

func parse(s string) (version, bool) {
	m := semverRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return version{}, false
	}
	var v version
	for i := range 3 {
		v.nums[i], _ = strconv.Atoi(m[i+1])
	}
	v.pre = m[4]
	return v, true
}

func (a version) less(b version) bool {
	for i := range 3 {
		if a.nums[i] != b.nums[i] {
			return a.nums[i] < b.nums[i]
		}
	}
	switch {
	case a.pre == b.pre:
		return false
	case a.pre == "":
		return false // 1.0.0 > 1.0.0-beta
	case b.pre == "":
		return true
	}
	return a.pre < b.pre
}

// Comparable reports whether a running version can be compared with
// releases: development builds ("dev", a bare commit hash) cannot.
func Comparable(current string) bool {
	_, ok := parse(describeRe.ReplaceAllString(current, ""))
	return ok
}

// Newer reports whether latest is a newer release than current. A build made
// after a tag (git describe "v0.1.0-3-gabc") counts as that tag.
func Newer(current, latest string) bool {
	cur, ok := parse(describeRe.ReplaceAllString(current, ""))
	if !ok {
		return false
	}
	lat, ok := parse(latest)
	return ok && cur.less(lat)
}

// Download fetches the release's installer into dir, checks it against the
// published SHA-256 and returns its path.
func (c *Checker) Download(ctx context.Context, r *Release, dir string) (string, error) {
	if r.Installer == nil || r.Checksum == nil {
		return "", errors.New("this release has no Windows installer with a checksum")
	}
	if r.Installer.Size > maxInstaller {
		return "", errors.New("the installer is unexpectedly large")
	}
	sumText, err := c.get(ctx, r.Checksum.URL, 4<<10)
	if err != nil {
		return "", fmt.Errorf("download checksum: %w", err)
	}
	want, _, _ := strings.Cut(strings.TrimSpace(string(sumText)), " ")
	want = strings.ToLower(want)
	if len(want) != 64 {
		return "", errors.New("the published checksum is malformed")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, filepath.Base(r.Installer.Name))
	tmp := path + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	err = c.stream(ctx, r.Installer.URL, io.MultiWriter(f, h))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("download installer: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(tmp)
		return "", fmt.Errorf("the downloaded installer doesn't match its checksum (got %s…, want %s…); not installing", got[:12], want[:12])
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return path, nil
}

func checkURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && !strings.HasPrefix(u.Host, "127.0.0.1")) {
		return fmt.Errorf("refusing to download from %q", raw)
	}
	return nil
}

func (c *Checker) get(ctx context.Context, url string, limit int64) ([]byte, error) {
	var b strings.Builder
	if err := c.streamLimit(ctx, url, &b, limit); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

func (c *Checker) stream(ctx context.Context, url string, w io.Writer) error {
	return c.streamLimit(ctx, url, w, maxInstaller)
}

func (c *Checker) streamLimit(ctx context.Context, url string, w io.Writer, limit int64) error {
	if err := checkURL(url); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "lps-update")
	res, err := c.Client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s", req.URL.Host, res.Status)
	}
	n, err := io.Copy(w, io.LimitReader(res.Body, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return errors.New("download is larger than expected")
	}
	return nil
}

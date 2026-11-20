package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		cur, lat string
		want     bool
	}{
		{"v0.1.0", "0.2.0", true},
		{"0.2.0", "0.2.0", false},
		{"v0.2.0", "0.1.9", false},
		{"0.9.0", "0.10.0", true}, // numeric, not string, order
		{"v0.1.0-3-gabc1234", "0.1.0", false},
		{"v0.1.0-3-gabc1234-dirty", "0.2.0", true},
		{"0.2.0-beta.1", "0.2.0", true},
		{"0.2.0", "0.2.0-beta.1", false},
		{"dev", "9.9.9", false},
		{"54ebf8c", "9.9.9", false},
		{"0.1.0", "garbage", false},
	}
	for _, c := range cases {
		if got := Newer(c.cur, c.lat); got != c.want {
			t.Errorf("Newer(%q, %q) = %v, want %v", c.cur, c.lat, got, c.want)
		}
	}
	if Comparable("dev") || Comparable("54ebf8c") || !Comparable("v0.1.0-3-gabc1234") {
		t.Fatal("Comparable")
	}
}

// fakeGitHub serves a release whose installer is body, with checksum sum.
func fakeGitHub(t *testing.T, body, sum string) *httptest.Server {
	t.Helper()
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v0.2.0","name":"LPS v0.2.0","body":"notes","html_url":"https://github.com/o/r/releases/tag/v0.2.0",
			"published_at":"2026-11-20T10:00:00Z","assets":[
			{"name":"LPS-Setup-0.2.0.exe","browser_download_url":"%[1]s/dl/setup.exe","size":%[2]d},
			{"name":"LPS-Setup-0.2.0.exe.sha256","browser_download_url":"%[1]s/dl/setup.sha256","size":85}]}`, ts.URL, len(body))
		case "/dl/setup.exe":
			fmt.Fprint(w, body)
		case "/dl/setup.sha256":
			fmt.Fprintf(w, "%s  LPS-Setup-0.2.0.exe", sum)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

func TestLatestAndDownload(t *testing.T) {
	body := "MZ fake installer"
	s := sha256.Sum256([]byte(body))
	ts := fakeGitHub(t, body, hex.EncodeToString(s[:]))
	c := &Checker{APIBase: ts.URL, Repo: "o/r", Client: ts.Client()}
	r, err := c.Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if r.Version != "0.2.0" || r.Installer == nil || r.Checksum == nil || r.Notes != "notes" {
		t.Fatalf("release = %+v", r)
	}
	dir := t.TempDir()
	path, err := c.Download(context.Background(), r, dir)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != body || filepath.Base(path) != "LPS-Setup-0.2.0.exe" {
		t.Fatalf("downloaded %s: %q", path, b)
	}
}

func TestDownloadRejectsBadChecksum(t *testing.T) {
	ts := fakeGitHub(t, "tampered", strings.Repeat("0", 64))
	c := &Checker{APIBase: ts.URL, Repo: "o/r", Client: ts.Client()}
	r, _ := c.Latest(context.Background())
	dir := t.TempDir()
	if _, err := c.Download(context.Background(), r, dir); err == nil || !strings.Contains(err.Error(), "doesn't match its checksum") {
		t.Fatalf("err = %v", err)
	}
	left, _ := os.ReadDir(dir)
	if len(left) != 0 {
		t.Fatalf("files left behind: %v", left)
	}
}

func TestLatestErrors(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) }))
	defer ts.Close()
	c := &Checker{APIBase: ts.URL, Repo: "o/r", Client: ts.Client()}
	if _, err := c.Latest(context.Background()); err == nil || !strings.Contains(err.Error(), "no releases") {
		t.Fatalf("err = %v", err)
	}
	if err := checkURL("http://evil.example/x"); err == nil {
		t.Fatal("plain http to another host must be refused")
	}
}

// TestRealGitHub checks the real repository (needs network).
func TestRealGitHub(t *testing.T) {
	if os.Getenv("LPS_NET_TESTS") == "" {
		t.Skip("set LPS_NET_TESTS=1")
	}
	r, err := NewChecker(DefaultRepo).Latest(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("latest %s installer=%v checksum=%v", r.Version, r.Installer != nil, r.Checksum != nil)
}

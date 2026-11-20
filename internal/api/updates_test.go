package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/abrardw/lps/internal/update"
)

// fakeReleases serves a v0.2.0 release whose installer is installer and
// whose published checksum is sum ("" = the right one).
func fakeReleases(t *testing.T, installer, sum string) *httptest.Server {
	t.Helper()
	if sum == "" {
		s := sha256.Sum256([]byte(installer))
		sum = hex.EncodeToString(s[:])
	}
	var ts *httptest.Server
	ts = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/r/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v0.2.0","name":"LPS v0.2.0","body":"## What's new\n- snapshots","html_url":"https://github.com/o/r/releases/tag/v0.2.0",
			"assets":[{"name":"LPS-Setup-0.2.0.exe","browser_download_url":"%[1]s/setup.exe","size":%[2]d},
			{"name":"LPS-Setup-0.2.0.exe.sha256","browser_download_url":"%[1]s/setup.sha256","size":85}]}`, ts.URL, len(installer))
		case "/setup.exe":
			fmt.Fprint(w, installer)
		case "/setup.sha256":
			fmt.Fprintf(w, "%s  LPS-Setup-0.2.0.exe\n", sum)
		}
	}))
	t.Cleanup(ts.Close)
	return ts
}

// withUpdater makes h's server a Windows desktop app at version, checking ts.
func withUpdater(t *testing.T, h *harness, ts *httptest.Server, version string) (installed *atomic.Value, quit *atomic.Bool) {
	t.Helper()
	old := canSelfUpdate
	canSelfUpdate = true
	t.Cleanup(func() { canSelfUpdate = old })
	installed, quit = &atomic.Value{}, &atomic.Bool{}
	h.srv.cfg.Version = version
	h.srv.cfg.Quit = func() { quit.Store(true) }
	h.srv.updater = &update.Checker{APIBase: ts.URL, Repo: "o/r", Client: ts.Client()}
	h.srv.hooks.startInstaller = func(path string) error { installed.Store(path); return nil }
	return installed, quit
}

func TestUpdateCheckAndInstall(t *testing.T) {
	h := setup(t)
	h.login()
	ts := fakeReleases(t, "MZ new installer", "")
	installed, quit := withUpdater(t, h, ts, "v0.1.0")

	var v updateView
	h.do("GET", "/api/system/update", nil, &v)
	if v.Available || v.Latest != nil || !v.Enabled || !v.Comparable || v.Current != "v0.1.0" {
		t.Fatalf("before check: %+v", v)
	}
	if code := h.do("POST", "/api/system/update/check", nil, &v); code != 200 {
		t.Fatalf("check: %d", code)
	}
	if !v.Available || v.Latest.Version != "0.2.0" || !strings.Contains(v.Latest.Notes, "snapshots") || !v.CanInstall || v.CheckedAt == nil {
		t.Fatalf("after check: %+v", v)
	}

	// Turning automatic checks off is remembered.
	h.do("PUT", "/api/system/update", map[string]bool{"enabled": false}, &v)
	if v.Enabled {
		t.Fatal("still enabled")
	}
	h.do("PUT", "/api/system/update", map[string]bool{"enabled": true}, nil)

	// Install: download, verify, start the installer, quit.
	if code := h.do("POST", "/api/system/update/install", nil, &v); code != 202 {
		t.Fatalf("install: %d", code)
	}
	waitFor(t, func() bool { return quit.Load() })
	path, _ := installed.Load().(string)
	if filepath.Base(path) != "LPS-Setup-0.2.0.exe" || !strings.HasPrefix(path, filepath.Join(h.srv.cfg.DataDir, "updates")) {
		t.Fatalf("installer path %q", path)
	}
	if b, _ := os.ReadFile(path); string(b) != "MZ new installer" {
		t.Fatalf("installer content %q", b)
	}
}

func TestUpdateRefusals(t *testing.T) {
	h := setup(t)
	h.login()

	// A tampered download is not installed.
	ts := fakeReleases(t, "tampered", strings.Repeat("a", 64))
	installed, quit := withUpdater(t, h, ts, "v0.1.0")
	h.do("POST", "/api/system/update/check", nil, nil)
	h.do("POST", "/api/system/update/install", nil, nil)
	var v updateView
	waitFor(t, func() bool {
		h.do("GET", "/api/system/update", nil, &v)
		return v.InstallErr != ""
	})
	if !strings.Contains(v.InstallErr, "checksum") || installed.Load() != nil || quit.Load() {
		t.Fatalf("tampered: %+v installed=%v quit=%v", v, installed.Load(), quit.Load())
	}

	// Up to date: nothing to install.
	h.srv.cfg.Version = "v0.2.0"
	if code := h.do("POST", "/api/system/update/install", nil, nil); code != 409 {
		t.Fatalf("up to date install: %d", code)
	}
	// Development builds don't compare or install.
	h.srv.cfg.Version = "dev"
	h.do("GET", "/api/system/update", nil, &v)
	if v.Comparable || v.Available || v.CanInstall {
		t.Fatalf("dev build: %+v", v)
	}
	// Without a quit hook (lpsd, Linux) it only links to the release.
	h.srv.cfg.Version = "v0.1.0"
	h.srv.cfg.Quit = nil
	h.do("GET", "/api/system/update", nil, &v)
	if !v.Available || v.CanInstall {
		t.Fatalf("no quit hook: %+v", v)
	}
	if code := h.do("POST", "/api/system/update/install", nil, nil); code != 409 {
		t.Fatalf("install without quit hook: %d", code)
	}
}

package api

import (
	"context"
	"errors"
	"testing"

	"github.com/abrardw/lps/internal/platform"
)

type fakeAutostart struct {
	supported, on bool
}

func (f *fakeAutostart) Supported() bool        { return f.supported }
func (f *fakeAutostart) Enabled() (bool, error) { return f.on, nil }
func (f *fakeAutostart) Set(on bool) error {
	if !f.supported {
		return platform.ErrUnsupported
	}
	f.on = on
	return nil
}

func fakeHooks(wslOK bool, started *int) systemHooks {
	return systemHooks{
		checks: func(context.Context) []platform.Check {
			st := platform.StatusOK
			if !wslOK {
				st = platform.StatusFail
			}
			return []platform.Check{{ID: "wsl", Label: "WSL2", Status: st, Required: true}, {ID: "disk", Status: platform.StatusWarn}}
		},
		startDocker: func() error { *started++; return nil },
		canStart:    func() bool { return true },
		autostart:   &fakeAutostart{supported: true},
	}
}

func TestSetupChecksAccessRule(t *testing.T) {
	h := setup(t)
	n := 0
	h.srv.hooks = fakeHooks(true, &n)

	var resp checksResp
	if code := h.do("GET", "/api/setup/checks", nil, &resp); code != 200 {
		t.Fatalf("before setup: %d", code)
	}
	if !resp.Ready || len(resp.Checks) != 3 { // wsl, disk, docker (no tunnel in this harness)
		t.Fatalf("resp = %+v", resp)
	}
	if code := h.do("POST", "/api/setup/actions/start-docker", nil, nil); code != 202 || n != 1 {
		t.Fatalf("start-docker: %d, calls %d", code, n)
	}

	h.login() // creates the admin, then log out to test the locked-down state
	h.do("POST", "/api/auth/logout", nil, nil)
	if code := h.do("GET", "/api/setup/checks", nil, nil); code != 401 {
		t.Fatalf("after setup, anonymous: %d", code)
	}
	if code := h.do("POST", "/api/setup/actions/start-docker", nil, nil); code != 401 {
		t.Fatalf("action after setup, anonymous: %d", code)
	}
	h.do("POST", "/api/auth/login", credentials{"admin", "supersecret1"}, nil)
	if code := h.do("GET", "/api/setup/checks", nil, nil); code != 200 {
		t.Fatalf("after setup, logged in: %d", code)
	}
}

func TestSetupChecksNotReady(t *testing.T) {
	h := setup(t)
	n := 0
	h.srv.hooks = fakeHooks(false, &n)
	var resp checksResp
	h.do("GET", "/api/setup/checks", nil, &resp)
	if resp.Ready {
		t.Fatal("must not be ready while a required check fails")
	}

	// Docker down: the docker check offers the start action.
	h.eng.pingErr = errors.New("cannot connect")
	h.do("GET", "/api/setup/checks", nil, &resp)
	var dk platform.Check
	for _, c := range resp.Checks {
		if c.ID == "docker" {
			dk = c
		}
	}
	if dk.Status != platform.StatusFail || len(dk.Fixes) != 1 || dk.Fixes[0].Value != "start-docker" {
		t.Fatalf("docker check = %+v", dk)
	}
	if code := h.do("POST", "/api/setup/actions/nope", nil, nil); code != 404 {
		t.Fatalf("unknown action: %d", code)
	}
}

func TestAutostartAPI(t *testing.T) {
	h := setup(t)
	n := 0
	h.srv.hooks = fakeHooks(true, &n)
	if code := h.do("GET", "/api/system/autostart", nil, nil); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
	h.login()
	var got map[string]any
	h.do("PUT", "/api/system/autostart", map[string]bool{"enabled": true}, &got)
	if got["enabled"] != true || got["supported"] != true {
		t.Fatalf("after enable: %v", got)
	}
	h.srv.hooks.autostart = &fakeAutostart{supported: false}
	if code := h.do("PUT", "/api/system/autostart", map[string]bool{"enabled": true}, nil); code != 501 {
		t.Fatalf("unsupported: %d", code)
	}
}

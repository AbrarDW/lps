//go:build linux

package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeSystemctl struct {
	enabled bool
	calls   []string
	fail    error
}

func (f *fakeSystemctl) run(args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	if f.fail != nil {
		return "", f.fail
	}
	switch args[0] {
	case "is-enabled":
		if f.enabled {
			return "enabled\n", nil
		}
		return "disabled\n", errors.New("exit status 1")
	case "enable":
		f.enabled = true
	case "disable":
		f.enabled = false
	}
	return "", nil
}

func TestSystemdAutostart(t *testing.T) {
	f := &fakeSystemctl{}
	a := SystemdAutostart{Systemctl: f.run, UnitFound: true}
	if !a.Supported() {
		t.Fatal("should be supported with a unit and systemctl")
	}
	if on, err := a.Enabled(); err != nil || on {
		t.Fatalf("Enabled = %v, %v; want false, nil (disabled exits non-zero)", on, err)
	}
	if err := a.Set(true); err != nil {
		t.Fatal(err)
	}
	if on, _ := a.Enabled(); !on {
		t.Fatal("not enabled after Set(true)")
	}
	if err := a.Set(false); err != nil {
		t.Fatal(err)
	}
	want := []string{"is-enabled lps.service", "enable lps.service", "is-enabled lps.service", "disable lps.service"}
	if strings.Join(f.calls, "|") != strings.Join(want, "|") {
		t.Fatalf("calls = %q, want %q", f.calls, want)
	}

	f.fail = errors.New("Failed to connect to bus")
	if _, err := a.Enabled(); err == nil {
		t.Fatal("want an error when systemctl can't reach the user manager")
	}
}

func TestSystemdAutostartUnsupported(t *testing.T) {
	for _, a := range []SystemdAutostart{
		{Systemctl: (&fakeSystemctl{}).run}, // package not installed
		{UnitFound: true},                   // no user systemd
	} {
		if a.Supported() {
			t.Fatalf("%+v should be unsupported", a)
		}
		if err := a.Set(true); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("Set = %v, want ErrUnsupported", err)
		}
		if on, err := a.Enabled(); on || err != nil {
			t.Fatalf("Enabled = %v, %v", on, err)
		}
	}
}

func TestSystemdAutostartLingerNote(t *testing.T) {
	dir := t.TempDir()
	a := SystemdAutostart{User: "dev", LingerDir: dir}
	if n := a.Note(); !strings.Contains(n, "sudo loginctl enable-linger dev") {
		t.Fatalf("note = %q", n)
	}
	if err := os.WriteFile(filepath.Join(dir, "dev"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if n := a.Note(); n != "" {
		t.Fatalf("note with lingering on = %q, want none", n)
	}
}

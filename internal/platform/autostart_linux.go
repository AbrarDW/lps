//go:build linux

package platform

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

// SystemdUnit is the systemd user unit the Linux package installs.
const SystemdUnit = "lps.service"

// unitDirs are where packages put systemd user units.
var unitDirs = []string{"/usr/lib/systemd/user", "/lib/systemd/user", "/etc/systemd/user"}

// SystemdAutostart enables the packaged systemd user unit, so LPS starts
// when the user signs in (and at boot, once lingering is on for the user).
type SystemdAutostart struct {
	// Systemctl runs "systemctl --user <args>" and returns its stdout.
	Systemctl func(args ...string) (string, error)
	UnitFound bool   // the package's unit file is installed
	User      string // login name, for the linger hint
	LingerDir string // /var/lib/systemd/linger: one file per lingering user
}

// NewAutostart returns an Autostart for the systemd user unit installed by
// the LPS .deb package. Without the package (or without a user systemd
// session to talk to) it is unsupported.
func NewAutostart() Autostart {
	a := SystemdAutostart{LingerDir: "/var/lib/systemd/linger"}
	if u, err := user.Current(); err == nil {
		a.User = u.Username
	}
	for _, d := range unitDirs {
		if _, err := os.Stat(filepath.Join(d, SystemdUnit)); err == nil {
			a.UnitFound = true
			break
		}
	}
	bin, err := exec.LookPath("systemctl")
	if err != nil || os.Getenv("XDG_RUNTIME_DIR") == "" {
		return a // no user systemd instance reachable
	}
	a.Systemctl = func(args ...string) (string, error) {
		var out, stderr bytes.Buffer
		cmd := exec.Command(bin, append([]string{"--user"}, args...)...)
		cmd.Stdout, cmd.Stderr = &out, &stderr
		err := cmd.Run()
		if err != nil && stderr.Len() > 0 {
			err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
		}
		return out.String(), err
	}
	return a
}

// Supported implements Autostart.
func (a SystemdAutostart) Supported() bool { return a.UnitFound && a.Systemctl != nil }

// Enabled implements Autostart.
func (a SystemdAutostart) Enabled() (bool, error) {
	if !a.Supported() {
		return false, nil
	}
	// is-enabled exits non-zero for "disabled", so trust what it printed.
	out, err := a.Systemctl("is-enabled", SystemdUnit)
	switch strings.TrimSpace(out) {
	case "enabled", "enabled-runtime":
		return true, nil
	case "":
		if err == nil {
			err = errors.New("systemctl printed nothing")
		}
		return false, err
	default: // disabled, masked, linked, static, ...
		return false, nil
	}
}

// Set implements Autostart. It never starts or stops the running LPS: it
// only changes whether systemd starts it at the next sign-in.
func (a SystemdAutostart) Set(on bool) error {
	if !a.Supported() {
		return ErrUnsupported
	}
	verb := "disable"
	if on {
		verb = "enable"
	}
	_, err := a.Systemctl(verb, SystemdUnit)
	return err
}

// Label names the switch in the dashboard.
func (a SystemdAutostart) Label() string { return "Start LPS when I sign in" }

// Note explains how to make LPS start at boot, before anyone signs in, when
// that isn't set up yet. Turning lingering on needs an administrator, so LPS
// can only show the command.
func (a SystemdAutostart) Note() string {
	if a.User == "" || a.LingerDir == "" {
		return ""
	}
	if _, err := os.Stat(filepath.Join(a.LingerDir, a.User)); err == nil {
		return "" // already starts at boot
	}
	return "To also start LPS when this computer boots, before anyone signs in, run: sudo loginctl enable-linger " + a.User
}

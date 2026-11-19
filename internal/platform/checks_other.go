//go:build !windows

package platform

import (
	"context"
	"os/exec"

	"golang.org/x/sys/unix"
)

// DockerDesktopPath is Windows-only.
func DockerDesktopPath() string { return "" }

// StartDocker is only automated on Windows.
func StartDocker() error { return ErrUnsupported }

func osChecks(context.Context) []Check {
	c := Check{ID: "docker-installed", Label: "Docker installed", Required: true}
	if _, err := exec.LookPath("docker"); err == nil {
		c.Status, c.Detail = StatusOK, "Docker CLI found"
	} else {
		c.Status, c.Detail = StatusFail, DockerHint()
		c.Fixes = []Fix{{FixLink, "Get Docker", "https://docs.docker.com/engine/install/"}}
	}
	return []Check{c}
}

// FreeBytes returns free space available to unprivileged users on dir's filesystem.
func FreeBytes(dir string) (uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return st.Bavail * uint64(st.Bsize), nil
}

type noAutostart struct{}

func (noAutostart) Supported() bool        { return false }
func (noAutostart) Enabled() (bool, error) { return false, nil }
func (noAutostart) Set(bool) error         { return ErrUnsupported }

// NewAutostart returns an Autostart; not yet implemented outside Windows.
func NewAutostart() Autostart { return noAutostart{} }

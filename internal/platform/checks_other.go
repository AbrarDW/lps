//go:build !windows

package platform

import (
	"context"
	"os/exec"
	"runtime"

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
		url := "https://docs.docker.com/engine/install/"
		if runtime.GOOS == "darwin" {
			url = DockerDownloadURL
		}
		c.Fixes = []Fix{{FixLink, "Get Docker", url}}
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

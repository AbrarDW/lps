//go:build !windows

package platform

import (
	"errors"
	"os/exec"
)

// HideConsole is only needed on Windows; elsewhere child processes never get windows.
func HideConsole(*exec.Cmd) {}

// CanSelfUpdate is false outside Windows: there, update by downloading the
// new release (or with your package manager).
const CanSelfUpdate = false

// StartInstaller is only supported on Windows.
func StartInstaller(string) error {
	return errors.New("installing updates is only supported on Windows")
}

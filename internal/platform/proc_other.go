//go:build !windows

package platform

import "os/exec"

// HideConsole is only needed on Windows; elsewhere child processes never get windows.
func HideConsole(*exec.Cmd) {}

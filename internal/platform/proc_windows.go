//go:build windows

package platform

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// HideConsole makes a console program (tailscale.exe, wsl.exe, ...) run
// without a window. lps.exe is a GUI program with no console of its own, so
// without CREATE_NO_WINDOW Windows opens a new console window for every such
// child, which flashes on screen and steals focus from the browser.
func HideConsole(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NO_WINDOW
}

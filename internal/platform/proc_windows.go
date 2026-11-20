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

// CanSelfUpdate reports whether this build can install updates itself
// (Windows: by running the new LPS-Setup.exe silently).
const CanSelfUpdate = true

// StartInstaller starts the downloaded installer silently and detached, so it
// keeps running after LPS quits. /RELAUNCH=1 makes it start LPS again when done.
func StartInstaller(path string) error {
	cmd := exec.Command(path, "/VERYSILENT", "/SUPPRESSMSGBOXES", "/NORESTART", "/RELAUNCH=1")
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS,
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

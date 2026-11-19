//go:build windows

package platform

import (
	"os/exec"
	"testing"

	"golang.org/x/sys/windows"
)

func TestHideConsole(t *testing.T) {
	cmd := exec.Command("wsl.exe", "--status")
	HideConsole(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.HideWindow {
		t.Fatal("HideWindow not set")
	}
	if cmd.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 {
		t.Fatal("CREATE_NO_WINDOW not set: console tools would flash a window from lps.exe")
	}
}

func TestHideConsoleRunsHidden(t *testing.T) {
	// A real console program must still run and return output with the flags set.
	cmd := exec.Command("cmd.exe", "/c", "echo", "ok")
	HideConsole(cmd)
	out, err := cmd.Output()
	if err != nil || len(out) == 0 {
		t.Fatalf("hidden command failed: %v %q", err, out)
	}
}

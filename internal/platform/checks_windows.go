//go:build windows

package platform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// DockerDesktopPath returns the Docker Desktop executable, or "" if not installed.
func DockerDesktopPath() string {
	for _, env := range []string{"ProgramFiles", "ProgramW6432"} {
		if d := os.Getenv(env); d != "" {
			p := filepath.Join(d, "Docker", "Docker", "Docker Desktop.exe")
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}

// StartDocker launches Docker Desktop.
func StartDocker() error {
	p := DockerDesktopPath()
	if p == "" {
		return fmt.Errorf("Docker Desktop is not installed")
	}
	cmd := exec.Command(p)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

func osChecks(ctx context.Context) []Check {
	var out []Check

	// WSL2 needs Windows 10 version 2004 (build 19041) or newer.
	v := windows.RtlGetVersion()
	win := Check{ID: "windows", Label: "Windows version", Detail: fmt.Sprintf("Windows %d.%d build %d", v.MajorVersion, v.MinorVersion, v.BuildNumber)}
	if v.MajorVersion > 10 || (v.MajorVersion == 10 && v.BuildNumber >= 19041) {
		win.Status = StatusOK
	} else {
		win.Status, win.Detail = StatusFail, win.Detail+". Windows 10 version 2004 or newer is needed for WSL2."
		win.Fixes = []Fix{{FixLink, "Update Windows", "ms-settings:windowsupdate"}}
	}
	out = append(out, win)

	wsl := Check{ID: "wsl", Label: "WSL2 (runs the Linux VPSes)", Required: true}
	wctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := exec.CommandContext(wctx, "wsl.exe", "--status")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Run(); err == nil {
		wsl.Status, wsl.Detail = StatusOK, "Installed"
	} else {
		wsl.Status = StatusFail
		wsl.Detail = "Not installed. Open PowerShell as administrator, run the command below, then restart your PC."
		wsl.Fixes = []Fix{
			{FixCommand, "Install WSL", "wsl --install"},
			{FixLink, "Instructions", "https://learn.microsoft.com/windows/wsl/install"},
		}
	}
	out = append(out, wsl)

	dd := Check{ID: "docker-installed", Label: "Docker Desktop installed", Required: true}
	if DockerDesktopPath() != "" {
		dd.Status, dd.Detail = StatusOK, "Installed"
	} else if _, err := exec.LookPath("docker"); err == nil {
		dd.Status, dd.Detail = StatusOK, "Docker CLI found"
	} else {
		dd.Status, dd.Detail = StatusFail, "Install Docker Desktop with the WSL2 backend (the default), then start it."
		dd.Fixes = []Fix{{FixLink, "Download Docker Desktop", DockerDownloadURL}}
	}
	return append(out, dd)
}

// FreeBytes returns free space available to the user on dir's drive.
func FreeBytes(dir string) (uint64, error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, err
	}
	var free, total, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &free, &total, &totalFree); err != nil {
		return 0, err
	}
	return free, nil
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// WindowsAutostart manages the HKCU Run entry that starts lps.exe at sign-in.
type WindowsAutostart struct{ Exe string }

// NewAutostart returns an Autostart for the lps.exe next to the running binary.
func NewAutostart() Autostart {
	self, err := os.Executable()
	if err != nil {
		return WindowsAutostart{}
	}
	exe := filepath.Join(filepath.Dir(self), "lps.exe")
	if _, err := os.Stat(exe); err != nil {
		return WindowsAutostart{} // only the installed desktop app can autostart
	}
	return WindowsAutostart{Exe: exe}
}

// Supported implements Autostart.
func (a WindowsAutostart) Supported() bool { return a.Exe != "" }

// Enabled implements Autostart.
func (a WindowsAutostart) Enabled() (bool, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		return false, err
	}
	defer k.Close()
	_, _, err = k.GetStringValue("LPS")
	if err == registry.ErrNotExist {
		return false, nil
	}
	return err == nil, err
}

// Set implements Autostart.
func (a WindowsAutostart) Set(on bool) error {
	if !a.Supported() {
		return ErrUnsupported
	}
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if on {
		return k.SetStringValue("LPS", `"`+a.Exe+`"`)
	}
	if err := k.DeleteValue("LPS"); err != nil && err != registry.ErrNotExist {
		return err
	}
	return nil
}

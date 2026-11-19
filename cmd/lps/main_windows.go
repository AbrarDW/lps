//go:build windows

package main

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"os/exec"

	"fyne.io/systray"
	"golang.org/x/sys/windows"

	"github.com/abrardw/lps/internal/app"
	"github.com/abrardw/lps/internal/platform"
)

//go:embed lps.ico
var icon []byte

func openBrowser(url string) {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	platform.HideConsole(cmd)
	_ = cmd.Start()
}

func messageBox(title, text string, flags uint32) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	windows.MessageBox(0, m, t, flags)
}

func main() {
	dataDir := platform.DefaultDataDir()
	logw, closeLog := openLog(dataDir)
	defer closeLog()
	logger := newLogger(logw)
	url := "http://" + app.DefaultAddr

	defer func() {
		if r := recover(); r != nil {
			logger.Error("panic", "err", r)
			messageBox("LPS", fmt.Sprintf("LPS crashed: %v\n\nSee %s\\lps.log", r, dataDir), windows.MB_ICONERROR)
		}
	}()

	// Already running (e.g. started at sign-in): just show the dashboard.
	if app.Healthy(app.DefaultAddr) {
		openBrowser(url)
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- app.Run(ctx, app.Options{
			DataDir: dataDir, Version: version, Logger: logger,
			OnReady: func(u string) { url = u; openBrowser(u) },
		})
	}()

	onReady := func() {
		systray.SetIcon(icon)
		systray.SetTitle("LPS")
		systray.SetTooltip("LPS - Local Personal Server")
		mOpen := systray.AddMenuItem("Open dashboard", "Open LPS in your browser")
		systray.AddSeparator()
		auto := platform.NewAutostart()
		mAuto := systray.AddMenuItemCheckbox("Start with Windows", "Start LPS when you sign in", false)
		if !auto.Supported() {
			mAuto.Disable()
		} else if on, _ := auto.Enabled(); on {
			mAuto.Check()
		}
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("Quit LPS", "Stop LPS (VPSes and apps keep running in Docker)")
		systray.SetOnTapped(func() { openBrowser(url) }) // left click

		go func() {
			for {
				select {
				case <-mOpen.ClickedCh:
					openBrowser(url)
				case <-mAuto.ClickedCh:
					want := !mAuto.Checked()
					if err := auto.Set(want); err != nil {
						logger.Error("autostart", "err", err)
						continue
					}
					if want {
						mAuto.Check()
					} else {
						mAuto.Uncheck()
					}
				case <-mQuit.ClickedCh:
					systray.Quit()
					return
				case err := <-serverDone:
					if err != nil {
						logger.Error("server stopped", "err", err)
						messageBox("LPS could not start", err.Error()+"\n\nDetails: "+dataDir+"\\lps.log", windows.MB_ICONERROR)
					}
					systray.Quit()
					return
				}
			}
		}()
	}
	onExit := func() {
		cancel()
		select {
		case <-serverDone:
		default:
		}
	}
	systray.Run(onReady, onExit)
	cancel()
	os.Exit(0)
}

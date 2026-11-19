//go:build !windows

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/abrardw/lps/internal/app"
	"github.com/abrardw/lps/internal/platform"
)

func openBrowser(url string) {
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	_ = exec.Command(name, url).Start()
}

// main runs LPS and opens the dashboard. The tray icon is Windows-only for
// now; on Linux/macOS stop it with Ctrl+C.
func main() {
	if app.Healthy(app.DefaultAddr) {
		openBrowser("http://" + app.DefaultAddr)
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	err := app.Run(ctx, app.Options{
		DataDir: platform.DefaultDataDir(), Version: version, Logger: newLogger(os.Stderr),
		OnReady: openBrowser,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "lps:", err)
		os.Exit(1)
	}
}

// Command lps is the LPS desktop launcher: it runs the LPS server in the
// background, opens the dashboard in the browser and (on Windows) shows a
// tray icon. Use lpsd for the command-line server.
package main

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
)

var version = "dev" // set with -ldflags "-X main.version=..."

const maxLogSize = 5 << 20

// openLog returns the launcher's log file in dataDir, rotating it when large.
// GUI programs have no console, so this is where errors end up.
func openLog(dataDir string) (io.Writer, func()) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return io.Discard, func() {}
	}
	path := filepath.Join(dataDir, "lps.log")
	if st, err := os.Stat(path); err == nil && st.Size() > maxLogSize {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return io.Discard, func() {}
	}
	return f, func() { f.Close() }
}

func newLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

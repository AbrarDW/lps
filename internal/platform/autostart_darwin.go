//go:build darwin

package platform

import (
	"bytes"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// LaunchAgentLabel names LPS's launchd agent.
const LaunchAgentLabel = "com.abrardw.lps"

// MacBinary is where the LPS .pkg installs lpsd.
const MacBinary = "/usr/local/bin/lpsd"

// LaunchAgent starts LPS at sign-in through a per-user launchd agent in
// ~/Library/LaunchAgents, so it runs as the user: it can read their project
// folders and reach their Docker socket.
type LaunchAgent struct {
	Bin  string // lpsd; "" when LPS isn't installed from the .pkg
	Home string
}

// NewAutostart returns the LaunchAgent for the lpsd installed by the .pkg.
func NewAutostart() Autostart {
	a := LaunchAgent{}
	a.Home, _ = os.UserHomeDir()
	if st, err := os.Stat(MacBinary); err == nil && !st.IsDir() {
		a.Bin = MacBinary
	}
	return a
}

// Path is the agent's plist.
func (a LaunchAgent) Path() string {
	return filepath.Join(a.Home, "Library", "LaunchAgents", LaunchAgentLabel+".plist")
}

// LogPath is where launchd writes lpsd's output.
func (a LaunchAgent) LogPath() string {
	return filepath.Join(a.Home, "Library", "Logs", "LPS", "lpsd.log")
}

// Supported implements Autostart.
func (a LaunchAgent) Supported() bool { return a.Bin != "" && a.Home != "" }

// Enabled implements Autostart.
func (a LaunchAgent) Enabled() (bool, error) {
	if !a.Supported() {
		return false, nil
	}
	_, err := os.Stat(a.Path())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return err == nil, err
}

// Set implements Autostart. It only writes or removes the agent, which
// launchd reads at the next sign-in; the running LPS is left alone.
func (a LaunchAgent) Set(on bool) error {
	if !a.Supported() {
		return ErrUnsupported
	}
	if !on {
		if err := os.Remove(a.Path()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(a.LogPath()), 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(a.Path()), 0o755); err != nil {
		return err
	}
	tmp := a.Path() + ".tmp"
	if err := os.WriteFile(tmp, a.Plist(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, a.Path())
}

// Label names the switch in the dashboard.
func (a LaunchAgent) Label() string { return "Start LPS when I sign in" }

// Note implements the dashboard's hint; macOS needs none.
func (a LaunchAgent) Note() string { return "" }

// Plist is the agent definition. launchd gives agents a bare PATH, so it adds
// where Homebrew, Docker Desktop and Tailscale put their command-line tools.
func (a LaunchAgent) Plist() []byte {
	path := strings.Join([]string{
		"/usr/local/bin", "/opt/homebrew/bin", filepath.Join(a.Home, ".docker", "bin"),
		"/Applications/Docker.app/Contents/Resources/bin", "/usr/bin", "/bin", "/usr/sbin", "/sbin",
	}, ":")
	esc := func(s string) string {
		var b bytes.Buffer
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + LaunchAgentLabel + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + esc(a.Bin) + `</string>
		<string>serve</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<dict>
		<key>SuccessfulExit</key>
		<false/>
	</dict>
	<key>ThrottleInterval</key>
	<integer>10</integer>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>` + esc(path) + `</string>
	</dict>
	<key>StandardOutPath</key>
	<string>` + esc(a.LogPath()) + `</string>
	<key>StandardErrorPath</key>
	<string>` + esc(a.LogPath()) + `</string>
</dict>
</plist>
`)
	return []byte(b.String())
}

// Package platform contains OS-specific defaults (data directories, etc.).
package platform

import (
	"os"
	"path/filepath"
	"runtime"
)

// DefaultDataDir returns where lpsd keeps its database and state.
//
//	Windows: %ProgramData%\LPS  (falls back to %LOCALAPPDATA%\LPS)
//	macOS:   ~/Library/Application Support/LPS
//	Linux:   $XDG_DATA_HOME/lps or ~/.local/share/lps
func DefaultDataDir() string {
	switch runtime.GOOS {
	case "windows":
		if d := os.Getenv("ProgramData"); d != "" {
			return filepath.Join(d, "LPS")
		}
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "LPS")
		}
	case "darwin":
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, "Library", "Application Support", "LPS")
		}
	default:
		if d := os.Getenv("XDG_DATA_HOME"); d != "" {
			return filepath.Join(d, "lps")
		}
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, ".local", "share", "lps")
		}
	}
	return "lps-data"
}

// DockerHint returns OS-specific advice when the Docker engine is unreachable.
func DockerHint() string {
	switch runtime.GOOS {
	case "windows":
		return "Install Docker Desktop with the WSL2 backend (it runs the Linux VPS containers), then start it."
	case "darwin":
		return "Install and start Docker Desktop (or Colima/OrbStack)."
	default:
		return "Install Docker Engine and make sure your user can access /var/run/docker.sock."
	}
}

package docker

import (
	"os"
	"path/filepath"
	"runtime"
)

// defaultSocket finds the Docker socket when DOCKER_HOST isn't set. LPS often
// runs as a service (launchd, systemd) that doesn't see the shell's
// environment or Docker CLI contexts, and on macOS the engine's socket depends
// on which app provides it. It returns "" to keep the client's default
// (/var/run/docker.sock, or the named pipe on Windows).
func defaultSocket() string {
	if os.Getenv("DOCKER_HOST") != "" {
		return ""
	}
	home, _ := os.UserHomeDir()
	return findSocket(runtime.GOOS, home, os.Getenv("XDG_RUNTIME_DIR"), func(p string) bool {
		st, err := os.Stat(p)
		return err == nil && st.Mode()&os.ModeSocket != 0
	})
}

func findSocket(goos, home, runtimeDir string, isSocket func(string) bool) string {
	if goos == "windows" || isSocket("/var/run/docker.sock") {
		return ""
	}
	var candidates []string
	switch goos {
	case "darwin":
		if home != "" {
			candidates = append(candidates,
				filepath.Join(home, ".docker", "run", "docker.sock"),                                     // Docker Desktop
				filepath.Join(home, ".orbstack", "run", "docker.sock"),                                   // OrbStack
				filepath.Join(home, ".colima", "default", "docker.sock"),                                 // Colima
				filepath.Join(home, ".colima", "docker.sock"),                                            // older Colima
				filepath.Join(home, ".rd", "docker.sock"),                                                // Rancher Desktop
				filepath.Join(home, ".local", "share", "containers", "podman", "machine", "podman.sock"), // Podman
			)
		}
	default:
		if runtimeDir != "" {
			candidates = append(candidates, filepath.Join(runtimeDir, "docker.sock")) // rootless Docker
		}
		if home != "" {
			candidates = append(candidates, filepath.Join(home, ".docker", "desktop", "docker.sock")) // Docker Desktop for Linux
		}
	}
	for _, c := range candidates {
		if isSocket(c) {
			return "unix://" + c
		}
	}
	return ""
}

package platform

import (
	"context"
	"errors"
	"fmt"
)

// Check statuses.
const (
	StatusOK   = "ok"
	StatusWarn = "warn"
	StatusFail = "fail"
)

// Fix kinds.
const (
	FixLink    = "link"    // open Value (a URL)
	FixCommand = "command" // show Value for the user to copy and run
	FixAction  = "action"  // POST /api/setup/actions/<Value>
)

// Fix tells the user (or the UI) how to resolve a failed check.
type Fix struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Value string `json:"value"`
}

// Check is one item on the setup checklist.
type Check struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Status   string `json:"status"`
	Detail   string `json:"detail,omitempty"`
	Required bool   `json:"required"`
	Fixes    []Fix  `json:"fixes,omitempty"`
}

// ErrUnsupported is returned for features not available on this OS.
var ErrUnsupported = errors.New("not supported on this operating system")

// DockerDownloadURL is where users get Docker Desktop.
const DockerDownloadURL = "https://www.docker.com/products/docker-desktop/"

// SystemChecks returns the OS-specific checks (Windows version, WSL2,
// Docker installed, disk space). Docker-running and Tailscale checks are
// added by the API, which already has those clients.
func SystemChecks(ctx context.Context, dataDir string) []Check {
	checks := osChecks(ctx)
	return append(checks, DiskCheck(dataDir))
}

// DiskCheck warns when the data drive is low on space (images and VPS disks live there too).
func DiskCheck(dir string) Check {
	c := Check{ID: "disk", Label: "Free disk space"}
	free, err := FreeBytes(dir)
	if err != nil {
		c.Status, c.Detail = StatusWarn, "Could not read free space: "+err.Error()
		return c
	}
	gb := float64(free) / (1 << 30)
	c.Detail = fmt.Sprintf("%.1f GB free", gb)
	switch {
	case gb < 5:
		c.Status, c.Detail = StatusFail, c.Detail+". Free up space: Docker images and VPS disks need room."
	case gb < 20:
		c.Status, c.Detail = StatusWarn, c.Detail+". 20 GB or more is recommended."
	default:
		c.Status = StatusOK
	}
	return c
}

// Autostart starts LPS when the user signs in.
type Autostart interface {
	Supported() bool
	Enabled() (bool, error)
	Set(on bool) error
}

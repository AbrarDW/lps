// Package docker drives the Docker Engine that runs LPS VPSes and apps.
package docker

import (
	"context"
	"io"
)

// Container states reported by Engine.State.
const (
	StateRunning = "running"
	StateStopped = "stopped"
	StateMissing = "missing"
)

// VPSSpec describes a Linux VPS container.
type VPSSpec struct {
	Name      string  // unique, used in container/volume names
	User      string  // SSH login user
	PublicKey string  // authorized_keys content (optional if Password set)
	Password  string  // optional; never stored by lpsd
	SSHPort   int     // host port mapped to container port 22
	CPUs      float64 // 0 = unlimited
	MemoryMB  int     // 0 = unlimited
}

// AppSpec describes an app container built from a project directory.
type AppSpec struct {
	Name          string
	Image         string
	ContainerPort int
	HostPort      int
	CPUs          float64
	MemoryMB      int
}

// Terminal is an interactive shell session (a TTY) inside a container.
type Terminal interface {
	io.ReadWriteCloser
	// Resize changes the TTY size.
	Resize(ctx context.Context, cols, rows uint) error
}

// Engine is the subset of container operations lpsd needs.
// It is an interface so the API can be tested without a Docker daemon.
type Engine interface {
	// Ping checks the daemon and returns its version.
	Ping(ctx context.Context) (string, error)
	// EnsureVPSImage builds the embedded Ubuntu VPS image if it is missing.
	EnsureVPSImage(ctx context.Context, log io.Writer) error
	// CreateVPS creates (but does not start) a VPS container and its home volume.
	CreateVPS(ctx context.Context, spec VPSSpec) (containerID string, err error)
	// BuildImage builds a tar build context into tag using the named
	// Dockerfile inside the context, streaming output to log.
	BuildImage(ctx context.Context, buildContext io.Reader, tag, dockerfile string, log io.Writer) error
	// CreateApp creates (but does not start) an app container.
	CreateApp(ctx context.Context, spec AppSpec) (containerID string, err error)
	Start(ctx context.Context, id string) error
	Stop(ctx context.Context, id string) error
	// Remove force-removes a container; a missing container is not an error.
	Remove(ctx context.Context, id string) error
	// RemoveVolume deletes a named volume; a missing volume is not an error.
	RemoveVolume(ctx context.Context, name string) error
	// State returns StateRunning, StateStopped or StateMissing.
	State(ctx context.Context, id string) (string, error)
	// OpenTerminal starts a login shell as user inside a running container.
	OpenTerminal(ctx context.Context, containerID, user string, cols, rows uint) (Terminal, error)
	// Logs returns the last tail lines of combined stdout/stderr.
	Logs(ctx context.Context, id string, tail int) (string, error)
}

// VPSVolumeName is the persistent /home volume for a VPS.
func VPSVolumeName(name string) string { return "lps-vps-" + name + "-home" }

// VPSContainerName is the container name for a VPS.
func VPSContainerName(name string) string { return "lps-vps-" + name }

// AppContainerName is the container name for an app.
func AppContainerName(name string) string { return "lps-app-" + name }

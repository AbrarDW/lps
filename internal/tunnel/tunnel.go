// Package tunnel exposes local LPS ports to remote devices through a
// pluggable provider (Tailscale today; Cloudflare later).
package tunnel

import (
	"context"
	"errors"
	"fmt"
)

// Exposure protocols.
const (
	ProtoHTTPS = "https" // provider terminates TLS and proxies HTTP to the local port
	ProtoTCP   = "tcp"   // raw TCP forwarding (used for SSH)
)

// Exposure is one remote port forwarded to a port on 127.0.0.1.
type Exposure struct {
	Proto     string `json:"proto"`
	Port      int    `json:"port"`      // port on the remote side (e.g. the tailnet name)
	LocalPort int    `json:"localPort"` // port on 127.0.0.1
	Public    bool   `json:"public"`    // reachable from the whole internet (Funnel)
}

func (e Exposure) String() string {
	vis := "private"
	if e.Public {
		vis = "public"
	}
	return fmt.Sprintf("%s:%d -> 127.0.0.1:%d (%s)", e.Proto, e.Port, e.LocalPort, vis)
}

// Status describes the provider on this machine.
type Status struct {
	Installed     bool   `json:"installed"`
	Running       bool   `json:"running"` // connected and logged in
	State         string `json:"state"`   // provider-specific, e.g. Tailscale BackendState
	DNSName       string `json:"dnsName"` // e.g. my-pc.tail1234.ts.net
	HostName      string `json:"hostName"`
	Tailnet       string `json:"tailnet"`
	HTTPSEnabled  bool   `json:"httpsEnabled"`
	FunnelEnabled bool   `json:"funnelEnabled"`
	FunnelPorts   []int  `json:"funnelPorts"`
}

// Provider forwards remote ports to local ones.
type Provider interface {
	Name() string
	Status(ctx context.Context) (Status, error)
	// Apply creates or updates an exposure.
	Apply(ctx context.Context, e Exposure) error
	// Remove deletes an exposure; removing a missing one is not an error.
	Remove(ctx context.Context, e Exposure) error
	// UsedPorts returns the remote ports currently configured on the
	// provider, including ones LPS did not create.
	UsedPorts(ctx context.Context) (map[int]bool, error)
}

// NeedsActionError means the user must do something (sign in, enable a
// feature in an admin console) before the provider can continue.
type NeedsActionError struct {
	Message string `json:"message"`
	URL     string `json:"url,omitempty"`
}

func (e *NeedsActionError) Error() string {
	if e.URL != "" {
		return e.Message + " (" + e.URL + ")"
	}
	return e.Message
}

// AsNeedsAction unwraps a NeedsActionError.
func AsNeedsAction(err error) (*NeedsActionError, bool) {
	var na *NeedsActionError
	ok := errors.As(err, &na)
	return na, ok
}

// ErrNotInstalled is returned when the provider's software is missing.
var ErrNotInstalled = errors.New("not installed")

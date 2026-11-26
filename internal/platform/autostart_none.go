//go:build !windows && !linux && !darwin

package platform

type noAutostart struct{}

func (noAutostart) Supported() bool        { return false }
func (noAutostart) Enabled() (bool, error) { return false, nil }
func (noAutostart) Set(bool) error         { return ErrUnsupported }

// NewAutostart returns an Autostart; not yet implemented on this OS.
func NewAutostart() Autostart { return noAutostart{} }

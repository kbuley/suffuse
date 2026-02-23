//go:build !darwin && !windows && !linux

package clip

// New returns a no-op backend suitable for unsupported platforms.
func New() Backend {
	return &headlessBackend{watchCh: make(chan struct{})}
}

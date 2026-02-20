//go:build !darwin && !windows && !linux

// Package clip provides a no-op clipboard backend for headless environments
// (containers, CI, etc.) where no display server is available.
package clip

import "go.klb.dev/suffuse/internal/message"

type headlessBackend struct {
	watchCh chan struct{}
}

// New returns a no-op backend suitable for headless containers.
func New() Backend {
	return &headlessBackend{
		watchCh: make(chan struct{}),
	}
}

func (b *headlessBackend) Name() string                  { return "headless (no-op)" }
func (b *headlessBackend) Read() ([]message.Item, error) { return nil, nil }
func (b *headlessBackend) Write(_ []message.Item) error  { return nil }
func (b *headlessBackend) Watch() <-chan struct{}         { return b.watchCh }
func (b *headlessBackend) Close()                        {}

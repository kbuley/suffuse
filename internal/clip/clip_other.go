//go:build !darwin && !windows && !linux

package clip

import pb "go.klb.dev/suffuse/gen/suffuse/v1"

type headlessBackend struct {
	watchCh chan struct{}
}

// New returns a no-op backend suitable for headless containers.
func New() Backend {
	return &headlessBackend{watchCh: make(chan struct{})}
}

func (b *headlessBackend) Name() string                         { return "headless (no-op)" }
func (b *headlessBackend) Read() ([]*pb.ClipboardItem, error)   { return nil, nil }
func (b *headlessBackend) Write(_ []*pb.ClipboardItem) error    { return nil }
func (b *headlessBackend) Watch() <-chan struct{}                { return b.watchCh }
func (b *headlessBackend) Close()                               {}

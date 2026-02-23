package clip

import pb "go.klb.dev/suffuse/gen/suffuse/v1"

// headlessBackend is a no-op clipboard backend for environments without a
// display server (headless Linux servers, containers, etc.).
// It never produces Watch events and silently discards writes.
type headlessBackend struct {
	watchCh chan struct{}
}

func (b *headlessBackend) Name() string                       { return "headless (no-op)" }
func (b *headlessBackend) Read() ([]*pb.ClipboardItem, error) { return nil, nil }
func (b *headlessBackend) Write(_ []*pb.ClipboardItem) error  { return nil }
func (b *headlessBackend) Watch() <-chan struct{}              { return b.watchCh }
func (b *headlessBackend) Close()                             {}

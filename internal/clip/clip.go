// Package clip provides a unified interface to the system clipboard across
// platforms. Build constraints select the appropriate implementation:
//
//	clip_darwin.go   — macOS via golang.design/x/clipboard + cgo changeCount
//	clip_windows.go  — Windows via golang.design/x/clipboard + AddClipboardFormatListener
//	clip_linux.go    — Linux via golang.design/x/clipboard, polling only
//	clip_other.go    — headless / container stub
package clip

import pb "go.klb.dev/suffuse/gen/suffuse/v1"

// Backend is the interface that all platform clipboard implementations satisfy.
type Backend interface {
	Name() string
	Read() ([]*pb.ClipboardItem, error)
	Write(items []*pb.ClipboardItem) error
	Watch() <-chan struct{}
	Close()
}

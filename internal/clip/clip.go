// Package clip provides a unified interface to the system clipboard across
// platforms. Build constraints select the appropriate implementation:
//
//	clip_darwin.go   — macOS via golang.design/x/clipboard + cgo changeCount
//	clip_windows.go  — Windows via golang.design/x/clipboard + AddClipboardFormatListener
//	clip_linux.go    — Linux via golang.design/x/clipboard, polling only
//	clip_other.go    — headless / container stub
package clip

import "go.klb.dev/suffuse/internal/message"

// ClipboardItem mirrors message.Item for clipboard backend use.
type ClipboardItem = message.Item

// Backend is the interface that all platform clipboard implementations satisfy.
type Backend interface {
	// Name returns a human-readable name for the backend.
	Name() string

	// Read returns the current clipboard contents as a slice of typed items.
	// Returns nil, nil if the clipboard is empty or contains only unsupported types.
	Read() ([]ClipboardItem, error)

	// Write sets the clipboard contents to the provided items.
	Write(items []ClipboardItem) error

	// Watch returns a channel that receives a signal whenever the clipboard
	// changes. The channel is never closed. On platforms without native change
	// notification (Linux X11/Wayland) this is implemented via polling.
	// The caller should call Read() when it receives from the channel.
	Watch() <-chan struct{}

	// Close releases any resources held by the backend.
	Close()
}

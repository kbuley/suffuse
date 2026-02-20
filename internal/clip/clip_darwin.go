//go:build darwin

package clip

// #cgo CFLAGS: -x objective-c
// #cgo LDFLAGS: -framework Cocoa
// #import <Cocoa/Cocoa.h>
//
// NSInteger suffuse_changeCount() {
//     return [[NSPasteboard generalPasteboard] changeCount];
// }
import "C"

import (
	"log/slog"
	"time"

	"golang.design/x/clipboard"

	"go.klb.dev/suffuse/internal/message"
)

const darwinPollInterval = 100 * time.Millisecond

func init() {
	if err := clipboard.Init(); err != nil {
		slog.Warn("clipboard init failed", "err", err)
	}
}

type darwinBackend struct {
	lastChange C.NSInteger
	watchCh    chan struct{}
	done       chan struct{}
}

// New returns the macOS clipboard backend.
func New() Backend {
	b := &darwinBackend{
		lastChange: C.suffuse_changeCount(),
		watchCh:    make(chan struct{}, 1),
		done:       make(chan struct{}),
	}
	go b.poll()
	return b
}

func (b *darwinBackend) Name() string { return "macOS NSPasteboard" }

func (b *darwinBackend) poll() {
	t := time.NewTicker(darwinPollInterval)
	defer t.Stop()
	for {
		select {
		case <-b.done:
			return
		case <-t.C:
			cc := C.suffuse_changeCount()
			if cc != b.lastChange {
				b.lastChange = cc
				select {
				case b.watchCh <- struct{}{}:
				default:
				}
			}
		}
	}
}

func (b *darwinBackend) Read() ([]message.Item, error) {
	var items []message.Item
	if text := clipboard.Read(clipboard.FmtText); text != nil {
		items = append(items, message.NewTextItem(string(text)))
	}
	if img := clipboard.Read(clipboard.FmtImage); img != nil {
		items = append(items, message.NewBinaryItem("image/png", img))
	}
	return items, nil
}

func (b *darwinBackend) Write(items []message.Item) error {
	for _, it := range items {
		data, err := it.Decode()
		if err != nil {
			continue
		}
		switch it.MIME {
		case "text/plain":
			clipboard.Write(clipboard.FmtText, data)
		case "image/png":
			clipboard.Write(clipboard.FmtImage, data)
		}
	}
	return nil
}

func (b *darwinBackend) Watch() <-chan struct{} { return b.watchCh }
func (b *darwinBackend) Close()                { close(b.done) }

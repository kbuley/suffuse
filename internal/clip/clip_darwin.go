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
	"fmt"
	"log/slog"
	"time"

	"golang.design/x/clipboard"

	pb "go.klb.dev/suffuse/gen/suffuse/v1"
)

const darwinPollInterval = 100 * time.Millisecond

type darwinBackend struct {
	lastChange C.NSInteger
	watchCh    chan struct{}
	done       chan struct{}
}

// New returns the macOS clipboard backend.
// clipboard.Init is called here rather than in init() so that CLI sub-commands
// (status, copy, paste) that never construct a Backend don't log spurious
// warnings on headless systems.
func New() Backend {
	if err := clipboard.Init(); err != nil {
		slog.Warn("clipboard init failed", "err", err)
	}
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

func (b *darwinBackend) Read() ([]*pb.ClipboardItem, error) {
	var items []*pb.ClipboardItem
	if text := clipboard.Read(clipboard.FmtText); text != nil {
		items = append(items, &pb.ClipboardItem{Mime: "text/plain", Data: text})
	}
	if img := clipboard.Read(clipboard.FmtImage); img != nil {
		items = append(items, &pb.ClipboardItem{Mime: "image/png", Data: img})
	}
	return items, nil
}

func (b *darwinBackend) Write(items []*pb.ClipboardItem) error {
	for _, it := range items {
		switch it.Mime {
		case "text/plain":
			clipboard.Write(clipboard.FmtText, it.Data)
		case "image/png":
			clipboard.Write(clipboard.FmtImage, it.Data)
		default:
			return fmt.Errorf("unsupported MIME type: %s", it.Mime)
		}
	}
	return nil
}

func (b *darwinBackend) Watch() <-chan struct{} { return b.watchCh }
func (b *darwinBackend) Close()                { close(b.done) }

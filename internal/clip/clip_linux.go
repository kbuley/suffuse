//go:build linux

package clip

import (
	"bytes"
	"fmt"
	"log/slog"
	"time"

	"golang.design/x/clipboard"

	pb "go.klb.dev/suffuse/gen/suffuse/v1"
)

const linuxPollInterval = 250 * time.Millisecond

type linuxBackend struct {
	watchCh  chan struct{}
	done     chan struct{}
	lastText []byte
	lastImg  []byte
}

// New returns the Linux clipboard backend, or a headless no-op backend if
// the display environment is unavailable (e.g. a headless server without X11
// or Wayland). clipboard.Init is called here rather than in init() so that
// CLI sub-commands (status, copy, paste) don't trigger the warning.
func New() Backend {
	if err := clipboard.Init(); err != nil {
		slog.Warn("clipboard unavailable, running headless", "err", err)
		return &headlessBackend{watchCh: make(chan struct{})}
	}
	b := &linuxBackend{
		watchCh: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	go b.poll()
	return b
}

func (b *linuxBackend) Name() string { return "Linux clipboard (poll)" }

func (b *linuxBackend) poll() {
	t := time.NewTicker(linuxPollInterval)
	defer t.Stop()
	for {
		select {
		case <-b.done:
			return
		case <-t.C:
			text := clipboard.Read(clipboard.FmtText)
			img := clipboard.Read(clipboard.FmtImage)
			if !bytes.Equal(text, b.lastText) || !bytes.Equal(img, b.lastImg) {
				b.lastText = text
				b.lastImg = img
				select {
				case b.watchCh <- struct{}{}:
				default:
				}
			}
		}
	}
}

func (b *linuxBackend) Read() ([]*pb.ClipboardItem, error) {
	var items []*pb.ClipboardItem
	if text := clipboard.Read(clipboard.FmtText); text != nil {
		items = append(items, &pb.ClipboardItem{Mime: "text/plain", Data: text})
	}
	if img := clipboard.Read(clipboard.FmtImage); img != nil {
		items = append(items, &pb.ClipboardItem{Mime: "image/png", Data: img})
	}
	return items, nil
}

func (b *linuxBackend) Write(items []*pb.ClipboardItem) error {
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

func (b *linuxBackend) Watch() <-chan struct{} { return b.watchCh }
func (b *linuxBackend) Close()                { close(b.done) }

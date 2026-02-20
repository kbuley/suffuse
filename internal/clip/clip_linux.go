//go:build linux

package clip

import (
	"bytes"
	"log/slog"
	"time"

	"golang.design/x/clipboard"

	"go.klb.dev/suffuse/internal/message"
)

const linuxPollInterval = 250 * time.Millisecond

func init() {
	if err := clipboard.Init(); err != nil {
		// Non-fatal: headless containers have no display server.
		slog.Warn("clipboard init failed (headless?)", "err", err)
	}
}

type linuxBackend struct {
	watchCh  chan struct{}
	done     chan struct{}
	lastText []byte
	lastImg  []byte
}

// New returns the Linux clipboard backend. Change detection is via polling
// since X11 provides no notification mechanism and Wayland support is deferred.
func New() Backend {
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

func (b *linuxBackend) Read() ([]message.Item, error) {
	var items []message.Item
	if text := clipboard.Read(clipboard.FmtText); text != nil {
		items = append(items, message.NewTextItem(string(text)))
	}
	if img := clipboard.Read(clipboard.FmtImage); img != nil {
		items = append(items, message.NewBinaryItem("image/png", img))
	}
	return items, nil
}

func (b *linuxBackend) Write(items []message.Item) error {
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

func (b *linuxBackend) Watch() <-chan struct{} { return b.watchCh }
func (b *linuxBackend) Close()                { close(b.done) }

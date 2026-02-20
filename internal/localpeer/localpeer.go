// Package localpeer implements the hub.Peer that owns the server's system clipboard.
package localpeer

import (
	"log/slog"
	"reflect"
	"sync"
	"time"

	"go.klb.dev/suffuse/internal/clip"
	"go.klb.dev/suffuse/internal/hub"
	"go.klb.dev/suffuse/internal/message"
)

const peerID = "local"

// Peer is the hub.Peer that owns the server-side clipboard.
type Peer struct {
	h       *hub.Hub
	backend clip.Backend
	sendCh  chan *message.Message

	mu        sync.RWMutex
	info      message.PeerInfo
	lastSeen  time.Time
	lastItems []message.Item
}

// New creates the local peer but does not start it.
func New(h *hub.Hub, backend clip.Backend, source string) *Peer {
	now := time.Now()
	return &Peer{
		h:       h,
		backend: backend,
		sendCh:  make(chan *message.Message, 64),
		info: message.PeerInfo{
			ID:          peerID,
			Source:      source,
			Addr:        "local",
			Role:        message.RoleBoth,
			Clipboard:   message.DefaultClipboard,
			ConnectedAt: now,
			LastSeen:    now,
		},
		lastSeen: now,
	}
}

func (p *Peer) ID() string { return peerID }

func (p *Peer) Info() message.PeerInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	info := p.info
	info.LastSeen = p.lastSeen
	return info
}

// Send implements hub.Peer — writes incoming clipboard updates to the local system clipboard.
func (p *Peer) Send(msg *message.Message) {
	select {
	case p.sendCh <- msg:
	default:
		slog.Warn("local peer send channel full, dropping")
	}
}

// Run registers with the hub and starts the watch + write loops.
// Blocks until the backend is closed; call in a goroutine.
func (p *Peer) Run() {
	p.h.Register(p)
	defer p.h.Unregister(p)

	slog.Info("local clipboard peer started", "backend", p.backend.Name())

	// Writer: apply incoming updates to the local clipboard.
	go func() {
		for msg := range p.sendCh {
			if msg.Type != message.TypeClipboard || len(msg.Items) == 0 {
				continue
			}
			p.mu.Lock()
			if reflect.DeepEqual(msg.Items, p.lastItems) {
				p.mu.Unlock()
				continue
			}
			p.lastItems = msg.Items
			p.lastSeen = time.Now()
			p.mu.Unlock()

			if err := p.backend.Write(msg.Items); err != nil {
				slog.Error("local clipboard write failed", "err", err)
			} else {
				slog.Debug("local clipboard updated",
					"source", msg.Source,
					"items", len(msg.Items),
				)
			}
		}
	}()

	// Watcher: publish local clipboard changes to the hub.
	for range p.backend.Watch() {
		items, err := p.backend.Read()
		if err != nil {
			slog.Error("local clipboard read failed", "err", err)
			continue
		}
		if len(items) == 0 {
			continue
		}

		p.mu.Lock()
		if reflect.DeepEqual(items, p.lastItems) {
			p.mu.Unlock()
			continue
		}
		p.lastItems = items
		p.lastSeen = time.Now()
		p.mu.Unlock()

		slog.Debug("local clipboard changed, publishing", "items", len(items))
		p.h.Publish(items, message.DefaultClipboard, peerID)
	}
}

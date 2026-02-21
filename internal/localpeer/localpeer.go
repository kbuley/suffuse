// Package localpeer implements the hub.Peer that owns the server's local system clipboard.
package localpeer

import (
	"log/slog"
	"reflect"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	pb "go.klb.dev/suffuse/gen/suffuse/v1"
	"go.klb.dev/suffuse/internal/clip"
	"go.klb.dev/suffuse/internal/hub"
)

const peerID = "local"

// Peer is the hub.Peer that owns the server-side clipboard.
type Peer struct {
	h       *hub.Hub
	backend clip.Backend
	source  string
	sendCh  chan hub.Event

	mu          sync.RWMutex
	lastItems   []*pb.ClipboardItem
	connectedAt time.Time
	lastSeen    time.Time
}

// New creates the local peer but does not start it.
func New(h *hub.Hub, backend clip.Backend, source string) *Peer {
	now := time.Now()
	return &Peer{
		h:           h,
		backend:     backend,
		source:      source,
		sendCh:      make(chan hub.Event, 64),
		connectedAt: now,
		lastSeen:    now,
	}
}

func (p *Peer) ID() string { return peerID }

func (p *Peer) Info() *pb.PeerInfo {
	p.mu.RLock()
	ls := p.lastSeen
	p.mu.RUnlock()
	return &pb.PeerInfo{
		Source:      p.source,
		Addr:        "local",
		Role:        "both",
		Clipboard:   hub.DefaultClipboard,
		ConnectedAt: timestamppb.New(p.connectedAt),
		LastSeen:    timestamppb.New(ls),
	}
}

// Send implements hub.Peer — queues incoming clipboard updates to write to the local system clipboard.
func (p *Peer) Send(ev hub.Event) {
	select {
	case p.sendCh <- ev:
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

	// Writer: apply incoming hub events to the local clipboard.
	go func() {
		for ev := range p.sendCh {
			if len(ev.Items) == 0 {
				continue
			}
			p.mu.Lock()
			same := reflect.DeepEqual(ev.Items, p.lastItems)
			p.mu.Unlock()
			if same {
				continue
			}
			if err := p.backend.Write(ev.Items); err != nil {
				slog.Error("local clipboard write failed", "err", err)
				continue
			}
			p.mu.Lock()
			p.lastItems = ev.Items
			p.lastSeen = time.Now()
			p.mu.Unlock()
			hub.LogItems("local clipboard updated", ev.Source, ev.Clipboard, ev.Items)
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
		same := reflect.DeepEqual(items, p.lastItems)
		if !same {
			p.lastItems = items
			p.lastSeen = time.Now()
		}
		p.mu.Unlock()
		if same {
			continue
		}
		hub.LogItems("local clipboard changed, publishing", p.source, hub.DefaultClipboard, items)
		p.h.Publish(items, hub.DefaultClipboard, peerID, p.source)
	}
}

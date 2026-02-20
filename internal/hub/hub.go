// Package hub implements the central clipboard broker.
package hub

import (
	"log/slog"
	"sync"
	"time"

	"go.klb.dev/suffuse/internal/message"
)

// Peer is anything that can receive messages from the hub.
type Peer interface {
	ID() string
	Info() message.PeerInfo
	Send(*message.Message)
}

// Hub routes clipboard updates between all registered peers.
type Hub struct {
	mu     sync.RWMutex
	peers  map[string]Peer
	latest map[string][]message.Item // clipboard name → latest items
}

// New returns an empty Hub.
func New() *Hub {
	return &Hub{
		peers:  make(map[string]Peer),
		latest: make(map[string][]message.Item),
	}
}

// Register adds a peer and immediately sends it the latest clipboard contents
// for its subscribed clipboard.
func (h *Hub) Register(p Peer) {
	h.mu.Lock()
	h.peers[p.ID()] = p
	info := p.Info()
	cb := info.Clipboard
	if cb == "" {
		cb = message.DefaultClipboard
	}
	latest := h.latest[cb]
	total := len(h.peers)
	h.mu.Unlock()

	slog.Info("peer registered",
		"peer", p.ID(),
		"source", info.Source,
		"clipboard", cb,
		"total", total,
	)

	if len(latest) > 0 {
		filtered := (&message.Message{Items: latest}).FilterItems(info.AcceptedTypes)
		if len(filtered) > 0 {
			p.Send(&message.Message{
				Type:      message.TypeClipboard,
				Source:    "hub",
				Clipboard: cb,
				Items:     filtered,
			})
		}
	}
}

// Unregister removes a peer from the hub.
func (h *Hub) Unregister(p Peer) {
	h.mu.Lock()
	delete(h.peers, p.ID())
	total := len(h.peers)
	h.mu.Unlock()

	slog.Info("peer unregistered",
		"peer", p.ID(),
		"source", p.Info().Source,
		"total", total,
	)
}

// Publish stores items as the latest clipboard and fans the message out to
// every peer on the same clipboard except the origin.
func (h *Hub) Publish(items []message.Item, clipboard, originID string) {
	if clipboard == "" {
		clipboard = message.DefaultClipboard
	}

	h.mu.Lock()
	h.latest[clipboard] = items

	type target struct {
		peer     Peer
		accepted []string
	}
	var targets []target
	for id, p := range h.peers {
		if id == originID {
			continue
		}
		info := p.Info()
		pCb := info.Clipboard
		if pCb == "" {
			pCb = message.DefaultClipboard
		}
		if pCb == clipboard {
			targets = append(targets, target{p, info.AcceptedTypes})
		}
	}
	h.mu.Unlock()

	for _, t := range targets {
		filtered := (&message.Message{Items: items}).FilterItems(t.accepted)
		if len(filtered) == 0 {
			continue
		}
		t.peer.Send(&message.Message{
			Type:      message.TypeClipboard,
			Source:    originID,
			Clipboard: clipboard,
			Items:     filtered,
		})
	}
}

// Peers returns a snapshot of all current peer metadata.
func (h *Hub) Peers() []message.PeerInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]message.PeerInfo, 0, len(h.peers))
	for _, p := range h.peers {
		out = append(out, p.Info())
	}
	return out
}

// UpdateLastSeen is a hook for implementations that centralise the timestamp.
func (h *Hub) UpdateLastSeen(_ string, _ time.Time) {}

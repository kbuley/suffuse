// Package hub implements the central clipboard broker.
// It is transport-agnostic: peers register, receive events via a channel,
// and publish items. The hub uses proto types from gen/suffuse/v1.
package hub

import (
	"log/slog"
	"sync"

	pb "go.klb.dev/suffuse/gen/suffuse/v1"
)

const DefaultClipboard = "default"

// Event is a clipboard update delivered to a peer.
type Event struct {
	Source    string
	Clipboard string
	Items     []*pb.ClipboardItem
}

// Peer is anything that can receive clipboard events from the hub.
type Peer interface {
	ID() string
	Info() *pb.PeerInfo
	// Send delivers an event to the peer. Must be non-blocking.
	Send(Event)
}

// Hub routes clipboard updates between all registered peers.
type Hub struct {
	mu           sync.RWMutex
	peers        map[string]Peer
	latest       map[string][]*pb.ClipboardItem // clipboard → latest items
	latestSource map[string]string              // clipboard → source name
}

// New returns an empty Hub.
func New() *Hub {
	return &Hub{
		peers:        make(map[string]Peer),
		latest:       make(map[string][]*pb.ClipboardItem),
		latestSource: make(map[string]string),
	}
}

// Register adds a peer and immediately delivers the latest clipboard contents
// for its subscribed clipboard.
func (h *Hub) Register(p Peer) {
	h.mu.Lock()
	h.peers[p.ID()] = p
	info := p.Info()
	cb := canonicalize(info.Clipboard)
	latest := h.latest[cb]
	src := h.latestSource[cb]
	total := len(h.peers)
	h.mu.Unlock()

	slog.Info("peer registered",
		"peer", p.ID(),
		"source", info.Source,
		"clipboard", cb,
		"total", total,
	)

	if len(latest) > 0 {
		filtered := filterItems(latest, info.AcceptedTypes)
		if len(filtered) > 0 {
			p.Send(Event{Source: src, Clipboard: cb, Items: filtered})
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

// Publish stores items as the latest clipboard and fans out to all peers on
// the same clipboard except the origin.
func (h *Hub) Publish(items []*pb.ClipboardItem, clipboardName, originID, source string) {
	cb := canonicalize(clipboardName)

	h.mu.Lock()
	h.latest[cb] = items
	h.latestSource[cb] = source

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
		if canonicalize(info.Clipboard) == cb {
			targets = append(targets, target{p, info.AcceptedTypes})
		}
	}
	h.mu.Unlock()

	for _, t := range targets {
		filtered := filterItems(items, t.accepted)
		if len(filtered) == 0 {
			continue
		}
		t.peer.Send(Event{Source: source, Clipboard: cb, Items: filtered})
	}
}

// Latest returns the most recent items and source for the named clipboard,
// optionally filtered by accepted MIME types.
func (h *Hub) Latest(clipboardName string, accept []string) ([]*pb.ClipboardItem, string) {
	cb := canonicalize(clipboardName)
	h.mu.RLock()
	defer h.mu.RUnlock()
	return filterItems(h.latest[cb], accept), h.latestSource[cb]
}

// Peers returns a snapshot of all current peer metadata.
func (h *Hub) Peers() []*pb.PeerInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*pb.PeerInfo, 0, len(h.peers))
	for _, p := range h.peers {
		out = append(out, p.Info())
	}
	return out
}

// canonicalize returns the effective clipboard name, defaulting to "default".
func canonicalize(s string) string {
	if s == "" {
		return DefaultClipboard
	}
	return s
}

// filterItems returns only items whose MIME type is in accepted.
// If accepted is empty all items are returned unchanged.
func filterItems(items []*pb.ClipboardItem, accepted []string) []*pb.ClipboardItem {
	if len(accepted) == 0 {
		return items
	}
	set := make(map[string]struct{}, len(accepted))
	for _, a := range accepted {
		set[a] = struct{}{}
	}
	var out []*pb.ClipboardItem
	for _, it := range items {
		if _, ok := set[it.Mime]; ok {
			out = append(out, it)
		}
	}
	return out
}

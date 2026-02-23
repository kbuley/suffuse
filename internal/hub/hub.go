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

// BroadcastPeer is an optional interface a Peer may implement to signal that
// it wants to receive events from all clipboards, not just the one reported
// in Info().Clipboard. The federation upstream peer implements this.
type BroadcastPeer interface {
	Peer
	Broadcast()
}

// ClipboardFilter describes what a set of peers needs from a single clipboard.
// An empty Accepts slice means all MIME types are accepted.
type ClipboardFilter struct {
	Clipboard string
	Accepts   []string
}

// PeerChangeListener is notified whenever the set of registered peers changes.
// filters contains one entry per distinct clipboard that has at least one
// local watcher, with Accepts being the union of accepted types for that
// clipboard. An empty Accepts on a filter means at least one peer accepts
// everything on that clipboard.
type PeerChangeListener interface {
	OnPeerChange(filters []ClipboardFilter)
}

// Hub routes clipboard updates between all registered peers.
type Hub struct {
	mu           sync.RWMutex
	peers        map[string]Peer
	latest       map[string][]*pb.ClipboardItem // clipboard → latest items
	latestSource map[string]string              // clipboard → source name

	listenerMu sync.RWMutex
	listener   PeerChangeListener
}

// New returns an empty Hub.
func New() *Hub {
	return &Hub{
		peers:        make(map[string]Peer),
		latest:       make(map[string][]*pb.ClipboardItem),
		latestSource: make(map[string]string),
	}
}

// SetPeerChangeListener registers a listener that is called whenever the peer
// set changes. Only one listener is supported; calling again replaces it.
func (h *Hub) SetPeerChangeListener(l PeerChangeListener) {
	h.listenerMu.Lock()
	h.listener = l
	h.listenerMu.Unlock()
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
	filters := h.clipboardFiltersLocked()
	h.mu.Unlock()

	slog.Info("peer registered",
		"peer", p.ID(),
		"source", info.Source,
		"clipboard", cb,
		"total", total,
	)

	h.notifyListener(filters)

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
	filters := h.clipboardFiltersLocked()
	h.mu.Unlock()

	slog.Info("peer unregistered",
		"peer", p.ID(),
		"source", p.Info().Source,
		"total", total,
	)

	h.notifyListener(filters)
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
		_, isBroadcast := p.(BroadcastPeer)
		if isBroadcast || canonicalize(info.Clipboard) == cb {
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

// clipboardFiltersLocked computes the current set of ClipboardFilters — one
// per distinct clipboard name across all registered peers. For each clipboard,
// Accepts is the union of AcceptedTypes across all peers on that clipboard; an
// empty Accepts means at least one peer accepts everything.
// Must be called with h.mu held.
func (h *Hub) clipboardFiltersLocked() []ClipboardFilter {
	// clipboard → set of accepted MIME types (nil sentinel = accepts all)
	type entry struct {
		accepts map[string]struct{}
		all     bool // true if any peer accepts everything
	}
	m := make(map[string]*entry)

	for _, p := range h.peers {
		// BroadcastPeers span all clipboards and should not influence the
		// per-clipboard filter calculation — they are the consumers of that
		// calculation, not inputs to it.
		if _, isBroadcast := p.(BroadcastPeer); isBroadcast {
			continue
		}
		info := p.Info()
		cb := canonicalize(info.Clipboard)
		e, ok := m[cb]
		if !ok {
			e = &entry{accepts: make(map[string]struct{})}
			m[cb] = e
		}
		if e.all {
			continue // already unbounded
		}
		if len(info.AcceptedTypes) == 0 {
			e.all = true
			e.accepts = nil
			continue
		}
		for _, t := range info.AcceptedTypes {
			e.accepts[t] = struct{}{}
		}
	}

	out := make([]ClipboardFilter, 0, len(m))
	for cb, e := range m {
		f := ClipboardFilter{Clipboard: cb}
		if !e.all {
			for t := range e.accepts {
				f.Accepts = append(f.Accepts, t)
			}
		}
		out = append(out, f)
	}
	return out
}

// notifyListener calls the registered PeerChangeListener if one is set.
func (h *Hub) notifyListener(filters []ClipboardFilter) {
	h.listenerMu.RLock()
	l := h.listener
	h.listenerMu.RUnlock()
	if l != nil {
		l.OnPeerChange(filters)
	}
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

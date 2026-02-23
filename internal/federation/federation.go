// Package federation manages the optional upstream connection that turns a
// standalone suffuse server into a federated node.
//
// When an upstream address is configured, the Upstream type:
//   - Registers itself with the local hub as a peer (using a fixed sentinel ID),
//     receiving locally-published clipboard events and forwarding them upstream.
//   - Maintains one Watch stream per distinct clipboard that local peers subscribe
//     to. Each stream uses the MIME accept-union for that clipboard so upstream
//     only sends what local consumers can handle.
//   - Implements hub.PeerChangeListener: when the per-clipboard filter set
//     changes (new clipboard watched, last watcher gone, MIME union changed),
//     streams are opened, closed, or resubscribed accordingly.
//   - Reconnects each stream independently with exponential back-off.
//
// Loop prevention: events received from upstream are published to the local hub
// with originID == upstreamOriginID. The Upstream peer is registered with the
// same ID, so the hub will not deliver those events back to us, breaking the
// forwarding loop.
package federation

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"slices"
	"sort"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "go.klb.dev/suffuse/gen/suffuse/v1"
	"go.klb.dev/suffuse/internal/hub"
	"go.klb.dev/suffuse/internal/tlsconf"
)

const (
	upstreamOriginID = "federation/upstream"
	reconnectDelay   = time.Second
	maxReconnect     = 30 * time.Second
)

// Config holds the configuration for the upstream federation connection.
type Config struct {
	// Addr is the upstream server address (host:port).
	Addr string
	// Token is the shared secret for the upstream server (may be empty).
	Token string
	// Source is the identifier sent to the upstream server.
	Source string
}

// clipboardFilter is a snapshot of what a single clipboard needs from upstream.
type clipboardFilter struct {
	accepts []string // sorted; nil/empty = all types
}

func (f clipboardFilter) equal(other clipboardFilter) bool {
	return slices.Equal(f.accepts, other.accepts)
}

// streamHandle manages one upstream Watch stream for one clipboard.
type streamHandle struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Upstream manages persistent federation streams to one upstream server.
// It implements hub.Peer (to receive local events for forwarding upstream)
// and hub.PeerChangeListener (to reconcile streams when local watchers change).
type Upstream struct {
	cfg    Config
	h      *hub.Hub
	conn   *grpc.ClientConn
	client pb.ClipboardServiceClient

	// sendCh receives local hub events destined for the upstream server.
	sendCh chan hub.Event

	// streamsMu guards streams and wantFilters.
	streamsMu   sync.Mutex
	streams     map[string]*streamHandle  // clipboard → active stream
	wantFilters map[string]clipboardFilter // clipboard → desired filter

	// State for UpstreamInfo reported via StatusResponse.
	stateMu     sync.RWMutex
	connectedAt map[string]time.Time // clipboard → connected time
	lastSeen    map[string]time.Time // clipboard → last event time
}

// New creates an Upstream, registers it with the hub, and returns it.
// Call Run in a goroutine to start the connection loops.
func New(cfg Config, h *hub.Hub) (*Upstream, error) {
	opts, err := dialOpts(cfg.Token, cfg.Source)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(cfg.Addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("federation dial %s: %w", cfg.Addr, err)
	}

	u := &Upstream{
		cfg:         cfg,
		h:           h,
		conn:        conn,
		client:      pb.NewClipboardServiceClient(conn),
		sendCh:      make(chan hub.Event, 64),
		streams:     make(map[string]*streamHandle),
		wantFilters: make(map[string]clipboardFilter),
		connectedAt: make(map[string]time.Time),
		lastSeen:    make(map[string]time.Time),
	}

	h.SetPeerChangeListener(u)
	h.Register(u)

	return u, nil
}

// ── hub.Peer implementation ───────────────────────────────────────────────────

func (u *Upstream) ID() string { return upstreamOriginID }

// Info reports the upstream peer. AcceptedTypes and Clipboard are left empty
// because this peer spans multiple clipboards — the hub sees it as accepting
// everything, which is correct: filtering happens per-stream upstream.
func (u *Upstream) Info() *pb.PeerInfo {
	u.stateMu.RLock()
	var oldest time.Time
	for _, t := range u.connectedAt {
		if oldest.IsZero() || t.Before(oldest) {
			oldest = t
		}
	}
	u.stateMu.RUnlock()

	var connectedAtTS *timestamppb.Timestamp
	if !oldest.IsZero() {
		connectedAtTS = timestamppb.New(oldest)
	}

	return &pb.PeerInfo{
		Source:      u.cfg.Source,
		Addr:        u.cfg.Addr,
		Role:        "upstream",
		Clipboard:   "", // spans all clipboards
		ConnectedAt: connectedAtTS,
	}
}

// Broadcast implements hub.BroadcastPeer, signalling that this peer wants
// events from all clipboards.
func (u *Upstream) Broadcast() {}

// Send receives a local hub event and queues it for forwarding upstream.
func (u *Upstream) Send(ev hub.Event) {
	select {
	case u.sendCh <- ev:
	default:
		slog.Warn("federation upstream send channel full, dropping",
			"source", ev.Source, "clipboard", ev.Clipboard)
	}
}

// ── hub.PeerChangeListener implementation ────────────────────────────────────

// OnPeerChange is called by the hub on every peer register/unregister.
// It reconciles the set of active upstream Watch streams against the new
// per-clipboard filter requirements.
func (u *Upstream) OnPeerChange(filters []hub.ClipboardFilter) {
	// Build the desired filter map from the hub's notification.
	want := make(map[string]clipboardFilter, len(filters))
	for _, f := range filters {
		accepts := slices.Clone(f.Accepts)
		sort.Strings(accepts)
		want[f.Clipboard] = clipboardFilter{accepts: accepts}
	}

	u.streamsMu.Lock()
	defer u.streamsMu.Unlock()

	// Stop streams for clipboards no longer needed.
	for cb, h := range u.streams {
		if _, needed := want[cb]; !needed {
			slog.Info("federation closing upstream stream", "clipboard", cb)
			h.cancel()
			<-h.done
			delete(u.streams, cb)
			u.stateMu.Lock()
			delete(u.connectedAt, cb)
			delete(u.lastSeen, cb)
			u.stateMu.Unlock()
		}
	}

	// Open or resubscribe streams for clipboards that need it.
	for cb, f := range want {
		current, active := u.wantFilters[cb]
		if active && current.equal(f) {
			continue // already correct
		}
		// Cancel existing stream if filter changed.
		if h, exists := u.streams[cb]; exists {
			slog.Info("federation resubscribing upstream stream",
				"clipboard", cb, "accepts", f.accepts)
			h.cancel()
			<-h.done
			delete(u.streams, cb)
		}
		u.wantFilters[cb] = f
		u.streams[cb] = u.startStream(cb, f)
	}

	// Remove wantFilters for clipboards that are no longer needed.
	for cb := range u.wantFilters {
		if _, needed := want[cb]; !needed {
			delete(u.wantFilters, cb)
		}
	}
}

// ── stream lifecycle ──────────────────────────────────────────────────────────

// startStream spawns a goroutine that maintains a Watch stream for cb with
// the given filter, reconnecting on error. Must be called with streamsMu held.
func (u *Upstream) startStream(cb string, f clipboardFilter) *streamHandle {
	ctx, cancel := context.WithCancel(context.Background())
	h := &streamHandle{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(h.done)
		u.streamLoop(ctx, cb, f)
	}()
	return h
}

// streamLoop runs a Watch stream for one clipboard, reconnecting with
// exponential back-off until ctx is cancelled.
func (u *Upstream) streamLoop(ctx context.Context, cb string, f clipboardFilter) {
	delay := reconnectDelay
	for {
		err := u.runStream(ctx, cb, f)
		if err == nil || errors.Is(err, context.Canceled) ||
			status.Code(err) == codes.Canceled {
			return
		}
		slog.Warn("federation upstream stream ended, reconnecting",
			"clipboard", cb, "err", err, "retry_in", delay)

		u.stateMu.Lock()
		delete(u.connectedAt, cb)
		u.stateMu.Unlock()

		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay < maxReconnect {
			delay *= 2
		}
	}
}

// runStream opens one Watch stream and runs until it errors or ctx is done.
func (u *Upstream) runStream(ctx context.Context, cb string, f clipboardFilter) error {
	stream, err := u.client.Watch(ctx, &pb.WatchRequest{
		Clipboard: cb,
		Accepts:   f.accepts,
	})
	if err != nil {
		return fmt.Errorf("watch: %w", err)
	}

	now := time.Now()
	u.stateMu.Lock()
	u.connectedAt[cb] = now
	u.stateMu.Unlock()

	slog.Info("federation upstream stream connected",
		"addr", u.cfg.Addr, "clipboard", cb, "accepts", f.accepts)

	var lastItems []*pb.ClipboardItem
	for {
		ev, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return fmt.Errorf("upstream closed stream")
			}
			return err
		}

		now := time.Now()
		u.stateMu.Lock()
		u.lastSeen[cb] = now
		u.stateMu.Unlock()

		if len(ev.Items) == 0 {
			continue
		}
		if reflect.DeepEqual(ev.Items, lastItems) {
			continue
		}
		lastItems = ev.Items

		hub.LogItems("federation received from upstream", ev.Source, ev.Clipboard, ev.Items)
		u.h.Publish(ev.Items, ev.Clipboard, upstreamOriginID, ev.Source)
	}
}

// ── Run (forward loop) ────────────────────────────────────────────────────────

// Run starts the local→upstream forward loop. It blocks until ctx is cancelled.
// Call in a goroutine alongside the hub. Watch streams are managed separately
// via OnPeerChange; this loop only handles Copy forwarding.
func (u *Upstream) Run(ctx context.Context) {
	defer func() {
		// Cancel all active streams on shutdown.
		u.streamsMu.Lock()
		for cb, h := range u.streams {
			h.cancel()
			<-h.done
			delete(u.streams, cb)
		}
		u.streamsMu.Unlock()
		u.conn.Close()
		u.h.Unregister(u)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-u.sendCh:
			hub.LogItems("federation forwarding to upstream", ev.Source, ev.Clipboard, ev.Items)
			_, err := u.client.Copy(ctx, &pb.CopyRequest{
				Source:    ev.Source,
				Clipboard: ev.Clipboard,
				Items:     ev.Items,
			})
			if err != nil && !errors.Is(ctx.Err(), context.Canceled) {
				slog.Warn("federation upstream copy failed", "err", err)
			}
		}
	}
}

// ── UpstreamInfo ──────────────────────────────────────────────────────────────

// UpstreamInfo returns a snapshot of the upstream connection state for use in
// StatusResponse.UpstreamInfo.
func (u *Upstream) UpstreamInfo() *pb.UpstreamInfo {
	u.stateMu.RLock()
	defer u.stateMu.RUnlock()

	// Report the oldest connectedAt across all active streams, and the
	// most recent lastSeen.
	var connectedAt time.Time
	var lastSeen time.Time
	for _, t := range u.connectedAt {
		if connectedAt.IsZero() || t.Before(connectedAt) {
			connectedAt = t
		}
	}
	for _, t := range u.lastSeen {
		if t.After(lastSeen) {
			lastSeen = t
		}
	}

	info := &pb.UpstreamInfo{
		Addr:   u.cfg.Addr,
		Source: u.cfg.Source,
	}
	if !connectedAt.IsZero() {
		info.ConnectedAt = timestamppb.New(connectedAt)
	}
	if !lastSeen.IsZero() {
		info.LastSeen = timestamppb.New(lastSeen)
	}
	return info
}

// ── dial helpers ──────────────────────────────────────────────────────────────

func dialOpts(token, source string) ([]grpc.DialOption, error) {
	passphrase := token
	if passphrase == "" {
		passphrase = tlsconf.DefaultPassphrase
	}
	clientCreds, err := tlsconf.ClientCredentials(passphrase)
	if err != nil {
		return nil, fmt.Errorf("federation TLS credentials: %w", err)
	}
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(clientCreds),
		// Keepalive: send HTTP/2 PINGs on idle connections so NAT gateways
		// don't silently drop Watch streams between federated servers.
		// PermitWithoutStream keeps the connection alive between stream
		// teardown and reconnect.
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                30 * time.Second,
			Timeout:             10 * time.Second,
			PermitWithoutStream: true,
		}),
	}
	if token != "" || source != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(&federationCreds{
			token:  token,
			source: source,
		}))
	}
	return opts, nil
}

type federationCreds struct {
	token  string
	source string
}

func (c *federationCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	md := make(map[string]string, 2)
	if c.token != "" {
		md["authorization"] = "Bearer " + c.token
	}
	if c.source != "" {
		md["x-suffuse-source"] = c.source
	}
	return md, nil
}

func (c *federationCreds) RequireTransportSecurity() bool { return true }

// Package grpcservice implements the ClipboardService gRPC server.
package grpcservice

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "go.klb.dev/suffuse/gen/suffuse/v1"
	"go.klb.dev/suffuse/internal/hub"
)

// Service implements pb.ClipboardServiceServer.
type Service struct {
	pb.UnimplementedClipboardServiceServer
	h     *hub.Hub
	token string // empty = no auth
}

// New returns a Service backed by h. token may be empty to disable auth.
func New(h *hub.Hub, token string) *Service {
	return &Service{h: h, token: token}
}

// Copy implements ClipboardService.Copy.
func (s *Service) Copy(ctx context.Context, req *pb.CopyRequest) (*pb.CopyResponse, error) {
	if err := s.auth(ctx); err != nil {
		return nil, err
	}
	if len(req.Items) == 0 {
		return &pb.CopyResponse{}, nil
	}
	src := sourceFromCtx(ctx, req.Source)
	cb := canonicalize(req.Clipboard)
	hub.LogItems("clipboard received", src, cb, req.Items)
	s.h.Publish(req.Items, cb, addrFromCtx(ctx), src)
	return &pb.CopyResponse{}, nil
}

// Paste implements ClipboardService.Paste.
func (s *Service) Paste(ctx context.Context, req *pb.PasteRequest) (*pb.PasteResponse, error) {
	if err := s.auth(ctx); err != nil {
		return nil, err
	}
	cb := canonicalize(req.Clipboard)
	items, src := s.h.Latest(cb, req.Accepts)
	return &pb.PasteResponse{
		Source:    src,
		Clipboard: cb,
		Items:     items,
	}, nil
}

// Watch implements ClipboardService.Watch.
func (s *Service) Watch(req *pb.WatchRequest, stream pb.ClipboardService_WatchServer) error {
	if err := s.auth(stream.Context()); err != nil {
		return err
	}

	addr := addrFromCtx(stream.Context())
	cb := canonicalize(req.Clipboard)
	id := addr + "/watch/" + cb

	wp := &watchPeer{
		id:           id,
		source:       sourceFromCtx(stream.Context(), ""),
		addr:         addr,
		clipboard:    cb,
		accept:       req.Accepts,
		metadataOnly: req.MetadataOnly,
		ch:           make(chan hub.Event, 16),
		connectedAt:  time.Now(),
	}

	s.h.Register(wp)
	defer s.h.Unregister(wp)

	slog.Info("watch started", "peer", id, "accept", req.Accepts, "metadata_only", req.MetadataOnly)

	for {
		select {
		case <-stream.Context().Done():
			return nil
		case ev := <-wp.ch:
			availTypes := make([]string, len(ev.Items))
			for i, it := range ev.Items {
				availTypes[i] = it.Mime
			}

			var items []*pb.ClipboardItem
			if !req.MetadataOnly {
				items = ev.Items
			}

			if err := stream.Send(&pb.WatchResponse{
				Source:         ev.Source,
				Clipboard:      ev.Clipboard,
				Items:          items,
				AvailableTypes: availTypes,
			}); err != nil {
				return err
			}
		}
	}
}

// Status implements ClipboardService.Status.
func (s *Service) Status(ctx context.Context, _ *pb.StatusRequest) (*pb.StatusResponse, error) {
	if err := s.auth(ctx); err != nil {
		return nil, err
	}
	return &pb.StatusResponse{Peers: s.h.Peers()}, nil
}

// auth validates the bearer token in ctx metadata. Skipped when s.token is empty.
func (s *Service) auth(ctx context.Context) error {
	if s.token == "" {
		return nil
	}
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return status.Error(codes.Unauthenticated, "missing metadata")
	}
	vals := md.Get("authorization")
	if len(vals) == 0 {
		return status.Error(codes.Unauthenticated, "missing authorization header")
	}
	const prefix = "Bearer "
	tok := vals[0]
	if len(tok) > len(prefix) && tok[:len(prefix)] == prefix {
		tok = tok[len(prefix):]
	}
	if tok != s.token {
		return status.Error(codes.Unauthenticated, "invalid token")
	}
	return nil
}

func sourceFromCtx(ctx context.Context, fallback string) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if vals := md.Get("x-suffuse-source"); len(vals) > 0 {
			return vals[0]
		}
	}
	if fallback != "" {
		return fallback
	}
	return addrFromCtx(ctx)
}

func addrFromCtx(ctx context.Context) string {
	if p, ok := peer.FromContext(ctx); ok {
		return p.Addr.String()
	}
	return "unknown"
}

func canonicalize(s string) string {
	if s == "" {
		return hub.DefaultClipboard
	}
	return s
}

// ── watchPeer ──────────────────────────────────────────────────────────────

// watchPeer is a transient hub.Peer backed by a Watch stream.
type watchPeer struct {
	id           string
	source       string
	addr         string
	clipboard    string
	accept       []string
	metadataOnly bool
	ch           chan hub.Event
	connectedAt  time.Time
	lastSeen     atomic.Int64
}

func (p *watchPeer) ID() string { return p.id }

func (p *watchPeer) Info() *pb.PeerInfo {
	ls := p.lastSeen.Load()
	var lastSeenTS *timestamppb.Timestamp
	if ls > 0 {
		lastSeenTS = timestamppb.New(time.Unix(0, ls))
	}
	return &pb.PeerInfo{
		Source:        p.source,
		Addr:          p.addr,
		Role:          "client",
		Clipboard:     p.clipboard,
		AcceptedTypes: p.accept,
		ConnectedAt:   timestamppb.New(p.connectedAt),
		LastSeen:      lastSeenTS,
	}
}

func (p *watchPeer) Send(ev hub.Event) {
	p.lastSeen.Store(time.Now().UnixNano())
	select {
	case p.ch <- ev:
	default:
		slog.Warn("watch peer channel full, dropping", "peer", p.id)
	}
}

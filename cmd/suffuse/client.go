package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "go.klb.dev/suffuse/gen/suffuse/v1"
	"go.klb.dev/suffuse/internal/clip"
	"go.klb.dev/suffuse/internal/hub"
	"go.klb.dev/suffuse/internal/ipc"
)

const (
	reconnectDelay = time.Second
	maxReconnect   = 30 * time.Second
)

// ── sessionState ──────────────────────────────────────────────────────────────

// sessionState holds metadata about the live upstream connection so that the
// IPC status handler can enrich responses without opening a second TCP hop.
type sessionState struct {
	mu          sync.RWMutex
	serverAddr  string
	source      string
	connectedAt time.Time
	connected   bool
	lastSeen    atomic.Int64 // UnixNano of last WatchResponse received
}

func newSessionState(serverAddr, source string) *sessionState {
	return &sessionState{serverAddr: serverAddr, source: source}
}

func (s *sessionState) markConnected() {
	s.mu.Lock()
	s.connected = true
	s.connectedAt = time.Now()
	s.mu.Unlock()
}

func (s *sessionState) markDisconnected() {
	s.mu.Lock()
	s.connected = false
	s.mu.Unlock()
}

func (s *sessionState) touch() {
	s.lastSeen.Store(time.Now().UnixNano())
}

func (s *sessionState) snapshot() (serverAddr string, connectedAt time.Time, lastSeen time.Time, connected bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ls := s.lastSeen.Load()
	var lsTime time.Time
	if ls > 0 {
		lsTime = time.Unix(0, ls)
	}
	return s.serverAddr, s.connectedAt, lsTime, s.connected
}

// ── ipcService ────────────────────────────────────────────────────────────────

// ipcService implements ClipboardServiceServer over the local Unix socket.
// CLI tools (copy/paste/status) connect here; requests are proxied upstream
// and Status responses are enriched with local connection metadata.
type ipcService struct {
	pb.UnimplementedClipboardServiceServer
	upstream pb.ClipboardServiceClient
	state    *sessionState
}

func (s *ipcService) Copy(ctx context.Context, req *pb.CopyRequest) (*pb.CopyResponse, error) {
	if s.upstream == nil {
		return nil, status.Error(codes.Unavailable, "not connected to server")
	}
	return s.upstream.Copy(ctx, req)
}

func (s *ipcService) Paste(ctx context.Context, req *pb.PasteRequest) (*pb.PasteResponse, error) {
	if s.upstream == nil {
		return nil, status.Error(codes.Unavailable, "not connected to server")
	}
	return s.upstream.Paste(ctx, req)
}

func (s *ipcService) Status(ctx context.Context, req *pb.StatusRequest) (*pb.StatusResponse, error) {
	if s.upstream == nil {
		return nil, status.Error(codes.Unavailable, "not connected to server")
	}
	resp, err := s.upstream.Status(ctx, req)
	if err != nil {
		return nil, err
	}
	serverAddr, connectedAt, lastSeen, _ := s.state.snapshot()
	resp.ClientInfo = &pb.ClientConnectionInfo{
		ServerAddr:  serverAddr,
		Source:      s.state.source,
		ConnectedAt: timestamppb.New(connectedAt),
		LastSeen:    timestamppb.New(lastSeen),
	}
	return resp, nil
}

// ── cobra command ─────────────────────────────────────────────────────────────

func newClientCmd() *cobra.Command {
	v := viper.New()

	cmd := &cobra.Command{
		Use:   "client",
		Short: "Connect to a suffuse server and sync the local clipboard",
		Long: `Connects to a suffuse server and keeps the local system clipboard
in sync with all other connected peers. Reconnects automatically on disconnect.

When running as a service, copy/paste/status CLI tools connect to the client
daemon via the local IPC socket rather than opening their own server connections.

Config file search order:
  /etc/suffuse/suffuse.toml
  $HOME/.config/suffuse/suffuse.toml
  path supplied via --config

Precedence (lowest → highest): defaults → config file → SUFFUSE_* env vars → flags`,
		Args:    cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error { return bindViper(cmd, v) },
		RunE:    func(_ *cobra.Command, _ []string) error { return runClient(v) },
	}

	f := cmd.Flags()
	f.String("server", "localhost:8752", "suffuse server address (host:port)")
	f.String("token", "", "shared secret (must match server)")
	f.String("source", defaultSource(), "identifier shown in server peer list")
	f.StringSlice("accept", nil, "MIME types to accept (empty = all); e.g. text/plain,image/png")
	addLoggingFlags(cmd)
	addConfigFlag(cmd)

	return cmd
}

func runClient(v *viper.Viper) error {
	setupLogging(v)

	serverAddr := v.GetString("server")
	token := v.GetString("token")
	source := v.GetString("source")
	accept := v.GetStringSlice("accept")

	slog.Info("suffuse client starting",
		"version", Version,
		"server", serverAddr,
		"source", source,
		"auth", token != "",
	)

	backend := clip.New()
	defer backend.Close()
	slog.Info("clipboard backend", "name", backend.Name())

	state := newSessionState(serverAddr, source)

	// Dial upstream (non-blocking — connection established lazily by gRPC).
	upstreamConn, err := grpc.NewClient(serverAddr, dialOpts(token, source)...)
	if err != nil {
		return fmt.Errorf("dial %s: %w", serverAddr, err)
	}
	defer upstreamConn.Close()
	upstreamClient := pb.NewClipboardServiceClient(upstreamConn)

	// Start local IPC server so CLI tools connect here instead of TCP.
	ipcSvc := &ipcService{upstream: upstreamClient, state: state}
	if ln, err := ipc.Listen(); err != nil {
		slog.Warn("IPC socket unavailable", "err", err)
	} else {
		slog.Info("IPC socket listening", "path", ipc.SocketPath())
		grpcSrv := grpc.NewServer()
		pb.RegisterClipboardServiceServer(grpcSrv, ipcSvc)
		go grpcSrv.Serve(ln) //nolint:errcheck
	}

	clientLoop(upstreamClient, backend, source, accept, state)
	return nil
}

// ── watch / copy loop ─────────────────────────────────────────────────────────

func clientLoop(
	client pb.ClipboardServiceClient,
	backend clip.Backend,
	source string,
	accept []string,
	state *sessionState,
) {
	var lastItems []*pb.ClipboardItem
	delay := reconnectDelay

	for {
		ctx, cancel := context.WithCancel(context.Background())

		// Local clipboard → server.
		go func() {
			defer cancel()
			for {
				select {
				case <-ctx.Done():
					return
				case <-backend.Watch():
					items, err := backend.Read()
					if err != nil || len(items) == 0 {
						continue
					}
					if reflect.DeepEqual(items, lastItems) {
						continue
					}
					lastItems = items
					hub.LogItems("local clipboard changed, sending", source, hub.DefaultClipboard, items)
					_, err = client.Copy(ctx, &pb.CopyRequest{
						Source:    source,
						Clipboard: hub.DefaultClipboard,
						Items:     items,
					})
					if err != nil && !errors.Is(ctx.Err(), context.Canceled) {
						slog.Warn("copy failed", "err", err)
					}
				}
			}
		}()

		err := watchStream(ctx, client, backend, &lastItems, accept, state)
		cancel()

		if err != nil {
			if status.Code(err) == codes.Canceled || errors.Is(err, context.Canceled) {
				return
			}
			slog.Warn("watch stream ended, reconnecting", "err", err, "retry_in", delay)
		}
		state.markDisconnected()
		time.Sleep(delay)
		if delay < maxReconnect {
			delay *= 2
		}
	}
}

func watchStream(
	ctx context.Context,
	client pb.ClipboardServiceClient,
	backend clip.Backend,
	lastItems *[]*pb.ClipboardItem,
	accept []string,
	state *sessionState,
) error {
	stream, err := client.Watch(ctx, &pb.WatchRequest{
		Clipboard: hub.DefaultClipboard,
		Accepts:   accept,
	})
	if err != nil {
		return err
	}
	state.markConnected()
	slog.Info("connected to server")

	for {
		ev, err := stream.Recv()
		if err != nil {
			if err == io.EOF {
				return fmt.Errorf("server closed stream")
			}
			return err
		}
		state.touch()
		if len(ev.Items) == 0 {
			continue
		}
		if reflect.DeepEqual(ev.Items, *lastItems) {
			continue
		}
		*lastItems = ev.Items
		hub.LogItems("clipboard received", ev.Source, ev.Clipboard, ev.Items)
		if err := backend.Write(ev.Items); err != nil {
			slog.Error("clipboard write failed", "err", err)
		}
	}
}

package main

import (
	"context"
	"fmt"
	"log/slog"
	"net"

	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/soheilhy/cmux"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"

	pb "go.klb.dev/suffuse/gen/suffuse/v1"
	"go.klb.dev/suffuse/internal/clip"
	"go.klb.dev/suffuse/internal/grpcservice"
	"go.klb.dev/suffuse/internal/hub"
	"go.klb.dev/suffuse/internal/ipc"
	"go.klb.dev/suffuse/internal/localpeer"
)

func newServerCmd() *cobra.Command {
	v := viper.New()

	cmd := &cobra.Command{
		Use:   "server",
		Short: "Run the clipboard hub (+ local clipboard integration)",
		Long: `Starts the suffuse hub. All connected clients share a clipboard.
The server also participates as a local clipboard peer by default.

Both gRPC and HTTP/JSON (grpc-gateway) are served on the same port.
JSON clients (e.g. a Neovim plugin) can use standard HTTP against the same address.

An IPC Unix socket is also opened so that local CLI tools (copy/paste/status)
connect without a TCP round-trip.

Config file search order:
  /etc/suffuse/suffuse.toml
  $HOME/.config/suffuse/suffuse.toml
  path supplied via --config

Precedence (lowest → highest): defaults → config file → SUFFUSE_* env vars → flags`,
		Args:    cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error { return bindViper(cmd, v) },
		RunE:    func(_ *cobra.Command, _ []string) error { return runServer(v) },
	}

	f := cmd.Flags()
	f.String("addr", "0.0.0.0:8752", "TCP listen address (serves both gRPC and HTTP/JSON)")
	f.String("token", "", "shared secret (empty = no auth)")
	f.Bool("no-local", false, "disable local clipboard integration (relay mode)")
	f.String("source", defaultSource(), "name for this host in peer lists")
	addLoggingFlags(cmd)
	addConfigFlag(cmd)

	return cmd
}

func runServer(v *viper.Viper) error {
	setupLogging(v)

	addr := v.GetString("addr")
	token := v.GetString("token")
	noLocal := v.GetBool("no-local")
	source := v.GetString("source")

	slog.Info("suffuse server starting",
		"version", Version,
		"addr", addr,
		"local_clip", !noLocal,
		"auth", token != "",
	)

	h := hub.New()
	svc := grpcservice.New(h, token)

	if !noLocal {
		backend := clip.New()
		lp := localpeer.New(h, backend, source)
		go lp.Run()
	}

	grpcSrv := grpc.NewServer()
	pb.RegisterClipboardServiceServer(grpcSrv, svc)
	reflection.Register(grpcSrv)

	// IPC socket: lets local CLI tools (copy/paste/status) avoid TCP.
	// The server's own ClipboardService is served directly — no enrichment
	// of StatusResponse.ClientInfo since there is no upstream connection.
	if ln, err := ipc.Listen(); err != nil {
		slog.Warn("IPC socket unavailable", "err", err)
	} else {
		slog.Info("IPC socket listening", "path", ipc.SocketPath())
		ipcSrv := grpc.NewServer()
		pb.RegisterClipboardServiceServer(ipcSrv, svc)
		go ipcSrv.Serve(ln) //nolint:errcheck
	}

	// HTTP/JSON gateway dials back to the gRPC server on the same address.
	gwMux := gwruntime.NewServeMux()
	gwCtx, gwCancel := context.WithCancel(context.Background())
	defer gwCancel()
	if err := pb.RegisterClipboardServiceHandlerFromEndpoint(
		gwCtx, gwMux, addr,
		[]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())},
	); err != nil {
		return fmt.Errorf("gateway registration: %w", err)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	slog.Info("listening", "addr", ln.Addr())

	// cmux routes gRPC (HTTP/2 + content-type: application/grpc) vs HTTP/1.1.
	m := cmux.New(ln)
	grpcL := m.MatchWithWriters(
		cmux.HTTP2MatchHeaderFieldSendSettings("content-type", "application/grpc"),
	)
	httpL := m.Match(cmux.Any())

	go grpcSrv.Serve(grpcL)           //nolint:errcheck
	go serveHTTPGateway(httpL, gwMux) //nolint:errcheck
	return m.Serve()
}

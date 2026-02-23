package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	gwruntime "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"

	pb "go.klb.dev/suffuse/gen/suffuse/v1"
	"go.klb.dev/suffuse/internal/clip"
	"go.klb.dev/suffuse/internal/federation"
	"go.klb.dev/suffuse/internal/grpcservice"
	"go.klb.dev/suffuse/internal/hub"
	"go.klb.dev/suffuse/internal/ipc"
	"go.klb.dev/suffuse/internal/localpeer"
	"go.klb.dev/suffuse/internal/tlsconf"
)

// keepalive timing constants.
const (
	kaTime    = 30 * time.Second
	kaTimeout = 10 * time.Second
	kaMinTime = 10 * time.Second
)

func newServerCmd() *cobra.Command {
	v := viper.New()

	cmd := &cobra.Command{
		Use:   "server",
		Short: "Run the clipboard hub (+ local clipboard integration)",
		Long: `Starts the suffuse hub. All connected clients share a clipboard.
The server also participates as a local clipboard peer by default.

Both gRPC and HTTP/JSON (grpc-gateway) are served on the same TCP port over
TLS. A Unix IPC socket is also opened for local CLI tools (copy/paste/status).

Transport security
  All TCP connections use TLS encrypted with a key derived from --token.
  The same token must be used on both sides or the TLS handshake will fail.
  If no token is set, the default passphrase "suffuse" is used — traffic is
  still encrypted, but any other suffuse instance with the default will connect.
  Set a custom token to restrict access to instances sharing that secret.

Federation
  Use --upstream to federate this server with another suffuse hub. Clipboard
  events flow both ways. The upstream accept filter stays in sync with local
  peer capabilities (e.g. text-only peers won't pull binary data from upstream).

Flags, environment variables, and config-file keys
  Flag                Env var                     Config key
  ───────────────────────────────────────────────────────────
  --addr              SUFFUSE_ADDR                addr
  --token             SUFFUSE_TOKEN               token
  --source            SUFFUSE_SOURCE              source
  --no-local          SUFFUSE_NO_LOCAL            no-local
  --upstream-host     SUFFUSE_UPSTREAM_HOST       upstream-host
  --upstream-port     SUFFUSE_UPSTREAM_PORT       upstream-port
  --upstream-token    SUFFUSE_UPSTREAM_TOKEN      upstream-token
  --upstream-source   SUFFUSE_UPSTREAM_SOURCE     upstream-source
  --log-level         SUFFUSE_LOG_LEVEL           log-level    (debug|info|warn|error)
  --log-format        SUFFUSE_LOG_FORMAT          log-format   (auto|text|json)
  --config            (flag only)

Config file search order (first found wins)
  /etc/suffuse/suffuse.toml
  $HOME/.config/suffuse/suffuse.toml
  path supplied via --config

Precedence: defaults → config file → SUFFUSE_* env vars → CLI flags`,
		Args:    cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error { return bindViper(cmd, v) },
		RunE:    func(_ *cobra.Command, _ []string) error { return runServer(v) },
	}

	f := cmd.Flags()
	f.String("addr", "0.0.0.0:8752", "TCP listen address (gRPC + HTTP/JSON, TLS)")
	f.String("token", "", `shared secret — used for TLS key derivation and per-RPC auth.
	If unset, defaults to "suffuse" for encryption (no per-RPC auth).`)
	f.Bool("no-local", false, "disable local clipboard integration (relay/hub-only mode)")
	f.String("source", defaultSource(), "name for this host shown in peer lists")
	f.String("upstream-host", "", "upstream suffuse server host (enables federation)")
	f.Int("upstream-port", 8752, "upstream suffuse server port")
	f.String("upstream-token", "", "shared secret for upstream server (defaults to --token)")
	f.String("upstream-source", "", "source name sent to upstream (defaults to --source)")
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
	upstreamHost := v.GetString("upstream-host")
	upstreamPort := v.GetInt("upstream-port")
	upstreamToken := v.GetString("upstream-token")
	upstreamSource := v.GetString("upstream-source")

	var upstreamAddr string
	if upstreamHost != "" {
		upstreamAddr = fmt.Sprintf("%s:%d", upstreamHost, upstreamPort)
	}

	if upstreamToken == "" {
		upstreamToken = token
	}
	if upstreamSource == "" {
		upstreamSource = source
	}

	// Derive TLS config from the token (default passphrase when unset).
	// NextProtos ["h2", "http/1.1"] lets ALPN negotiate correctly for both
	// gRPC (HTTP/2) and HTTP/JSON gateway (HTTP/1.1) clients on the same port.
	tlsPassphrase := token
	if tlsPassphrase == "" {
		tlsPassphrase = tlsconf.DefaultPassphrase
	}
	serverTLSCfg, clientCreds, err := tlsconf.ServerConfig(tlsPassphrase)
	if err != nil {
		return fmt.Errorf("TLS setup: %w", err)
	}

	slog.Info("suffuse server starting",
		"version", Version,
		"addr", addr,
		"local_clip", !noLocal,
		"upstream", upstreamAddr,
	)

	h := hub.New()

	if !noLocal {
		backend := clip.New()
		lp := localpeer.New(h, backend, source)
		go lp.Run()
	}

	// Federation
	var upstreamProvider grpcservice.UpstreamInfoProvider
	if upstreamAddr != "" {
		up, err := federation.New(federation.Config{
			Addr:   upstreamAddr,
			Token:  upstreamToken,
			Source: upstreamSource,
		}, h)
		if err != nil {
			return fmt.Errorf("federation: %w", err)
		}
		upstreamProvider = up
		ctx, cancel := context.WithCancel(context.Background())
		_ = cancel
		go up.Run(ctx)
	}

	svc := grpcservice.New(h, token, upstreamProvider)

	// gRPC server — no grpc.Creds here; TLS is handled at the listener level.
	// grpcSrv.ServeHTTP implements http.Handler so it plugs into the shared
	// http.Server below.
	grpcSrv := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    kaTime,
			Timeout: kaTimeout,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             kaMinTime,
			PermitWithoutStream: true,
		}),
	)
	pb.RegisterClipboardServiceServer(grpcSrv, svc)
	reflection.Register(grpcSrv)

	// IPC socket — Unix domain socket, no TLS needed.
	if ln, err := ipc.Listen(); err != nil {
		slog.Warn("IPC socket unavailable", "err", err)
	} else {
		slog.Info("IPC socket listening", "path", ipc.SocketPath())
		ipcSrv := grpc.NewServer()
		pb.RegisterClipboardServiceServer(ipcSrv, svc)
		go ipcSrv.Serve(ln) //nolint:errcheck
	}

	// HTTP/JSON gateway — dials back to the local gRPC port using the derived
	// client credentials (same TLS passphrase, so the loopback dial succeeds).
	gwMux := gwruntime.NewServeMux()
	gwCtx, gwCancel := context.WithCancel(context.Background())
	defer gwCancel()
	if err := pb.RegisterClipboardServiceHandlerFromEndpoint(
		gwCtx, gwMux, addr,
		[]grpc.DialOption{grpc.WithTransportCredentials(clientCreds)},
	); err != nil {
		return fmt.Errorf("gateway registration: %w", err)
	}

	// Single TLS listener for both gRPC and HTTP/JSON.
	// The handler routes by Content-Type: gRPC requests have
	// "application/grpc" and arrive over HTTP/2; everything else goes to the
	// gateway mux.
	tcpLn, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	tlsLn := tls.NewListener(tcpLn, serverTLSCfg)
	slog.Info("listening", "addr", tcpLn.Addr())

	httpSrv := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.ProtoMajor == 2 && strings.HasPrefix(r.Header.Get("Content-Type"), "application/grpc") {
				grpcSrv.ServeHTTP(w, r)
			} else {
				gwMux.ServeHTTP(w, r)
			}
		}),
	}
	return httpSrv.Serve(tlsLn)
}

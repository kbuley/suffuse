package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "go.klb.dev/suffuse/gen/suffuse/v1"
	"go.klb.dev/suffuse/internal/ipc"
	"go.klb.dev/suffuse/internal/tlsconf"
)

func getenv(key string) string  { return os.Getenv(key) }
func hostname() (string, error) { return os.Hostname() }

func isContainerID(s string) bool {
	if len(s) < 12 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// defaultSource returns a human-readable identifier for this host.
func defaultSource() string {
	for _, env := range []string{
		"SUFFUSE_SOURCE",
		"CONTAINER_NAME",
		"COMPOSE_SERVICE",
		"SERVICE_NAME",
		"HOSTNAME_FRIENDLY",
	} {
		if v := getenv(env); v != "" {
			return v
		}
	}
	h, err := hostname()
	if err != nil {
		return "unknown"
	}
	if isContainerID(h) {
		return "container-" + h[:8]
	}
	return h
}

// defaultHosts is the probe order used when no explicit --host is given.
// IPC is tried first (before any TCP) by the callers via ipc.IsRunning().
var defaultHosts = []string{
	"host.docker.internal",     // Docker Desktop (macOS / Windows / Docker Desktop Linux)
	"host.containers.internal", // Podman rootless
	"localhost",
}

// dialIPC returns a *grpc.ClientConn connected to the local IPC Unix socket.
// No auth needed — the socket is local and owner-restricted by the OS.
func dialIPC() (*grpc.ClientConn, error) {
	return grpc.NewClient(
		"unix://"+ipc.SocketPath(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
}

// dialServer probes hosts in order and returns the first reachable TLS connection.
// If host is non-empty only that host is tried. Port defaults to 8752.
// token is used for both TLS key derivation and per-RPC auth.
func dialServer(host string, port int, token, source string) (*grpc.ClientConn, error) {
	if port == 0 {
		port = 8752
	}
	hosts := defaultHosts
	if host != "" {
		hosts = []string{host}
	}
	passphrase := token
	if passphrase == "" {
		passphrase = tlsconf.DefaultPassphrase
	}
	creds, err := tlsconf.ClientCredentials(passphrase)
	if err != nil {
		return nil, fmt.Errorf("tls credentials: %w", err)
	}
	opts := []grpc.DialOption{grpc.WithTransportCredentials(creds)}
	if token != "" || source != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(&clientCreds{token: token, source: source}))
	}
	var lastErr error
	for _, h := range hosts {
		addr := fmt.Sprintf("%s:%d", h, port)
		conn, err := grpc.NewClient(addr, opts...)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", addr, err)
			continue
		}
		// Verify reachability with a short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		client := pb.NewClipboardServiceClient(conn)
		_, err = client.Status(ctx, &pb.StatusRequest{})
		cancel()
		if err == nil {
			return conn, nil
		}
		_ = conn.Close()
		lastErr = fmt.Errorf("%s: %w", addr, err)
	}
	return nil, fmt.Errorf("no reachable suffuse server: %w", lastErr)
}

// dialOpts returns gRPC dial options for the local IPC socket (insecure).
func dialOpts(token, source string) []grpc.DialOption {
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	if token != "" || source != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(&clientCreds{token: token, source: source}))
	}
	return opts
}

type clientCreds struct {
	token  string
	source string
}

func (c *clientCreds) GetRequestMetadata(_ context.Context, _ ...string) (map[string]string, error) {
	md := make(map[string]string, 2)
	if c.token != "" {
		md["authorization"] = "Bearer " + c.token
	}
	if c.source != "" {
		md["x-suffuse-source"] = c.source
	}
	return md, nil
}

func (c *clientCreds) RequireTransportSecurity() bool { return false }

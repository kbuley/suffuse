package main

import (
	"context"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"go.klb.dev/suffuse/internal/ipc"
)

// getenv is a thin wrapper around os.Getenv.
func getenv(key string) string { return os.Getenv(key) }

// hostname wraps os.Hostname.
func hostname() (string, error) { return os.Hostname() }

// isContainerID reports whether s looks like a container ID hash:
// 12–64 lowercase hex characters with no dots or dashes.
func isContainerID(s string) bool {
	if len(s) < 12 || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// defaultSource returns a human-readable identifier for this host.
// Resolution order:
//  1. SUFFUSE_SOURCE env var
//  2. Common container / compose name env vars
//  3. Hostname — truncated and prefixed if it looks like a container ID hash
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

// dialIPC returns a *grpc.ClientConn connected to the local IPC Unix socket.
// No auth is needed — the socket is local and owner-restricted by the OS.
func dialIPC() (*grpc.ClientConn, error) {
	return grpc.NewClient(
		"unix://"+ipc.SocketPath(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
}

// dialOpts returns gRPC dial options for a client connection.
// If token is non-empty, a bearer token credential is attached to every call.
// If source is non-empty, it is sent as x-suffuse-source metadata on every call.
func dialOpts(token, source string) []grpc.DialOption {
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	if token != "" || source != "" {
		opts = append(opts, grpc.WithPerRPCCredentials(&clientCreds{
			token:  token,
			source: source,
		}))
	}
	return opts
}

// clientCreds implements credentials.PerRPCCredentials, attaching bearer token
// and x-suffuse-source to every gRPC call's metadata.
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

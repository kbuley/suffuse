// Package ipc provides helpers for the local Unix-socket IPC channel used by
// CLI tools (copy/paste/status) to talk to a running suffuse client daemon
// instead of opening their own TCP connections to the server.
//
// The IPC channel is plain gRPC served over a Unix domain socket, using the
// same ClipboardService proto as the TCP server. The client daemon listens on
// the socket; CLI sub-commands probe for it and fall back to direct TCP if it
// is absent.
package ipc

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
)

// SocketPath returns the platform-appropriate path for the IPC socket.
//
//   - Linux / macOS: $TMPDIR/suffuse.sock  (override with $SUFFUSE_SOCKET)
//   - Windows:       \\.\pipe\suffuse      (named pipe — not yet implemented)
func SocketPath() string {
	if s := os.Getenv("SUFFUSE_SOCKET"); s != "" {
		return s
	}
	if runtime.GOOS == "windows" {
		return `\\.\pipe\suffuse`
	}
	return filepath.Join(os.TempDir(), "suffuse.sock")
}

// IsRunning reports whether a suffuse client daemon appears to be listening
// on the IPC socket. It does a cheap dial-and-close; no data is exchanged.
func IsRunning() bool {
	c, err := net.Dial("unix", SocketPath())
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// Listen creates and returns a net.Listener on the IPC socket path, removing
// any stale socket file first.
func Listen() (net.Listener, error) {
	path := SocketPath()
	// Remove stale socket from a previous (crashed) run.
	_ = os.Remove(path)
	return net.Listen("unix", path)
}

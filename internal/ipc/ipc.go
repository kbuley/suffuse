// Package ipc provides local inter-process communication between the
// suffuse daemon and CLI tools (suffuse copy/paste/status).
//
// On Linux and macOS the transport is a Unix domain socket.
// On Windows it is a named pipe.
package ipc

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
)

// SocketPath returns the platform-appropriate path for the IPC socket.
func SocketPath() string {
	return socketPath()
}

// Listen starts listening on the IPC socket and returns a net.Listener.
func Listen() (net.Listener, error) {
	path := SocketPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return nil, fmt.Errorf("ipc mkdir: %w", err)
	}
	// Remove stale socket from a previous run.
	_ = os.Remove(path)
	return listenIPC(path)
}

// Dial connects to the IPC socket of a running daemon.
func Dial() (net.Conn, error) {
	return dialIPC(SocketPath())
}

// IsRunning reports whether a daemon is currently listening on the IPC socket.
func IsRunning() bool {
	conn, err := Dial()
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

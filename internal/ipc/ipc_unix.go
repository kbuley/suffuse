//go:build !windows

package ipc

import (
	"net"
	"os"
	"path/filepath"
)

func socketPath() string {
	// Linux: prefer XDG_RUNTIME_DIR
	if dir := os.Getenv("XDG_RUNTIME_DIR"); dir != "" {
		return filepath.Join(dir, "suffuse.sock")
	}
	// macOS / fallback
	return filepath.Join(os.TempDir(), "suffuse.sock")
}

func listenIPC(path string) (net.Listener, error) {
	return net.Listen("unix", path)
}

func dialIPC(path string) (net.Conn, error) {
	return net.Dial("unix", path)
}

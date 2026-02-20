//go:build windows

package ipc

import (
	"net"

	"github.com/microsoft/go-winio"
)

const pipeName = `\\.\pipe\suffuse`

func socketPath() string { return pipeName }

func listenIPC(_ string) (net.Listener, error) {
	return winio.ListenPipe(pipeName, nil)
}

func dialIPC(_ string) (net.Conn, error) {
	return winio.DialPipe(pipeName, nil)
}

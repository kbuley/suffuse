package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"go.klb.dev/suffuse/internal/clip"
	"go.klb.dev/suffuse/internal/crypto"
	"go.klb.dev/suffuse/internal/ipc"
	"go.klb.dev/suffuse/internal/message"
	"go.klb.dev/suffuse/internal/wire"
)

const (
	watchdogTimeout = 45 * time.Second
	watchdogCheck   = 5 * time.Second
)

func newClientCmd() *cobra.Command {
	v := viper.New()

	cmd := &cobra.Command{
		Use:   "client",
		Short: "Connect to a suffuse server and sync the local clipboard",
		Long: `Connects to a suffuse server and keeps the local system clipboard
in sync with all other connected peers. Reconnects automatically on disconnect.

When running as a service, copy/paste/status CLI tools connect to the client
daemon via the local IPC socket rather than the server directly.

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

	var key *[32]byte
	if token != "" {
		var err error
		key, err = crypto.DeriveKey(token)
		if err != nil {
			return fmt.Errorf("key derivation: %w", err)
		}
	}

	slog.Info("suffuse client starting",
		"version", Version,
		"server", serverAddr,
		"source", source,
		"encrypted", key != nil,
	)

	backend := clip.New()
	defer backend.Close()
	slog.Info("clipboard backend", "name", backend.Name())

	// IPC socket so copy/paste/status can talk to us
	ipcLn, err := ipc.Listen()
	if err != nil {
		slog.Warn("IPC socket unavailable", "err", err)
	} else {
		slog.Info("IPC socket listening", "path", ipc.SocketPath())
		go serveClientIPC(ipcLn, serverAddr, source, accept, key)
	}

	connectLoop(serverAddr, token, source, accept, key, backend)
	return nil
}

func connectLoop(
	serverAddr, token, source string,
	accept []string,
	key *[32]byte,
	backend clip.Backend,
) {
	delay := time.Second
	for {
		slog.Info("connecting", "addr", serverAddr)
		conn, err := net.DialTimeout("tcp", serverAddr, 10*time.Second)
		if err != nil {
			slog.Warn("connection failed", "err", err, "retry_in", delay)
			time.Sleep(delay)
			if delay < 30*time.Second {
				delay *= 2
			}
			continue
		}
		delay = time.Second
		slog.Info("connected")
		runSession(conn, token, source, accept, key, backend)
		slog.Warn("disconnected, reconnecting")
		time.Sleep(time.Second)
	}
}

type clientSession struct {
	wc        *wire.Conn
	source    string
	accept    []string
	backend   clip.Backend
	sendCh    chan *message.Message
	lastItems []message.Item
	lastRecv  atomic.Int64
}

func runSession(
	conn net.Conn,
	token, source string,
	accept []string,
	key *[32]byte,
	backend clip.Backend,
) {
	s := &clientSession{
		wc:      wire.New(conn, key),
		source:  source,
		accept:  accept,
		backend: backend,
		sendCh:  make(chan *message.Message, 8),
	}
	s.lastRecv.Store(time.Now().UnixNano())

	if token != "" {
		if err := s.wc.WriteMsg(&message.Message{
			Type:      message.TypeAuth,
			Source:    source,
			Clipboard: message.DefaultClipboard,
			Payload:   encodeToken(token),
			Accept:    accept,
		}); err != nil {
			slog.Error("auth send failed", "err", err)
			return
		}
	}

	// Writer
	go func() {
		for msg := range s.sendCh {
			if err := s.wc.WriteMsg(msg); err != nil {
				slog.Error("write failed", "err", err)
				s.wc.Close()
				return
			}
		}
	}()

	// Reader
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		for {
			msg, err := s.wc.ReadMsg()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					slog.Info("server closed connection", "err", err)
				}
				s.wc.Close()
				return
			}
			s.lastRecv.Store(time.Now().UnixNano())

			switch msg.Type {
			case message.TypeClipboard:
				if len(msg.Items) == 0 {
					continue
				}
				if reflect.DeepEqual(msg.Items, s.lastItems) {
					continue
				}
				s.lastItems = msg.Items
				slog.Debug("clipboard received", "source", msg.Source, "items", len(msg.Items))
				if err := backend.Write(msg.Items); err != nil {
					slog.Error("clipboard write failed", "err", err)
				}

			case message.TypePing:
				s.send(&message.Message{Type: message.TypePong, Source: source})

			case message.TypePong:
				// handled by lastRecv update

			case message.TypeError:
				slog.Error("server error", "error", msg.Error)
				s.wc.Close()
				return
			}
		}
	}()

	// Watchdog
	go func() {
		ticker := time.NewTicker(watchdogCheck)
		defer ticker.Stop()
		for {
			select {
			case <-readerDone:
				return
			case <-ticker.C:
				age := time.Since(time.Unix(0, s.lastRecv.Load()))
				if age > watchdogTimeout {
					slog.Warn("watchdog: server silent too long, closing", "silent_for", age.Round(time.Second))
					s.wc.Close()
					return
				}
			}
		}
	}()

	// Clipboard watcher
	for {
		select {
		case <-readerDone:
			return
		case <-backend.Watch():
			items, err := backend.Read()
			if err != nil || len(items) == 0 {
				continue
			}
			if reflect.DeepEqual(items, s.lastItems) {
				continue
			}
			s.lastItems = items
			slog.Debug("local clipboard changed, sending", "items", len(items))
			s.send(&message.Message{
				Type:      message.TypeClipboard,
				Source:    source,
				Clipboard: message.DefaultClipboard,
				Items:     items,
			})
		}
	}
}

func (s *clientSession) send(msg *message.Message) {
	select {
	case s.sendCh <- msg:
	default:
		slog.Warn("client send channel full, dropping")
	}
}

func serveClientIPC(ln net.Listener, serverAddr, source string, accept []string, key *[32]byte) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go handleClientIPC(conn, serverAddr, source, accept, key)
	}
}

func handleClientIPC(conn net.Conn, serverAddr, source string, accept []string, key *[32]byte) {
	defer conn.Close()
	wc := wire.New(conn, nil)

	msg, err := wc.ReadMsg()
	if err != nil {
		return
	}

	switch msg.Type {
	case message.TypeStatus:
		resp := proxyStatus(serverAddr, source, accept, key)
		_ = wc.WriteMsg(resp)

	case message.TypeClipboard:
		forwardToServer(serverAddr, source, key, msg)

	case message.TypePing:
		items := retrieveFromServer(serverAddr, source, accept, key)
		_ = wc.WriteMsg(&message.Message{
			Type:      message.TypeClipboard,
			Clipboard: message.DefaultClipboard,
			Items:     items,
		})
	}
}

func proxyStatus(serverAddr, source string, accept []string, key *[32]byte) *message.Message {
	conn, err := net.DialTimeout("tcp", serverAddr, 5*time.Second)
	if err != nil {
		return &message.Message{
			Type:  message.TypeStatusResponse,
			Role:  message.RoleClient,
			Error: fmt.Sprintf("could not reach server: %v", err),
			Upstream: &message.UpstreamInfo{
				Addr: serverAddr,
			},
		}
	}
	defer conn.Close()

	wc := wire.New(conn, key)
	_ = wc.WriteMsg(&message.Message{
		Type:   message.TypeStatus,
		Source: source,
		Accept: accept,
	})

	resp, err := wc.ReadMsg()
	if err != nil {
		return &message.Message{
			Type:  message.TypeStatusResponse,
			Role:  message.RoleClient,
			Error: fmt.Sprintf("status read failed: %v", err),
		}
	}

	resp.Role = message.RoleClient
	resp.Upstream = &message.UpstreamInfo{
		Addr:        serverAddr,
		ConnectedAt: time.Now(),
		LastSeen:    time.Now(),
	}
	return resp
}

func forwardToServer(serverAddr, source string, key *[32]byte, msg *message.Message) {
	conn, err := net.DialTimeout("tcp", serverAddr, 5*time.Second)
	if err != nil {
		slog.Warn("copy: could not reach server", "err", err)
		return
	}
	defer conn.Close()
	wc := wire.New(conn, key)
	msg.Source = source
	_ = wc.WriteMsg(msg)
}

func retrieveFromServer(serverAddr, source string, accept []string, key *[32]byte) []message.Item {
	conn, err := net.DialTimeout("tcp", serverAddr, 5*time.Second)
	if err != nil {
		return nil
	}
	defer conn.Close()
	wc := wire.New(conn, key)
	_ = wc.WriteMsg(&message.Message{
		Type:   message.TypePing,
		Source: source,
		Accept: accept,
	})
	msg, err := wc.ReadMsg()
	if err != nil || msg.Type != message.TypeClipboard {
		return nil
	}
	return msg.Items
}

package main

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"net"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"go.klb.dev/suffuse/internal/clip"
	"go.klb.dev/suffuse/internal/crypto"
	"go.klb.dev/suffuse/internal/hub"
	"go.klb.dev/suffuse/internal/ipc"
	"go.klb.dev/suffuse/internal/localpeer"
	"go.klb.dev/suffuse/internal/message"
	"go.klb.dev/suffuse/internal/tcppeer"
	"go.klb.dev/suffuse/internal/wire"
)

func newServerCmd() *cobra.Command {
	v := viper.New()

	cmd := &cobra.Command{
		Use:   "server",
		Short: "Run the clipboard hub (+ local clipboard integration)",
		Long: `Starts the suffuse hub. All connected clients share a clipboard.
The server also participates as a local clipboard peer by default.

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
	f.String("addr", "0.0.0.0:8752", "TCP listen address")
	f.String("token", "", "shared secret (empty = no auth, no encryption)")
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

	var key *[32]byte
	if token != "" {
		var err error
		key, err = crypto.DeriveKey(token)
		if err != nil {
			return fmt.Errorf("key derivation: %w", err)
		}
	}

	slog.Info("suffuse server starting",
		"version", Version,
		"addr", addr,
		"local_clip", !noLocal,
		"encrypted", key != nil,
	)

	h := hub.New()

	if !noLocal {
		backend := clip.New()
		lp := localpeer.New(h, backend, source)
		go lp.Run()
	}

	// IPC socket for copy/paste/status CLI tools
	ipcLn, err := ipc.Listen()
	if err != nil {
		slog.Warn("IPC socket unavailable", "err", err)
	} else {
		slog.Info("IPC socket listening", "path", ipc.SocketPath())
		go serveIPC(ipcLn, h)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}
	slog.Info("listening", "addr", ln.Addr())

	for {
		conn, err := ln.Accept()
		if err != nil {
			slog.Error("accept failed", "err", err)
			continue
		}
		peer := tcppeer.New(conn, h, token, key)
		go peer.Serve()
	}
}

func serveIPC(ln net.Listener, h *hub.Hub) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go handleIPCConn(conn, h)
	}
}

func handleIPCConn(conn net.Conn, h *hub.Hub) {
	defer conn.Close()
	wc := wire.New(conn, nil)

	msg, err := wc.ReadMsg()
	if err != nil {
		return
	}

	switch msg.Type {
	case message.TypeClipboard:
		h.Publish(msg.Items, msg.ClipboardOf(), "ipc:copy")
		slog.Debug("ipc: clipboard published", "items", len(msg.Items))

	case message.TypeStatus:
		peers := h.Peers()
		hostname, _ := os.Hostname()
		_ = wc.WriteMsg(&message.Message{
			Type:  message.TypeStatusResponse,
			Role:  message.RoleBoth,
			Peers: peers,
			Upstream: &message.UpstreamInfo{
				Addr: hostname,
			},
		})

	case message.TypePing:
		// paste request — return latest clipboard via a transient peer registration
		pp := &pastePeer{wc: wc, h: h, got: make(chan *message.Message, 1)}
		pp.requestLatest(msg.ClipboardOf())
	}
}

// pastePeer is a transient hub.Peer used to retrieve the latest clipboard value.
type pastePeer struct {
	wc  *wire.Conn
	h   *hub.Hub
	got chan *message.Message
}

func (p *pastePeer) ID() string { return "ipc:paste" }

func (p *pastePeer) Info() message.PeerInfo {
	return message.PeerInfo{
		ID:        "ipc:paste",
		Source:    "paste",
		Clipboard: message.DefaultClipboard,
	}
}

func (p *pastePeer) Send(msg *message.Message) {
	select {
	case p.got <- msg:
	default:
	}
}

func (p *pastePeer) requestLatest(clipboard string) {
	p.h.Register(p)
	defer p.h.Unregister(p)
	select {
	case msg := <-p.got:
		_ = p.wc.WriteMsg(msg)
	default:
		_ = p.wc.WriteMsg(&message.Message{
			Type:      message.TypeClipboard,
			Clipboard: clipboard,
		})
	}
}

func encodeToken(token string) string {
	return base64.StdEncoding.EncodeToString([]byte(token))
}

func defaultSource() string {
	if h, err := os.Hostname(); err == nil {
		return h
	}
	return "unknown"
}

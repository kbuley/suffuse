// Package tcppeer adapts a net.Conn into a hub.Peer.
package tcppeer

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"go.klb.dev/suffuse/internal/hub"
	"go.klb.dev/suffuse/internal/message"
	"go.klb.dev/suffuse/internal/wire"
)

const (
	pingInterval = 15 * time.Second
	pongDeadline = 10 * time.Second
	authTimeout  = 10 * time.Second
)

// Peer wraps a single TCP connection as a hub.Peer.
type Peer struct {
	id     string
	conn   *wire.Conn
	h      *hub.Hub
	token  string
	sendCh chan *message.Message
	pongCh chan struct{}

	mu       sync.RWMutex
	info     message.PeerInfo
	lastSeen atomic.Int64 // UnixNano
}

// New creates a Peer for conn. token may be empty to disable auth.
func New(conn net.Conn, h *hub.Hub, token string, key *[32]byte) *Peer {
	now := time.Now()
	p := &Peer{
		id:     conn.RemoteAddr().String(),
		conn:   wire.New(conn, key),
		h:      h,
		token:  token,
		sendCh: make(chan *message.Message, 64),
		pongCh: make(chan struct{}, 1),
		info: message.PeerInfo{
			ID:          conn.RemoteAddr().String(),
			Addr:        conn.RemoteAddr().String(),
			Role:        message.RoleClient,
			Clipboard:   message.DefaultClipboard,
			ConnectedAt: now,
			LastSeen:    now,
		},
	}
	p.lastSeen.Store(now.UnixNano())
	return p
}

func (p *Peer) ID() string { return p.id }

func (p *Peer) Info() message.PeerInfo {
	p.mu.RLock()
	defer p.mu.RUnlock()
	info := p.info
	info.LastSeen = time.Unix(0, p.lastSeen.Load())
	return info
}

func (p *Peer) Send(msg *message.Message) {
	select {
	case p.sendCh <- msg:
	default:
		slog.Warn("tcp send channel full, dropping", "peer", p.id)
	}
}

func (p *Peer) notifyAlive() {
	p.lastSeen.Store(time.Now().UnixNano())
	select {
	case p.pongCh <- struct{}{}:
	default:
	}
}

// Serve authenticates, registers with the hub, and runs the read/write/ping loops.
func (p *Peer) Serve() {
	defer p.conn.Close()
	log := slog.With("peer", p.id)

	// Auth
	if p.token != "" {
		p.conn.SetReadDeadline(authTimeout)
		msg, err := p.conn.ReadMsg()
		if err != nil {
			log.Warn("auth read failed", "err", err)
			return
		}
		p.conn.SetReadDeadline(0)

		tokenBytes, _ := base64.StdEncoding.DecodeString(msg.Payload)
		if msg.Type != message.TypeAuth || string(tokenBytes) != p.token {
			log.Warn("auth failed")
			_ = p.conn.WriteMsg(&message.Message{
				Type:  message.TypeError,
				Error: "auth_failed",
			})
			return
		}

		p.mu.Lock()
		p.info.Source = msg.Source
		if msg.Clipboard != "" {
			p.info.Clipboard = msg.Clipboard
		}
		p.info.AcceptedTypes = msg.Accept
		p.mu.Unlock()

		log.Info("authenticated", "source", msg.Source)
	}

	// Register
	p.h.Register(p)
	defer func() {
		p.h.Unregister(p)
		close(p.sendCh)
	}()

	// Writer
	go func() {
		for msg := range p.sendCh {
			if err := p.conn.WriteMsg(msg); err != nil {
				log.Error("write failed", "err", err)
				p.conn.Close()
				return
			}
		}
	}()

	// Ping loop
	go func() {
		ticker := time.NewTicker(pingInterval)
		defer ticker.Stop()
		for range ticker.C {
			p.Send(&message.Message{Type: message.TypePing})
			select {
			case <-p.pongCh:
			case <-time.After(pongDeadline):
				log.Warn("pong timeout, closing")
				p.conn.Close()
				return
			}
		}
	}()

	// Reader
	for {
		msg, err := p.conn.ReadMsg()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				log.Info("connection closed", "err", err)
			}
			return
		}

		p.notifyAlive()

		switch msg.Type {
		case message.TypeClipboard:
			log.Debug("clipboard received",
				"items", len(msg.Items),
				"clipboard", msg.ClipboardOf(),
			)
			p.h.Publish(msg.Items, msg.ClipboardOf(), p.id)

		case message.TypePong:
			// handled by notifyAlive

		case message.TypePing:
			p.Send(&message.Message{Type: message.TypePong})

		case message.TypeStatus:
			peers := p.h.Peers()
			p.Send(&message.Message{
				Type:  message.TypeStatusResponse,
				Role:  message.RoleServer,
				Peers: peers,
			})

		default:
			log.Warn("unexpected message type", "type", msg.Type)
		}
	}
}

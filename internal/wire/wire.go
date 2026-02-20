// Package wire handles reading and writing newline-delimited JSON messages
// over a net.Conn, with optional NaCl secretbox encryption.
//
// Wire format (unencrypted):
//
//	<json>\n
//
// Wire format (encrypted):
//
//	<base64(nonce+ciphertext)>\n
//
// The encrypted form is just a base64 blob on the wire so that the framing
// logic is identical in both cases — every line is a single message.
package wire

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"net"
	"time"

	"go.klb.dev/suffuse/internal/crypto"
	"go.klb.dev/suffuse/internal/message"
)

const (
	// MaxMessageSize is the largest message we will read (16 MiB).
	MaxMessageSize = 16 * 1024 * 1024

	writeDeadline = 5 * time.Second
)

// Conn wraps a net.Conn with buffered newline-delimited JSON framing
// and optional encryption.
type Conn struct {
	conn net.Conn
	br   *bufio.Reader
	key  *[32]byte // nil = no encryption
}

// New wraps conn. If key is non-nil every message is encrypted with NaCl
// secretbox before being written and decrypted after being read.
func New(conn net.Conn, key *[32]byte) *Conn {
	return &Conn{
		conn: conn,
		br:   bufio.NewReaderSize(conn, 64*1024),
		key:  key,
	}
}

// Underlying returns the underlying net.Conn.
func (c *Conn) Underlying() net.Conn { return c.conn }

// SetReadDeadline sets or clears the read deadline.
func (c *Conn) SetReadDeadline(d time.Duration) {
	if d == 0 {
		_ = c.conn.SetReadDeadline(time.Time{})
	} else {
		_ = c.conn.SetReadDeadline(time.Now().Add(d))
	}
}

// SetWriteDeadline sets or clears the write deadline.
func (c *Conn) SetWriteDeadline(d time.Duration) {
	if d == 0 {
		_ = c.conn.SetWriteDeadline(time.Time{})
	} else {
		_ = c.conn.SetWriteDeadline(time.Now().Add(d))
	}
}

// Close closes the underlying connection.
func (c *Conn) Close() error { return c.conn.Close() }

// RemoteAddr returns the remote network address.
func (c *Conn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }

// WriteMsg serialises msg to JSON, optionally encrypts it, and writes it
// followed by a newline.
func (c *Conn) WriteMsg(msg *message.Message) error {
	raw, err := msg.Encode()
	if err != nil {
		return fmt.Errorf("encode: %w", err)
	}

	var line []byte
	if c.key != nil {
		ct, err := crypto.Seal(raw, c.key)
		if err != nil {
			return fmt.Errorf("encrypt: %w", err)
		}
		b64 := base64.StdEncoding.EncodeToString(ct)
		line = append([]byte(b64), '\n')
	} else {
		line = append(raw, '\n')
	}

	c.SetWriteDeadline(writeDeadline)
	_, err = c.conn.Write(line)
	c.SetWriteDeadline(0)
	return err
}

// ReadMsg reads one newline-terminated line, optionally decrypts it, and
// deserialises it into a Message.
func (c *Conn) ReadMsg() (*message.Message, error) {
	line, err := c.br.ReadBytes('\n')
	if err != nil {
		return nil, err
	}
	if len(line) > MaxMessageSize {
		return nil, fmt.Errorf("message too large (%d bytes)", len(line))
	}

	// Strip trailing newline
	line = line[:len(line)-1]

	var raw []byte
	if c.key != nil {
		ct, err := base64.StdEncoding.DecodeString(string(line))
		if err != nil {
			return nil, fmt.Errorf("base64 decode: %w", err)
		}
		raw, err = crypto.Open(ct, c.key)
		if err != nil {
			return nil, fmt.Errorf("decrypt: %w", err)
		}
	} else {
		raw = line
	}

	return message.Decode(raw)
}

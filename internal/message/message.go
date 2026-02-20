// Package message defines the suffuse wire protocol.
//
// All messages are newline-delimited JSON. Payloads are always base64-encoded
// so that binary content (images, etc.) is safe to embed in JSON strings.
// Each message is exactly one line: <json>\n
package message

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"
)

// Type identifies the kind of message.
type Type string

const (
	TypeClipboard      Type = "CLIPBOARD"
	TypePing           Type = "PING"
	TypePong           Type = "PONG"
	TypeAuth           Type = "AUTH"
	TypeStatus         Type = "STATUS"
	TypeStatusResponse Type = "STATUS_RESPONSE"
	TypeError          Type = "ERROR"
)

// Role identifies whether a peer is a server or client.
type Role string

const (
	RoleServer Role = "server"
	RoleClient Role = "client"
	RoleBoth   Role = "both" // server with local clipboard integration
)

// DefaultClipboard is the name of the default clipboard namespace.
const DefaultClipboard = "default"

// Item is a single clipboard representation with a MIME type.
// Data is always base64-encoded.
type Item struct {
	MIME string `json:"mime"`
	Data string `json:"data"` // base64-encoded
}

// NewTextItem creates a text/plain Item from a plain string.
func NewTextItem(text string) Item {
	return Item{
		MIME: "text/plain",
		Data: base64.StdEncoding.EncodeToString([]byte(text)),
	}
}

// NewBinaryItem creates an Item from raw bytes with the given MIME type.
func NewBinaryItem(mime string, data []byte) Item {
	return Item{
		MIME: mime,
		Data: base64.StdEncoding.EncodeToString(data),
	}
}

// Decode returns the raw bytes of the item payload.
func (it Item) Decode() ([]byte, error) {
	return base64.StdEncoding.DecodeString(it.Data)
}

// PeerInfo carries metadata about a connected peer, used in STATUS responses.
type PeerInfo struct {
	ID            string    `json:"id"`
	Source        string    `json:"source"`
	Addr          string    `json:"addr"`
	Role          Role      `json:"role"`
	Clipboard     string    `json:"clipboard"`
	AcceptedTypes []string  `json:"accepted_types,omitempty"`
	ConnectedAt   time.Time `json:"connected_at"`
	LastSeen      time.Time `json:"last_seen"`
}

// UpstreamInfo carries metadata about a client's server connection.
type UpstreamInfo struct {
	Addr        string    `json:"addr"`
	ConnectedAt time.Time `json:"connected_at"`
	LastSeen    time.Time `json:"last_seen"`
}

// Message is the top-level wire envelope.
type Message struct {
	// Always present
	Type      Type   `json:"type"`
	Source    string `json:"source,omitempty"`
	Clipboard string `json:"clipboard,omitempty"`

	// CLIPBOARD — the items on the clipboard, one per MIME type
	Items []Item `json:"items,omitempty"`

	// AUTH — token is base64-encoded; Accept declares which MIME types
	// this peer will accept. Empty Accept means accept all types.
	Payload string   `json:"payload,omitempty"`
	Accept  []string `json:"accept,omitempty"`

	// STATUS_RESPONSE
	Role     Role          `json:"role,omitempty"`
	Peers    []PeerInfo    `json:"peers,omitempty"`
	Upstream *UpstreamInfo `json:"upstream,omitempty"`

	// ERROR
	Error string `json:"error,omitempty"`
}

// Encode serialises the message to JSON without a trailing newline.
func (m *Message) Encode() ([]byte, error) {
	return json.Marshal(m)
}

// Decode deserialises a message from raw JSON bytes.
func Decode(b []byte) (*Message, error) {
	var m Message
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("message decode: %w", err)
	}
	return &m, nil
}

// ClipboardOf returns the effective clipboard name, defaulting to DefaultClipboard.
func (m *Message) ClipboardOf() string {
	if m.Clipboard == "" {
		return DefaultClipboard
	}
	return m.Clipboard
}

// TextPayload returns the decoded content of the first text/plain item, or "".
func (m *Message) TextPayload() string {
	for _, it := range m.Items {
		if it.MIME == "text/plain" {
			b, err := it.Decode()
			if err != nil {
				return ""
			}
			return string(b)
		}
	}
	return ""
}

// FilterItems returns only the items whose MIME type appears in accepted.
// If accepted is empty all items are returned unchanged.
func (m *Message) FilterItems(accepted []string) []Item {
	if len(accepted) == 0 {
		return m.Items
	}
	set := make(map[string]struct{}, len(accepted))
	for _, a := range accepted {
		set[a] = struct{}{}
	}
	var out []Item
	for _, it := range m.Items {
		if _, ok := set[it.MIME]; ok {
			out = append(out, it)
		}
	}
	return out
}

package main

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"go.klb.dev/suffuse/internal/crypto"
	"go.klb.dev/suffuse/internal/ipc"
	"go.klb.dev/suffuse/internal/message"
	"go.klb.dev/suffuse/internal/wire"
)

func newCopyCmd() *cobra.Command {
	v := viper.New()

	cmd := &cobra.Command{
		Use:   "copy",
		Short: "Copy stdin to the suffuse clipboard (like pbcopy)",
		Long: `Reads stdin and sends it to the suffuse clipboard.

If a local suffuse daemon is running, it is used directly via the IPC socket.
Otherwise connects to the server specified in config or via --server.`,
		Args:    cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error { return bindViper(cmd, v) },
		RunE:    func(_ *cobra.Command, _ []string) error { return runCopy(v) },
	}

	f := cmd.Flags()
	f.String("server", "localhost:8752", "suffuse server address (used if no local daemon)")
	f.String("token", "", "shared secret")
	f.String("mime", "text/plain", "MIME type of the data being copied")
	f.String("source", defaultSource(), "source identifier")
	addConfigFlag(cmd)

	return cmd
}

func runCopy(v *viper.Viper) error {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	if len(data) == 0 {
		return nil
	}

	mime := v.GetString("mime")
	source := v.GetString("source")

	var item message.Item
	if mime == "text/plain" {
		item = message.NewTextItem(string(data))
	} else {
		item = message.NewBinaryItem(mime, data)
	}

	msg := &message.Message{
		Type:      message.TypeClipboard,
		Source:    source,
		Clipboard: message.DefaultClipboard,
		Items:     []message.Item{item},
	}

	// Try local daemon first
	if ipc.IsRunning() {
		conn, err := ipc.Dial()
		if err == nil {
			defer conn.Close()
			wc := wire.New(conn, nil)
			if err := wc.WriteMsg(msg); err != nil {
				slog.Warn("ipc copy failed", "err", err)
			} else {
				return nil
			}
		}
	}

	// Fall back to direct server connection
	serverAddr := v.GetString("server")
	token := v.GetString("token")

	var key *[32]byte
	if token != "" {
		key, err = crypto.DeriveKey(token)
		if err != nil {
			return fmt.Errorf("key derivation: %w", err)
		}
	}

	conn, err := net.DialTimeout("tcp", serverAddr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("connect %s: %w", serverAddr, err)
	}
	defer conn.Close()

	wc := wire.New(conn, key)
	if token != "" {
		if err := wc.WriteMsg(&message.Message{
			Type:    message.TypeAuth,
			Source:  source,
			Payload: encodeToken(token),
		}); err != nil {
			return fmt.Errorf("auth: %w", err)
		}
	}

	return wc.WriteMsg(msg)
}

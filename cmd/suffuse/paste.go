package main

import (
	"fmt"
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

func newPasteCmd() *cobra.Command {
	v := viper.New()

	cmd := &cobra.Command{
		Use:   "paste",
		Short: "Print the suffuse clipboard to stdout (like pbpaste)",
		Long: `Retrieves the current suffuse clipboard and writes it to stdout.

If a local suffuse daemon is running, it is used directly via the IPC socket.
Otherwise connects to the server specified in config or via --server.

If the clipboard contains only an image and --mime is not set to image/png,
nothing is printed (exit 0). To retrieve an image use:

  suffuse paste --mime image/png > screenshot.png`,
		Args:    cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error { return bindViper(cmd, v) },
		RunE:    func(_ *cobra.Command, _ []string) error { return runPaste(v) },
	}

	f := cmd.Flags()
	f.String("server", "localhost:8752", "suffuse server address (used if no local daemon)")
	f.String("token", "", "shared secret")
	f.String("mime", "text/plain", "preferred MIME type to output")
	f.String("source", defaultSource(), "source identifier")
	addConfigFlag(cmd)

	return cmd
}

func runPaste(v *viper.Viper) error {
	mime := v.GetString("mime")
	source := v.GetString("source")
	token := v.GetString("token")

	var items []message.Item

	// Try local daemon first
	if ipc.IsRunning() {
		conn, err := ipc.Dial()
		if err == nil {
			defer conn.Close()
			wc := wire.New(conn, nil)
			if err := wc.WriteMsg(&message.Message{
				Type:      message.TypePing,
				Source:    source,
				Clipboard: message.DefaultClipboard,
				Accept:    []string{mime},
			}); err == nil {
				if msg, err := wc.ReadMsg(); err == nil && msg.Type == message.TypeClipboard {
					items = msg.Items
				}
			}
		}
	}

	// Fall back to direct server connection
	if items == nil {
		serverAddr := v.GetString("server")
		var key *[32]byte
		if token != "" {
			var err error
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
				Accept:  []string{mime},
			}); err != nil {
				return fmt.Errorf("auth: %w", err)
			}
		}

		if err := wc.WriteMsg(&message.Message{
			Type:      message.TypePing,
			Source:    source,
			Clipboard: message.DefaultClipboard,
			Accept:    []string{mime},
		}); err != nil {
			return fmt.Errorf("paste request: %w", err)
		}

		msg, err := wc.ReadMsg()
		if err != nil {
			return fmt.Errorf("paste response: %w", err)
		}
		items = msg.Items
	}

	for _, it := range items {
		if it.MIME == mime {
			data, err := it.Decode()
			if err != nil {
				return fmt.Errorf("decode item: %w", err)
			}
			_, err = os.Stdout.Write(data)
			return err
		}
	}

	// Requested type not in clipboard — exit 0, print nothing (pbpaste behaviour)
	return nil
}

package main

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"

	pb "go.klb.dev/suffuse/gen/suffuse/v1"
	"go.klb.dev/suffuse/internal/hub"
	"go.klb.dev/suffuse/internal/ipc"
)

func newPasteCmd() *cobra.Command {
	v := viper.New()

	cmd := &cobra.Command{
		Use:   "paste",
		Short: "Print the suffuse clipboard to stdout (like pbpaste)",
		Long: `Retrieves the current suffuse clipboard and writes it to stdout.

If the clipboard contains only types not matching --mime, nothing is printed
(exit 0). To retrieve an image:

  suffuse paste --mime image/png > screenshot.png`,
		Args:    cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error { return bindViper(cmd, v) },
		RunE:    func(cmd *cobra.Command, _ []string) error { return runPaste(cmd, v) },
	}

	f := cmd.Flags()
	f.String("server", "localhost:8752", "suffuse server address (used when no daemon is running)")
	f.String("token", "", "shared secret")
	f.String("mime", "text/plain", "preferred MIME type to output")
	f.String("source", defaultSource(), "source identifier")
	f.String("clipboard", hub.DefaultClipboard, "clipboard namespace")
	addConfigFlag(cmd)

	return cmd
}

func runPaste(cmd *cobra.Command, v *viper.Viper) error {
	mime := v.GetString("mime")
	source := v.GetString("source")
	clipboard := v.GetString("clipboard")

	var (
		conn *grpc.ClientConn
		err  error
	)
	if !cmd.Flags().Changed("server") && ipc.IsRunning() {
		conn, err = dialIPC()
		if err != nil {
			conn = nil
		}
	}
	if conn == nil {
		serverAddr := v.GetString("server")
		token := v.GetString("token")
		conn, err = grpc.NewClient(serverAddr, dialOpts(token, source)...)
		if err != nil {
			return fmt.Errorf("dial: %w", err)
		}
	}
	defer conn.Close()

	client := pb.NewClipboardServiceClient(conn)
	resp, err := client.Paste(context.Background(), &pb.PasteRequest{
		Clipboard: clipboard,
		Accepts:   []string{mime},
	})
	if err != nil {
		return fmt.Errorf("paste: %w", err)
	}

	for _, it := range resp.Items {
		if it.Mime == mime {
			_, err = os.Stdout.Write(it.Data)
			return err
		}
	}

	// Requested type not present — exit 0, print nothing (pbpaste behaviour).
	return nil
}

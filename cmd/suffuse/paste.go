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
		RunE:    func(_ *cobra.Command, _ []string) error { return runPaste(v) },
	}

	f := cmd.Flags()
	f.String("host", "", "suffuse server host (probes docker/podman/localhost if unset)")
	f.Int("port", 8752, "suffuse server port")
	f.String("token", "", "shared secret")
	f.String("mime", "text/plain", "preferred MIME type to output")
	f.String("source", defaultSource(), "source identifier")
	f.String("clipboard", hub.DefaultClipboard, "clipboard namespace")
	addConfigFlag(cmd)

	return cmd
}

func runPaste(v *viper.Viper) error {
	mime      := v.GetString("mime")
	source    := v.GetString("source")
	clipboard := v.GetString("clipboard")
	token     := v.GetString("token")
	host      := v.GetString("host")
	port      := v.GetInt("port")

	var (
		conn *grpc.ClientConn
		err  error
	)

	if ipc.IsRunning() {
		conn, err = dialIPC()
	}
	if conn == nil {
		conn, err = dialServer(host, port, token, source)
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
	return nil
}

package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"

	pb "go.klb.dev/suffuse/gen/suffuse/v1"
	"go.klb.dev/suffuse/internal/hub"
	"go.klb.dev/suffuse/internal/ipc"
)

func newCopyCmd() *cobra.Command {
	v := viper.New()

	cmd := &cobra.Command{
		Use:     "copy",
		Short:   "Copy stdin to the suffuse clipboard (like pbcopy)",
		Long:    `Reads stdin and publishes it to the suffuse clipboard via gRPC.`,
		Args:    cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error { return bindViper(cmd, v) },
		RunE:    func(cmd *cobra.Command, _ []string) error { return runCopy(cmd, v) },
	}

	f := cmd.Flags()
	f.String("server", "localhost:8752", "suffuse server address (used when no daemon is running)")
	f.String("token", "", "shared secret")
	f.String("mime", "text/plain", "MIME type of the data being copied")
	f.String("source", defaultSource(), "source identifier")
	f.String("clipboard", hub.DefaultClipboard, "clipboard namespace")
	addConfigFlag(cmd)

	return cmd
}

func runCopy(cmd *cobra.Command, v *viper.Viper) error {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	if len(data) == 0 {
		return nil
	}

	mime := v.GetString("mime")
	source := v.GetString("source")
	clipboard := v.GetString("clipboard")

	var conn *grpc.ClientConn
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
	_, err = client.Copy(context.Background(), &pb.CopyRequest{
		Source:    source,
		Clipboard: clipboard,
		Items:     []*pb.ClipboardItem{{Mime: mime, Data: data}},
	})
	if err != nil {
		return fmt.Errorf("copy: %w", err)
	}
	slog.Debug("copied", "mime", mime, "bytes", len(data))
	return nil
}

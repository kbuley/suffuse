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
		Use:   "copy",
		Short: "Copy stdin to the suffuse clipboard (like pbcopy)",
		Long:  `Reads stdin and publishes it to the suffuse clipboard via gRPC.`,
		Args:    cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error { return bindViper(cmd, v) },
		RunE:    func(_ *cobra.Command, _ []string) error { return runCopy(v) },
	}

	f := cmd.Flags()
	f.String("host", "", "suffuse server host (probes docker/podman/localhost if unset)")
	f.Int("port", 8752, "suffuse server port")
	f.String("token", "", "shared secret")
	f.String("mime", "text/plain", "MIME type of the data being copied")
	f.String("source", defaultSource(), "source identifier")
	f.String("clipboard", hub.DefaultClipboard, "clipboard namespace")
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

	mime      := v.GetString("mime")
	source    := v.GetString("source")
	clipboard := v.GetString("clipboard")
	token     := v.GetString("token")
	host      := v.GetString("host")
	port      := v.GetInt("port")

	var conn *grpc.ClientConn

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

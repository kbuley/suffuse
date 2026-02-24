package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/timestamppb"

	pb "go.klb.dev/suffuse/gen/suffuse/v1"
	"go.klb.dev/suffuse/internal/ipc"
)

func newStatusCmd() *cobra.Command {
	v := viper.New()

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show connected peers",
		Long: `Displays all peers currently connected to the suffuse server,
including source name, address, role, clipboard, and last-seen time.

Connects via the local IPC socket when a daemon is running on this host.
Pass --host to query a remote server directly over TCP.

Flags and their environment variables / config-file keys
  --host    SUFFUSE_HOST    host
  --port    SUFFUSE_PORT    port    (default: 8752)
  --token   SUFFUSE_TOKEN   token
  --source  SUFFUSE_SOURCE  source
  --json    (no env/config equivalent)

Config file search order (first found wins)
  /etc/suffuse/suffuse.toml
  $HOME/.config/suffuse/suffuse.toml
  path supplied via --config

Precedence: defaults → config file → SUFFUSE_* env vars → CLI flags`,
		Args:    cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error { return bindViper(cmd, v) },
		RunE:    func(cmd *cobra.Command, _ []string) error { return runStatus(cmd, v) },
	}

	f := cmd.Flags()
	f.String("host", "", "suffuse server host (probes docker/podman/localhost if unset)")
	f.Int("port", 8752, "suffuse server port")
	f.String("token", "", "shared secret")
	f.String("source", defaultSource(), "source identifier")
	f.Bool("json", false, "output raw JSON")
	addConfigFlag(cmd)

	return cmd
}

func runStatus(cmd *cobra.Command, v *viper.Viper) error {
	source  := v.GetString("source")
	token   := v.GetString("token")
	host    := v.GetString("host")
	port    := v.GetInt("port")
	jsonOut := v.GetBool("json")

	var (
		conn       *grpc.ClientConn
		transport  string
		remoteAddr string // non-empty when querying a remote server over TCP
		err        error
	)

	if !cmd.Flags().Changed("host") && ipc.IsRunning() {
		conn, err = dialIPC()
		if err == nil {
			transport = fmt.Sprintf("ipc (%s)", ipc.SocketPath())
		} else {
			conn = nil
		}
	}

	if conn == nil {
		var resolvedHost string
		conn, resolvedHost, err = dialServerResolved(host, port, token, source)
		if err != nil {
			return fmt.Errorf("dial: %w", err)
		}
		remoteAddr = fmt.Sprintf("%s:%d", resolvedHost, port)
		if host != "" {
			transport = fmt.Sprintf("tcp (%s:%d)", host, port)
		} else {
			transport = fmt.Sprintf("tcp (port %d, auto-probed)", port)
		}
	}
	defer conn.Close()

	client := pb.NewClipboardServiceClient(conn)
	resp, err := client.Status(context.Background(), &pb.StatusRequest{})
	if err != nil {
		return fmt.Errorf("status: %w", err)
	}

	if jsonOut {
		enc, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Println(string(enc))
		return nil
	}

	printStatus(resp, source, transport, remoteAddr)
	return nil
}

func printStatus(resp *pb.StatusResponse, mySource, transport string, remoteAddr string) {
	w := tabwriter.NewWriter(os.Stdout, 1, 0, 2, ' ', 0)

	fmt.Fprintf(w, "Transport:\t%s\n", transport)
	if ui := resp.UpstreamInfo; ui != nil {
		fmt.Fprintf(w, "Upstream:\t%s\n", ui.Addr)
		if ui.ConnectedAt != nil && !ui.ConnectedAt.AsTime().IsZero() {
			t := ui.ConnectedAt.AsTime()
			fmt.Fprintf(w, "Connected:\t%s (%s)\n", t.UTC().Format(time.RFC3339), fmtAge(t))
		}
		if ui.LastSeen != nil && !ui.LastSeen.AsTime().IsZero() {
			fmt.Fprintf(w, "Last seen:\t%s\n", fmtAge(ui.LastSeen.AsTime()))
		}
	}
	fmt.Fprintln(w)
	_ = w.Flush()

	if len(resp.Peers) == 0 {
		fmt.Println("No peers connected.")
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 1, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "\tSOURCE\tADDR\tROLE\tCLIPBOARD\tCONNECTED\tLAST SEEN\tACCEPTS\n")
	_, _ = fmt.Fprintf(tw, "\t------\t----\t----\t---------\t---------\t---------\t-------\n")
	for _, p := range resp.Peers {
		accepts := "*"
		if len(p.AcceptedTypes) > 0 {
			accepts = strings.Join(p.AcceptedTypes, ",")
		}
		// Mark the row that represents this client.
		// For upstream rows (role=="upstream"), never mark as self.
		marker := ""
		if p.Source == mySource && p.Role != "upstream" {
			marker = "*"
		}
		// The server records the local peer's addr as "local".
		// When we are querying remotely over TCP, replace "local" with
		// the address we actually connected to so the client sees something useful.
		addr := p.Addr
		if addr == "local" && remoteAddr != "" {
			addr = remoteAddr
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			marker, p.Source, addr, p.Role, p.Clipboard,
			tsAge(p.ConnectedAt), tsAge(p.LastSeen), accepts,
		)
	}
	_ = tw.Flush()
}

func tsAge(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return "-"
	}
	t := ts.AsTime()
	if t.IsZero() {
		return "-"
	}
	return fmtAge(t)
}

// fmtAge returns a human-readable age string like "5s ago", "2m ago", or a
// clock time for ages over an hour. All callers are responsible for any
// surrounding context (e.g. parentheses); this function never double-appends "ago".
func fmtAge(t time.Time) string {
	age := time.Since(t).Round(time.Second)
	if age < time.Minute {
		return fmt.Sprintf("%ds ago", int(age.Seconds()))
	}
	if age < time.Hour {
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	}
	return t.Format("15:04:05")
}

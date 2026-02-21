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
		Long: `Displays all peers currently connected to the suffuse server.

If a local server or client daemon is running, the request is sent via the IPC
Unix socket. Pass --server to target a specific server directly over TCP.`,
		Args:    cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error { return bindViper(cmd, v) },
		RunE:    func(cmd *cobra.Command, _ []string) error { return runStatus(cmd, v) },
	}

	f := cmd.Flags()
	f.String("server", "localhost:8752", "suffuse server address (used when no daemon is running)")
	f.String("token", "", "shared secret")
	f.String("source", defaultSource(), "source identifier")
	f.Bool("json", false, "output raw JSON")
	addConfigFlag(cmd)

	return cmd
}

func runStatus(cmd *cobra.Command, v *viper.Viper) error {
	source := v.GetString("source")
	jsonOut := v.GetBool("json")

	var (
		conn      *grpc.ClientConn
		transport string
		err       error
	)

	if !cmd.Flags().Changed("server") && ipc.IsRunning() {
		conn, err = dialIPC()
		if err == nil {
			transport = fmt.Sprintf("ipc (%s)", ipc.SocketPath())
		} else {
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
		transport = fmt.Sprintf("tcp (%s)", serverAddr)
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

	printStatus(resp, source, transport)
	return nil
}

func printStatus(resp *pb.StatusResponse, mySource, transport string) {
	w := tabwriter.NewWriter(os.Stdout, 1, 0, 2, ' ', 0)

	if ci := resp.ClientInfo; ci != nil {
		// Connected via IPC client daemon — show upstream connection metadata.
		fmt.Fprintf(w, "Role:\tclient\n")
		fmt.Fprintf(w, "Transport:\t%s\n", transport)
		fmt.Fprintf(w, "Server:\t%s\n", ci.ServerAddr)
		if ci.ConnectedAt != nil && !ci.ConnectedAt.AsTime().IsZero() {
			t := ci.ConnectedAt.AsTime()
			fmt.Fprintf(w, "Connected:\t%s (%s ago)\n", t.UTC().Format(time.RFC3339), fmtAge(t))
		}
		if mySource == defaultSource() && ci.Source != "" {
			mySource = ci.Source
		}
	} else {
		fmt.Fprintf(w, "Transport:\t%s\n", transport)
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
		marker := ""
		if p.Source == mySource {
			marker = "*"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			marker, p.Source, p.Addr, p.Role, p.Clipboard,
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

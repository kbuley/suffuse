package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"go.klb.dev/suffuse/internal/crypto"
	"go.klb.dev/suffuse/internal/ipc"
	"go.klb.dev/suffuse/internal/message"
	"go.klb.dev/suffuse/internal/wire"
)

func newStatusCmd() *cobra.Command {
	v := viper.New()

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show connected peers and server status",
		Long: `Displays all peers currently connected to the suffuse server,
including source name, IP address, role, clipboard, and last-seen time.

If a local daemon is running, the request is proxied through it and enriched
with local connection metadata. Otherwise connects directly to the server.`,
		Args:    cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error { return bindViper(cmd, v) },
		RunE:    func(_ *cobra.Command, _ []string) error { return runStatus(v) },
	}

	f := cmd.Flags()
	f.String("server", "localhost:8752", "suffuse server address")
	f.String("token", "", "shared secret")
	f.String("source", defaultSource(), "source identifier")
	f.Bool("json", false, "output raw JSON")
	addConfigFlag(cmd)

	return cmd
}

func runStatus(v *viper.Viper) error {
	source := v.GetString("source")
	token := v.GetString("token")
	jsonOut := v.GetBool("json")

	var resp *message.Message

	// Try local daemon first
	if ipc.IsRunning() {
		conn, err := ipc.Dial()
		if err == nil {
			defer conn.Close()
			wc := wire.New(conn, nil)
			if err := wc.WriteMsg(&message.Message{
				Type:   message.TypeStatus,
				Source: source,
			}); err == nil {
				if msg, err := wc.ReadMsg(); err == nil {
					resp = msg
				}
			}
		}
	}

	// Fall back to direct server connection
	if resp == nil {
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
			}); err != nil {
				return fmt.Errorf("auth: %w", err)
			}
		}

		if err := wc.WriteMsg(&message.Message{
			Type:   message.TypeStatus,
			Source: source,
		}); err != nil {
			return fmt.Errorf("status request: %w", err)
		}

		resp, err = wc.ReadMsg()
		if err != nil {
			return fmt.Errorf("status response: %w", err)
		}
	}

	if resp.Error != "" {
		return fmt.Errorf("server error: %s", resp.Error)
	}

	if jsonOut {
		enc, _ := resp.Encode()
		fmt.Println(string(enc))
		return nil
	}

	printStatus(resp)
	return nil
}

func printStatus(resp *message.Message) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "Role:\t%s\n", resp.Role)
	if resp.Upstream != nil {
		fmt.Fprintf(w, "Server:\t%s\n", resp.Upstream.Addr)
		if !resp.Upstream.ConnectedAt.IsZero() {
			fmt.Fprintf(w, "Connected:\t%s (%s ago)\n",
				resp.Upstream.ConnectedAt.Format(time.RFC3339),
				time.Since(resp.Upstream.ConnectedAt).Round(time.Second),
			)
		}
	}
	fmt.Fprintf(w, "\n")
	_ = w.Flush()

	if len(resp.Peers) == 0 {
		fmt.Println("No peers connected.")
		return
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "SOURCE\tADDR\tROLE\tCLIPBOARD\tCONNECTED\tLAST SEEN\tACCEPTS\n")
	fmt.Fprintf(tw, "------\t----\t----\t---------\t---------\t---------\t-------\n")
	for _, p := range resp.Peers {
		accepts := "*"
		if len(p.AcceptedTypes) > 0 {
			accepts = strings.Join(p.AcceptedTypes, ",")
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			p.Source, p.Addr, p.Role, p.Clipboard,
			fmtAge(p.ConnectedAt), fmtAge(p.LastSeen), accepts,
		)
	}
	_ = tw.Flush()
}

func fmtAge(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	age := time.Since(t).Round(time.Second)
	if age < time.Minute {
		return fmt.Sprintf("%ds ago", int(age.Seconds()))
	}
	if age < time.Hour {
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	}
	return t.Format("15:04:05")
}

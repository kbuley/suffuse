// suffuse: shared clipboard over TCP.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"go.klb.dev/suffuse/internal/logging"
)

// Version is set at build time via -ldflags "-X main.Version=x.y.z".
var Version = "dev"

func main() {
	root := &cobra.Command{
		Use:   "suffuse",
		Short: "Shared clipboard over TCP",
		Long: `suffuse synchronises the system clipboard across machines,
containers, and SSH sessions over an encrypted TCP connection (opportunistic
TLS — self-signed cert, no CA required).

Run "suffuse server" on each host. Use --upstream to federate servers together.
Use "suffuse copy/paste/status" as CLI tools on any host running a server.

Config file search order (first found wins):
  /etc/suffuse/suffuse.toml
  $HOME/.config/suffuse/suffuse.toml
  path supplied via --config

All flags can be set via SUFFUSE_<FLAG> env vars or config-file keys.
See "suffuse server --help" for the full flag reference.`,
		SilenceUsage: true,
	}

	root.AddCommand(
		newServerCmd(),
		newCopyCmd(),
		newPasteCmd(),
		newStatusCmd(),
		newVersionCmd(),
	)

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Args:  cobra.NoArgs,
		Run: func(_ *cobra.Command, _ []string) {
			fmt.Printf("suffuse %s\n", Version)
		},
	}
}

// resolveLogging sets up the global slog logger after flags are parsed.
func resolveLogging(interactive bool, formatStr, levelStr string) {
	format := logging.ParseFormat(formatStr)
	level := logging.ParseLevel(levelStr)
	if levelStr == "" {
		if interactive {
			level = logging.ParseLevel("debug")
		} else {
			level = logging.ParseLevel("info")
		}
	}
	logging.Setup(format, level)
}

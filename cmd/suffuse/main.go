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
containers, and SSH sessions over a plain TCP connection.

Run "suffuse server" on the host and "suffuse client" on each
container or remote machine. The server also participates as a
local clipboard peer by default.`,
		SilenceUsage: true,
	}

	root.AddCommand(
		newServerCmd(),
		newClientCmd(),
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

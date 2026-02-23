package main

import (
	"fmt"
	"os"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"go.klb.dev/suffuse/internal/logging"
)

// bindViper wires a command's flags into a viper instance with the standard
// config file search order and SUFFUSE_* env var prefix.
//
// Precedence (lowest → highest): defaults → config file → SUFFUSE_* env vars → flags
func bindViper(cmd *cobra.Command, v *viper.Viper) error {
	configFlag, _ := cmd.Flags().GetString("config")
	if configFlag != "" {
		v.SetConfigFile(configFlag)
	} else {
		v.SetConfigName("suffuse")
		v.SetConfigType("toml")
		for _, p := range configPaths() {
			v.AddConfigPath(p)
		}
	}

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return fmt.Errorf("config: %w", err)
		}
	}

	v.SetEnvPrefix("SUFFUSE")
	v.AutomaticEnv()

	if err := v.BindPFlags(cmd.Flags()); err != nil {
		return fmt.Errorf("binding flags: %w", err)
	}
	return nil
}

// configPaths returns the ordered list of directories to search for suffuse.toml.
// Paths are ordered lowest → highest precedence (viper searches in reverse).
func configPaths() []string {
	var paths []string

	if runtime.GOOS == "windows" {
		// System-wide (ProgramData) — used by the Windows service running as SYSTEM
		// %ProgramData% is typically C:\ProgramData
		if pd := os.Getenv("ProgramData"); pd != "" {
			paths = append(paths, fmt.Sprintf(`%s\suffuse`, pd))
		}
		// Per-user roaming profile — used by interactive CLI
		// %APPDATA% is typically C:\Users\<user>\AppData\Roaming
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			paths = append(paths, fmt.Sprintf(`%s\suffuse`, appdata))
		}
	} else {
		// Unix: system-wide first, then per-user (higher precedence)
		paths = append(paths, "/etc/suffuse")
		if home, err := os.UserHomeDir(); err == nil {
			paths = append(paths, fmt.Sprintf("%s/.config/suffuse", home))
		}
	}

	return paths
}

// addLoggingFlags adds the standard logging flags to a command.
func addLoggingFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("no-background", false, "run interactively: tinter logs + debug level")
	cmd.Flags().String("log-format", "auto", "log format: auto|text|json")
	cmd.Flags().String("log-level", "", "log level: debug|info|warn|error (default: info for service, debug for interactive)")
}

// addConfigFlag adds the --config flag to a command.
func addConfigFlag(cmd *cobra.Command) {
	cmd.Flags().String("config", "", "path to config file (overrides auto-discovery)")
}

// setupLogging reads logging flags from viper and configures slog.
func setupLogging(v *viper.Viper) {
	interactive := v.GetBool("no-background") || logging.IsTTY(os.Stderr)
	resolveLogging(interactive, v.GetString("log-format"), v.GetString("log-level"))
}

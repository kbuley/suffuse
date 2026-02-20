// Package logging configures the global slog logger for suffuse binaries.
package logging

import (
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/pwntr/tinter"
)

// Format selects the log output format.
type Format string

const (
	FormatAuto Format = "auto"
	FormatText Format = "text"
	FormatJSON Format = "json"
)

// ParseFormat converts a string to a Format, returning FormatAuto for unknown values.
func ParseFormat(s string) Format {
	switch strings.ToLower(s) {
	case "text", "tint", "human":
		return FormatText
	case "json":
		return FormatJSON
	default:
		return FormatAuto
	}
}

// ParseLevel converts a string to a slog.Level, defaulting to Info.
func ParseLevel(s string) slog.Level {
	var l slog.Level
	if err := l.UnmarshalText([]byte(s)); err != nil {
		return slog.LevelInfo
	}
	return l
}

// IsTTY reports whether w is a terminal.
func IsTTY(w io.Writer) bool {
	if f, ok := w.(*os.File); ok {
		return isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
	}
	return false
}

// Setup configures the global slog logger. Call once after flag/viper parsing.
func Setup(format Format, level slog.Level) {
	w := os.Stderr
	useTint := format == FormatText || (format == FormatAuto && IsTTY(w))

	var h slog.Handler
	if useTint {
		h = tinter.NewHandler(w, &tinter.Options{
			Level:      level,
			TimeFormat: "15:04:05.000",
		})
	} else {
		h = slog.NewJSONHandler(w, &slog.HandlerOptions{
			Level: level,
		})
	}
	slog.SetDefault(slog.New(h))
}

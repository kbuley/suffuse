package hub

import (
	"context"
	"log/slog"

	pb "go.klb.dev/suffuse/gen/suffuse/v1"
)

// LogItems logs a clipboard event at DEBUG only.
// Copy/paste activity is high-frequency and not useful at INFO level.
func LogItems(event, source, clipboard string, items []*pb.ClipboardItem) {
	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	mimes := make([]string, len(items))
	for i, it := range items {
		mimes[i] = it.Mime
	}
	slog.Debug(event, "source", source, "clipboard", clipboard, "types", mimes)
	for _, it := range items {
		if it.Mime == "text/plain" {
			preview := string(it.Data)
			if len(preview) > 120 {
				preview = preview[:120] + "…"
			}
			slog.Debug("clipboard item", "mime", it.Mime, "preview", preview)
		} else {
			slog.Debug("clipboard item", "mime", it.Mime, "size_bytes", len(it.Data))
		}
	}
}

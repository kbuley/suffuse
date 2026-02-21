package hub

import (
	"context"
	"log/slog"

	pb "go.klb.dev/suffuse/gen/suffuse/v1"
)

// LogItems logs a clipboard event at INFO (source, clipboard, mime types) and
// DEBUG (text preview up to 120 chars, or byte size for binary items).
func LogItems(event, source, clipboard string, items []*pb.ClipboardItem) {
	mimes := make([]string, len(items))
	for i, it := range items {
		mimes[i] = it.Mime
	}
	slog.Info(event, "source", source, "clipboard", clipboard, "types", mimes)

	if !slog.Default().Enabled(context.Background(), slog.LevelDebug) {
		return
	}
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

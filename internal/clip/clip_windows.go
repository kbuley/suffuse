//go:build windows

package clip

// #cgo LDFLAGS: -luser32
//
// #include <windows.h>
// #include <stdlib.h>
//
// static HWND suffuse_create_listener_window();
// static void suffuse_pump_messages(HWND hwnd, int* changed);
//
// static LRESULT CALLBACK suffuse_wnd_proc(HWND hwnd, UINT msg, WPARAM wp, LPARAM lp) {
//     if (msg == WM_CLIPBOARDUPDATE) {
//         PostMessage(hwnd, WM_USER + 1, 0, 0);
//         return 0;
//     }
//     return DefWindowProc(hwnd, msg, wp, lp);
// }
//
// static HWND suffuse_create_listener_window() {
//     WNDCLASS wc = {0};
//     wc.lpfnWndProc   = suffuse_wnd_proc;
//     wc.hInstance     = GetModuleHandle(NULL);
//     wc.lpszClassName = "SuffuseClipboard";
//     RegisterClass(&wc);
//     HWND hwnd = CreateWindowEx(0, "SuffuseClipboard", NULL, 0,
//         0, 0, 0, 0, HWND_MESSAGE, NULL, GetModuleHandle(NULL), NULL);
//     AddClipboardFormatListener(hwnd);
//     return hwnd;
// }
//
// static void suffuse_pump_messages(HWND hwnd, int* changed) {
//     MSG msg;
//     *changed = 0;
//     while (PeekMessage(&msg, hwnd, 0, 0, PM_REMOVE)) {
//         if (msg.message == WM_USER + 1) {
//             *changed = 1;
//         }
//         TranslateMessage(&msg);
//         DispatchMessage(&msg);
//     }
// }
import "C"

import (
	"log/slog"
	"time"

	"golang.design/x/clipboard"

	"go.klb.dev/suffuse/internal/message"
)

func init() {
	if err := clipboard.Init(); err != nil {
		slog.Warn("clipboard init failed", "err", err)
	}
}

type windowsBackend struct {
	hwnd    C.HWND
	watchCh chan struct{}
	done    chan struct{}
}

// New returns the Windows clipboard backend using AddClipboardFormatListener.
func New() Backend {
	hwnd := C.suffuse_create_listener_window()
	b := &windowsBackend{
		hwnd:    hwnd,
		watchCh: make(chan struct{}, 1),
		done:    make(chan struct{}),
	}
	go b.pump()
	return b
}

func (b *windowsBackend) Name() string { return "Windows Clipboard" }

func (b *windowsBackend) pump() {
	t := time.NewTicker(50 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-b.done:
			return
		case <-t.C:
			var changed C.int
			C.suffuse_pump_messages(b.hwnd, &changed)
			if changed != 0 {
				select {
				case b.watchCh <- struct{}{}:
				default:
				}
			}
		}
	}
}

func (b *windowsBackend) Read() ([]message.Item, error) {
	var items []message.Item
	if text := clipboard.Read(clipboard.FmtText); text != nil {
		items = append(items, message.NewTextItem(string(text)))
	}
	if img := clipboard.Read(clipboard.FmtImage); img != nil {
		items = append(items, message.NewBinaryItem("image/png", img))
	}
	return items, nil
}

func (b *windowsBackend) Write(items []message.Item) error {
	for _, it := range items {
		data, err := it.Decode()
		if err != nil {
			continue
		}
		switch it.MIME {
		case "text/plain":
			clipboard.Write(clipboard.FmtText, data)
		case "image/png":
			clipboard.Write(clipboard.FmtImage, data)
		}
	}
	return nil
}

func (b *windowsBackend) Watch() <-chan struct{} { return b.watchCh }
func (b *windowsBackend) Close()                { close(b.done) }

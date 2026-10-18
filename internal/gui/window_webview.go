//go:build gui

package gui

/*
#cgo pkg-config: gtk+-3.0
#include <gtk/gtk.h>

// The program name becomes the Wayland app_id and X11 WM_CLASS; matching the
// installed focus.desktop lets docks and the compositor show our icon.
static void focus_identity(void) {
	g_set_prgname("focus");
}

static void focus_place(void *w, int x, int y, int above) {
	GtkWindow *win = GTK_WINDOW(w);
	gtk_window_set_icon_name(win, "focus");
	if (x >= 0 && y >= 0) {
		gtk_window_move(win, x, y);
	}
	gtk_window_set_keep_above(win, above);
}
*/
import "C"

import (
	"os"
	"runtime"

	webview "github.com/webview/webview_go"
)

// GTK must run on the process's main thread.
func init() { runtime.LockOSThread() }

func openWindow(url string, opts Options) error {
	// WebKitGTK's DMA-BUF renderer leaves the window blank or kills it with a
	// Wayland protocol error on some GPUs (seen on NVIDIA). The fallback
	// renderer is plenty for one small card.
	if _, set := os.LookupEnv("WEBKIT_DISABLE_DMABUF_RENDERER"); !set {
		_ = os.Setenv("WEBKIT_DISABLE_DMABUF_RENDERER", "1")
	}
	C.focus_identity()
	w := webview.New(false)
	defer w.Destroy()
	w.SetTitle("Focus")
	w.SetSize(opts.Width, opts.Height, webview.HintNone)
	above := C.int(0)
	if opts.AlwaysOnTop {
		above = 1
	}
	// Positioning works on X11. Wayland compositors ignore it; see README
	// for a window rule instead.
	C.focus_place(w.Window(), C.int(opts.X), C.int(opts.Y), above)
	if err := w.Bind("focusClose", func() { w.Terminate() }); err != nil {
		return err
	}
	w.Navigate(url)
	w.Run()
	return nil
}

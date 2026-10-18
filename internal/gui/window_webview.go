//go:build gui

package gui

/*
#cgo pkg-config: gtk+-3.0 webkit2gtk-4.0
#include <gtk/gtk.h>
#include <webkit2/webkit2.h>

// The program name becomes the Wayland app_id and X11 WM_CLASS; matching the
// installed focus.desktop lets docks and the compositor show our icon.
static void focus_identity(const char *name) {
	g_set_prgname(name);
}

static void focus_place(void *w, int x, int y, int above) {
	GtkWindow *win = GTK_WINDOW(w);
	gtk_window_set_icon_name(win, "focus");
	if (x >= 0 && y >= 0) {
		gtk_window_move(win, x, y);
	}
	gtk_window_set_keep_above(win, above);
}

// The clock window is built here rather than by webview so that the RGBA
// visual, the missing title bar and "don't take focus" are set before it is
// first shown; GTK ignores them afterwards.
static void *focus_clock_window(int w, int h, int above) {
	if (!gtk_init_check(NULL, NULL)) {
		return NULL;
	}
	GtkWidget *win = gtk_window_new(GTK_WINDOW_TOPLEVEL);
	GtkWindow *gw = GTK_WINDOW(win);
	gtk_window_set_title(gw, "Focus clock");
	gtk_window_set_icon_name(gw, "focus");
	gtk_window_set_decorated(gw, FALSE);
	gtk_window_set_default_size(gw, w, h);
	gtk_window_set_keep_above(gw, above);
	gtk_window_set_focus_on_map(gw, FALSE);
	gtk_window_set_skip_taskbar_hint(gw, TRUE);
	GdkVisual *visual = gdk_screen_get_rgba_visual(gtk_widget_get_screen(win));
	if (visual) {
		gtk_widget_set_visual(win, visual);
	}
	gtk_widget_set_app_paintable(win, TRUE);
	g_signal_connect(win, "destroy", G_CALLBACK(gtk_main_quit), NULL);
	return win;
}

// The clock has no title bar, so pressing anywhere on it moves the window
// and pressing near the bottom-right corner resizes it. Connected handlers
// run before WebKit's own, so the page never sees these clicks.
static gboolean focus_clock_press(GtkWidget *view, GdkEventButton *ev, gpointer data) {
	if (ev->type != GDK_BUTTON_PRESS || ev->button != 1) {
		return FALSE;
	}
	GtkWindow *win = GTK_WINDOW(data);
	int w = gtk_widget_get_allocated_width(view);
	int h = gtk_widget_get_allocated_height(view);
	const int grip = 18;
	if (ev->x >= w - grip && ev->y >= h - grip) {
		gtk_window_begin_resize_drag(win, GDK_WINDOW_EDGE_SOUTH_EAST, ev->button,
			(int)ev->x_root, (int)ev->y_root, ev->time);
	} else {
		gtk_window_begin_move_drag(win, ev->button, (int)ev->x_root, (int)ev->y_root, ev->time);
	}
	return TRUE;
}

static void focus_clock_show(void *w, int x, int y) {
	GtkWidget *win = GTK_WIDGET(w);
	GtkWidget *view = gtk_bin_get_child(GTK_BIN(win));
	if (view) {
		GdkRGBA clear = {0, 0, 0, 0};
		webkit_web_view_set_background_color(WEBKIT_WEB_VIEW(view), &clear);
		g_signal_connect(view, "button-press-event", G_CALLBACK(focus_clock_press), win);
	}
	if (x >= 0 && y >= 0) {
		gtk_window_move(GTK_WINDOW(win), x, y);
	}
	gtk_widget_show_all(win);
}
*/
import "C"

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"unsafe"

	webview "github.com/webview/webview_go"
)

// quitOnSignal closes the window on SIGTERM or SIGINT. WebKitGTK replaces
// Go's default handlers, which left the window running after `kill`; this
// re-registers them after the webview exists.
func quitOnSignal(w webview.WebView) {
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigs
		w.Dispatch(w.Terminate)
	}()
}

// GTK must run on the process's main thread.
func init() { runtime.LockOSThread() }

func openWindow(url string, opts Options) error {
	// WebKitGTK's DMA-BUF renderer leaves the window blank or kills it with a
	// Wayland protocol error on some GPUs (seen on NVIDIA). The fallback
	// renderer is plenty for one small card.
	if _, set := os.LookupEnv("WEBKIT_DISABLE_DMABUF_RENDERER"); !set {
		_ = os.Setenv("WEBKIT_DISABLE_DMABUF_RENDERER", "1")
	}
	if opts.Clock {
		return openClock(url, opts)
	}
	name := C.CString("focus")
	C.focus_identity(name)
	C.free(unsafe.Pointer(name))
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
	quitOnSignal(w)
	w.Navigate(url)
	w.Run()
	return nil
}

func openClock(url string, opts Options) error {
	name := C.CString("focus-clock")
	C.focus_identity(name)
	C.free(unsafe.Pointer(name))
	above := C.int(0)
	if opts.AlwaysOnTop {
		above = 1
	}
	win := C.focus_clock_window(C.int(opts.Width), C.int(opts.Height), above)
	if win == nil {
		return errors.New("cannot open a display")
	}
	w := webview.NewWindow(false, win)
	defer w.Destroy()
	if err := w.Bind("focusClose", func() { w.Terminate() }); err != nil {
		return err
	}
	quitOnSignal(w)
	w.Navigate(url)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	g := Geometry{X: opts.X, Y: opts.Y, Width: opts.Width, Height: opts.Height}
	if onHyprland() {
		// Hyprland ignores client positioning and focus hints, so place the
		// window through hyprctl and remember where the user moves it.
		go hyprPlace(ctx, os.Getpid(), g, opts.StatePath, opts.AlwaysOnTop)
		C.focus_clock_show(win, -1, -1)
	} else {
		C.focus_clock_show(win, C.int(g.X), C.int(g.Y))
	}
	w.Run()
	return nil
}

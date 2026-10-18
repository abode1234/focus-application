//go:build !gui

package gui

import (
	"fmt"
	"os"
	"os/signal"
)

// openWindow without WebKitGTK: serve the same UI for a regular browser.
func openWindow(url string, opts Options) error {
	if opts.Popup {
		// Opened by the daemon with nowhere to print the URL; the desktop
		// notification already tells the user time is up.
		return nil
	}
	fmt.Fprintf(os.Stderr, "This build has no GUI window (rebuild with: make build).\nOpen the UI in a browser instead:\n  %s\nPress Ctrl+C to stop.\n", url)
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)
	<-stop
	return nil
}

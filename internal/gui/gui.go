// Package gui is the popup window: a WebView showing HTML fragments that an
// in-process HTTP server renders for HTMX. The WebView cannot reach the
// daemon's Unix socket, so this server proxies to it through the client.
package gui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/abode1234/focus-cli/internal/client"
	"github.com/abode1234/focus-cli/internal/lockfile"
)

// Options controls the window.
type Options struct {
	X, Y          int // below zero leaves placement to the window manager
	Width, Height int
	AlwaysOnTop   bool
	// Popup marks a window the daemon opened on time-up. It closes itself
	// once the reflection is saved.
	Popup    bool
	LockPath string
}

// Run shows the window until it is closed. If a window is already open it
// returns immediately; that window updates itself from the event stream.
func Run(c *client.Client, opts Options) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err := c.Health(ctx)
	cancel()
	if err != nil {
		return err
	}
	if opts.LockPath != "" {
		os.MkdirAll(filepath.Dir(opts.LockPath), 0o700)
		release, err := lockfile.Acquire(opts.LockPath)
		if errors.Is(err, lockfile.ErrLocked) {
			fmt.Fprintln(os.Stderr, "focus: the window is already open")
			return nil
		}
		if err != nil {
			return err
		}
		defer release()
	}

	token, err := newToken()
	if err != nil {
		return err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	srv := newServer(c, token, opts.Popup)
	hs := &http.Server{Handler: srv.Handler()}
	go hs.Serve(ln)
	defer hs.Close()

	url := fmt.Sprintf("http://%s/?t=%s", ln.Addr(), token)
	return openWindow(url, opts)
}

func newToken() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

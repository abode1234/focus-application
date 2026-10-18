// Package daemon wires the store, engine, notifier and API into focusd.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/abode1234/focus-cli/internal/api"
	"github.com/abode1234/focus-cli/internal/clock"
	"github.com/abode1234/focus-cli/internal/config"
	"github.com/abode1234/focus-cli/internal/engine"
	"github.com/abode1234/focus-cli/internal/lockfile"
	"github.com/abode1234/focus-cli/internal/model"
	"github.com/abode1234/focus-cli/internal/notify"
	"github.com/abode1234/focus-cli/internal/store"
	"github.com/abode1234/focus-cli/internal/wire"
)

// ErrAlreadyRunning means another focusd holds the lock.
var ErrAlreadyRunning = errors.New("focusd is already running")

// Run serves until ctx is cancelled.
func Run(ctx context.Context, paths config.Paths, cfg config.Config, log *slog.Logger) error {
	for _, dir := range []string{filepath.Dir(paths.Socket), filepath.Dir(paths.Lock)} {
		if err := privateDir(dir); err != nil {
			return err
		}
	}
	unlock, err := lockfile.Acquire(paths.Lock)
	if errors.Is(err, lockfile.ErrLocked) {
		return ErrAlreadyRunning
	}
	if err != nil {
		return fmt.Errorf("lock %s: %w", paths.Lock, err)
	}
	defer unlock()

	if err := os.MkdirAll(filepath.Dir(paths.DB), 0o700); err != nil {
		return err
	}
	repo, err := store.Open(paths.DB)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer repo.Close()

	var notifier notify.Notifier = notify.Nop{}
	if cfg.Notify {
		d := notify.Desktop{Logger: log}
		if cfg.Sound {
			d.SoundFile = cfg.SoundFile
		}
		notifier = d
	}

	eng := engine.New(repo, clock.Real{}, engine.Options{
		AutoBreak:     cfg.AutoBreak,
		BreakDuration: cfg.BreakDuration.Duration,
		Logger:        log,
		OnTimeUp: func(s model.Session) {
			// Late means the daemon was down or restarted after the alarm.
			// Remind once about an unreflected session, but don't pop windows
			// or announce breaks that ended long ago.
			late := time.Since(s.EndsAt()) > time.Minute
			log.Info("time up", "id", s.ID, "kind", s.Kind, "goal", s.Goal, "late", late)
			if s.Kind == model.KindBreak {
				if !late {
					notifier.Notify(ctx, "Break over", "Ready for the next focus session?")
				}
				return
			}
			if cfg.Popup && !late {
				openPopup(cfg, log)
			}
			notifier.Notify(ctx, "Time's up: "+s.Goal, "What did you accomplish? Answer in the popup or run: focus finish \"…\"")
		},
	})
	if err := eng.Recover(ctx); err != nil {
		return fmt.Errorf("recover: %w", err)
	}

	// We hold the lock, so any existing socket file is stale.
	if err := os.Remove(paths.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	ln, err := net.Listen("unix", paths.Socket)
	if err != nil {
		return err
	}
	if err := os.Chmod(paths.Socket, 0o600); err != nil {
		ln.Close()
		return err
	}
	defer os.Remove(paths.Socket)

	presets := make([]int64, 0, len(cfg.Presets))
	for _, p := range cfg.Presets {
		presets = append(presets, int64(p.Duration/time.Second))
	}
	srv := &api.Server{Engine: eng, Logger: log, Config: wire.Config{
		DefaultSeconds: int64(cfg.DefaultDuration.Duration / time.Second),
		PresetSeconds:  presets,
		BreakSeconds:   int64(cfg.BreakDuration.Duration / time.Second),
		AutoBreak:      cfg.AutoBreak,
	}}
	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 5 * time.Second}

	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				eng.CheckTimeUp()
			}
		}
	}()
	log.Info("focusd listening", "socket", paths.Socket, "db", paths.DB)

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// SSE streams never finish on their own; Close ends them after the grace period.
	if err := hs.Shutdown(shutdownCtx); err != nil {
		hs.Close()
	}
	return nil
}

// privateDir creates dir if needed and refuses one that another user owns
// or can enter, since the socket inside it is the only access control.
func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || int(st.Uid) != os.Getuid() || info.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("%s must be owned by you with mode 0700 (it is %v)", dir, info.Mode().Perm())
	}
	return nil
}

// openPopup launches the GUI window in its own session, detached from the
// daemon. A popup that is already open keeps itself single-instance.
func openPopup(cfg config.Config, log *slog.Logger) {
	argv := cfg.PopupCommand
	if len(argv) == 0 {
		bin, err := focusBinary()
		if err != nil {
			log.Warn("cannot open popup", "err", err)
			return
		}
		argv = []string{bin, "gui", "--popup"}
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		log.Warn("open popup", "cmd", argv, "err", err)
		return
	}
	go cmd.Wait()
}

// focusBinary finds the `focus` CLI: this executable if it is focus, else a
// sibling of focusd, else $PATH.
func focusBinary() (string, error) {
	if exe, err := os.Executable(); err == nil {
		if filepath.Base(exe) == "focus" {
			return exe, nil
		}
		sibling := filepath.Join(filepath.Dir(exe), "focus")
		if _, err := os.Stat(sibling); err == nil {
			return sibling, nil
		}
	}
	return exec.LookPath("focus")
}

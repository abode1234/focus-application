// Package notify shows desktop notifications and plays sounds.
package notify

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Action is a button on a notification. The key "default" is the action
// most notification servers run when the notification itself is clicked.
type Action struct {
	Key   string
	Label string
}

// Notifier alerts the user. With actions it waits until the notification is
// closed and returns the key of the chosen action, or "" for none.
type Notifier interface {
	Notify(ctx context.Context, title, body string, actions ...Action) string
}

// Desktop uses notify-send (org.freedesktop.Notifications) and, optionally,
// pw-play or paplay for sound. Missing tools are logged, not fatal.
type Desktop struct {
	SoundFile string // empty disables sound
	Logger    *slog.Logger
}

func (d Desktop) Notify(ctx context.Context, title, body string, actions ...Action) string {
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	go d.playSound(ctx, log)

	args := []string{"--app-name=Focus", "--urgency=critical", "--icon=focus", title, body}
	for _, a := range actions {
		args = append(args, "--action="+a.Key+"="+a.Label)
	}
	if len(actions) == 0 {
		// Without actions notify-send returns at once; the timeout only
		// guards against a hung notification server.
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	out, err := exec.CommandContext(ctx, "notify-send", args...).Output()
	if err != nil {
		log.Warn("notify-send failed", "err", err)
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (d Desktop) playSound(ctx context.Context, log *slog.Logger) {
	if d.SoundFile == "" {
		return
	}
	if _, err := os.Stat(d.SoundFile); err != nil {
		log.Warn("sound file unavailable", "path", d.SoundFile, "err", err)
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for _, player := range []string{"pw-play", "paplay"} {
		if _, err := exec.LookPath(player); err != nil {
			continue
		}
		if err := exec.CommandContext(ctx, player, d.SoundFile).Run(); err != nil {
			log.Warn("play sound failed", "player", player, "err", err)
		}
		return
	}
	log.Warn("no sound player found (pw-play or paplay)")
}

// Nop does nothing.
type Nop struct{}

func (Nop) Notify(context.Context, string, string, ...Action) string { return "" }

// Package notify shows desktop notifications and plays sounds.
package notify

import (
	"context"
	"log/slog"
	"os"
	"os/exec"
	"time"
)

// Notifier alerts the user.
type Notifier interface {
	Notify(ctx context.Context, title, body string)
}

// Desktop uses notify-send (org.freedesktop.Notifications) and, optionally,
// pw-play or paplay for sound. Missing tools are logged, not fatal.
type Desktop struct {
	SoundFile string // empty disables sound
	Logger    *slog.Logger
}

func (d Desktop) Notify(ctx context.Context, title, body string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	log := d.Logger
	if log == nil {
		log = slog.Default()
	}
	cmd := exec.CommandContext(ctx, "notify-send", "--app-name=Focus", "--urgency=critical",
		"--icon=alarm-symbolic", title, body)
	if err := cmd.Run(); err != nil {
		log.Warn("notify-send failed", "err", err)
	}
	if d.SoundFile == "" {
		return
	}
	if _, err := os.Stat(d.SoundFile); err != nil {
		log.Warn("sound file unavailable", "path", d.SoundFile, "err", err)
		return
	}
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

func (Nop) Notify(context.Context, string, string) {}

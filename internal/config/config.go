// Package config loads settings and resolves XDG file locations.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/abode1234/focus-cli/internal/durfmt"
)

// Duration is a time.Duration that reads "25m" style strings from TOML.
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := durfmt.Parse(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

// Config is the contents of config.toml.
type Config struct {
	// DefaultDuration is used when `focus start` gets no duration.
	DefaultDuration Duration `toml:"default_duration"`
	// Presets are the durations offered by the TUI and GUI.
	Presets []Duration `toml:"presets"`
	// AutoBreak starts a break after finishing a focus session.
	AutoBreak     bool     `toml:"auto_break"`
	BreakDuration Duration `toml:"break_duration"`

	Notify    bool   `toml:"notify"`
	Sound     bool   `toml:"sound"`
	SoundFile string `toml:"sound_file"`
	// Popup opens the GUI window when a focus session's time is up.
	Popup bool `toml:"popup"`
	// PopupCommand overrides the command used to open the window.
	PopupCommand []string `toml:"popup_command"`

	GUI GUI `toml:"gui"`

	// ClockOnStart opens the clock window when a focus session starts.
	ClockOnStart bool `toml:"clock_on_start"`
	// Clock is where the clock window first appears. Once you move or
	// resize it, it reopens where you left it.
	Clock GUI `toml:"clock"`
}

// GUI controls the popup window. X/Y below zero leave placement to the
// window manager.
type GUI struct {
	X      int `toml:"x"`
	Y      int `toml:"y"`
	Width  int `toml:"width"`
	Height int `toml:"height"`
	// AlwaysOnTop asks the window manager to keep the window above others.
	AlwaysOnTop bool `toml:"always_on_top"`
}

// Default returns the built-in settings.
func Default() Config {
	return Config{
		DefaultDuration: Duration{25 * time.Minute},
		Presets:         []Duration{{25 * time.Minute}, {50 * time.Minute}},
		AutoBreak:       true,
		BreakDuration:   Duration{5 * time.Minute},
		Notify:          true,
		Sound:           true,
		SoundFile:       "/usr/share/sounds/freedesktop/stereo/complete.oga",
		Popup:           true,
		GUI:             GUI{X: -1, Y: -1, Width: 420, Height: 520, AlwaysOnTop: true},
		ClockOnStart:    true,
		Clock:           GUI{X: -1, Y: -1, Width: 260, Height: 110, AlwaysOnTop: true},
	}
}

// Load reads path over the defaults. A missing file is not an error.
func Load(path string) (Config, error) {
	cfg := Default()
	_, err := toml.DecodeFile(path, &cfg)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("config %s: %w", path, err)
	}
	if cfg.DefaultDuration.Duration <= 0 {
		cfg.DefaultDuration = Default().DefaultDuration
	}
	if cfg.BreakDuration.Duration <= 0 {
		cfg.BreakDuration = Default().BreakDuration
	}
	if len(cfg.Presets) == 0 {
		cfg.Presets = Default().Presets
	}
	return cfg, nil
}

// Paths are the files the app uses.
type Paths struct {
	DB      string
	Config  string
	Socket  string
	Lock    string
	GUILock string
	// ClockLock keeps a single clock window; ClockState remembers where it was.
	ClockLock  string
	ClockState string
}

// ResolvePaths follows the XDG base directory spec. FOCUS_SOCKET overrides
// the socket path for clients and the daemon alike.
func ResolvePaths() Paths {
	home, _ := os.UserHomeDir()
	data := envOr("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	conf := envOr("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	state := envOr("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	runtime := envOr("XDG_RUNTIME_DIR", filepath.Join(os.TempDir(), fmt.Sprintf("focus-%d", os.Getuid())))
	run := filepath.Join(runtime, "focus")
	return Paths{
		DB:      filepath.Join(data, "focus", "focus.db"),
		Config:  filepath.Join(conf, "focus", "config.toml"),
		Socket:  envOr("FOCUS_SOCKET", filepath.Join(run, "focusd.sock")),
		Lock:    filepath.Join(run, "focusd.lock"),
		GUILock: filepath.Join(run, "gui.lock"),

		ClockLock:  filepath.Join(run, "clock.lock"),
		ClockState: filepath.Join(state, "focus", "clock.json"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

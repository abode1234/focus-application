package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultDuration.Duration != 25*time.Minute || !cfg.AutoBreak {
		t.Fatalf("defaults: %+v", cfg)
	}
}

func TestLoadOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte(`
default_duration = "50m"
presets = ["15m", "45"]
auto_break = false
break_duration = "10m"

[gui]
x = 100
y = 50
`), 0o600)
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultDuration.Duration != 50*time.Minute || cfg.AutoBreak || cfg.BreakDuration.Duration != 10*time.Minute {
		t.Fatalf("overrides: %+v", cfg)
	}
	if len(cfg.Presets) != 2 || cfg.Presets[1].Duration != 45*time.Minute {
		t.Fatalf("presets: %+v", cfg.Presets)
	}
	if cfg.GUI.X != 100 || cfg.GUI.Width != 420 {
		t.Fatalf("gui: %+v", cfg.GUI)
	}
}

func TestLoadRejectsBadDuration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	os.WriteFile(path, []byte(`default_duration = "soon"`), 0o600)
	if _, err := Load(path); err == nil {
		t.Fatal("bad duration accepted")
	}
}

func TestResolvePaths(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/d")
	t.Setenv("XDG_CONFIG_HOME", "/c")
	t.Setenv("XDG_RUNTIME_DIR", "/r")
	t.Setenv("FOCUS_SOCKET", "")
	p := ResolvePaths()
	if p.DB != "/d/focus/focus.db" || p.Config != "/c/focus/config.toml" || p.Socket != "/r/focus/focusd.sock" {
		t.Fatalf("paths: %+v", p)
	}
}

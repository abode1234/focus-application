# focus

Focus sessions for Linux. You set a goal and a duration. When time is up you get a notification and a popup asking what you accomplished, and the answer goes into your history. The timer lives in a background daemon, so closing the terminal or the window never loses it.

```
focus start "Write the design" 25m
```

Three interfaces talk to the same daemon and see the same state live:

| Interface | Command |
|---|---|
| CLI | `focus start`, `status`, `finish`, `abandon`, `break`, `log`, `stats` |
| TUI | `focus tui` |
| GUI popup | `focus gui` (also opens by itself when a session's time is up) |

## Install

Requirements: Go 1.27+, gcc, `webkit2gtk-4.1` and `gtk3` development files for the GUI, and `libnotify` (`notify-send`) for notifications.

```
make install
```

This builds `focusd` and `focus` into `~/.local/bin`, installs a `systemd --user` unit, and starts the daemon. It also adds **Focus** to your application menu with its icon (`~/.local/share/applications` and the hicolor icon theme), so it shows up in GNOME, KDE, rofi, wofi, fuzzel and other launchers that follow the freedesktop spec. Opening it from the menu shows the window; if the daemon is not running yet, the window says so and catches up when it starts.

It is safe to rerun after pulling changes. `make uninstall` removes everything except your history. Set `PREFIX` to install elsewhere, for example `make install PREFIX=$HOME/apps`.

No WebKitGTK? `make build-nogui` builds without CGO. The CLI and TUI work fully, and `focus gui` prints a URL you can open in a browser.

## Usage

```
focus start "goal" 25m       start a session (25m, 1h30m, or bare minutes like 50)
focus start Inbox zero       goal without quotes; duration defaults to config
focus start 25m              rejected: a duration alone is not a goal
focus status [--json]        current session and remaining time
focus finish "what I did"    finish; prompts for the reflection if omitted
focus finish --no-break      finish without the automatic break
focus abandon                abandon (not counted in stats)
focus break [5m]             start a break
focus log [--week|--all]     history
focus stats [--week]         totals, with a per-day chart for the week
focus tui                    terminal UI
focus gui [--x --y --w --h]  popup window
focus daemon                 run the daemon in the foreground
```

When time runs out the session stays open until you write a reflection, even if every interface was closed or the machine rebooted. Stats count the planned duration for a session that ran its course and the time actually spent for one finished early. After you finish a focus session a break starts automatically (configurable). Starting a new session during a break ends the break.

`focus status --json` gives `ends_at` and `remaining_seconds` for status bars such as waybar.

## Configuration

`~/.config/focus/config.toml`. Every key is optional. The daemon reads it at start, so run `systemctl --user restart focusd` after editing.

```toml
default_duration = "25m"
presets = ["25m", "50m"]      # offered by the TUI and GUI, plus "custom"
auto_break = true
break_duration = "5m"

notify = true
sound = true
sound_file = "/usr/share/sounds/freedesktop/stereo/complete.oga"
popup = true                  # open the window when time is up

[gui]
x = -1                        # -1 lets the window manager decide
y = -1
width = 420
height = 520
always_on_top = true
```

### Wayland and Hyprland

Wayland does not let applications place their own windows, so `x`/`y` only work on X11. On Hyprland (0.53 or newer) add a rule instead. This one was checked on Hyprland 0.56:

```
windowrule = match:title ^(Focus)$, float on, pin on, size 420 520, move (monitor_w-440) 40
```

The popup is opened by `focusd`, so the systemd user environment needs `WAYLAND_DISPLAY`. Most Hyprland setups export it already. If the popup never appears, add `exec-once = dbus-update-activation-environment --systemd WAYLAND_DISPLAY XDG_CURRENT_DESKTOP` to `hyprland.conf`.

The GUI sets `WEBKIT_DISABLE_DMABUF_RENDERER=1` unless you set it yourself. Without it, WebKitGTK shows a blank window or exits with "Error 71 (Protocol error)" on some GPUs, NVIDIA included.

## Files

| File | Path |
|---|---|
| Database | `$XDG_DATA_HOME/focus/focus.db` (SQLite, WAL) |
| Config | `$XDG_CONFIG_HOME/focus/config.toml` |
| Socket | `$XDG_RUNTIME_DIR/focus/focusd.sock` (mode 0600), overridable with `FOCUS_SOCKET` |
| Lock | `$XDG_RUNTIME_DIR/focus/focusd.lock` |

## Architecture

One daemon owns the state. Every interface is a thin client over HTTP+JSON on a Unix socket, and listens for changes over Server-Sent Events. Clients count down locally from `ends_at`, so the daemon sends no per-second ticks.

```
cmd/focusd, cmd/focus     entry points
internal/engine           state machine, time-up timer, stats; no HTTP or SQL
internal/store            SQLite queries and embedded migrations
internal/api              HTTP routes and SSE
internal/client           the library every interface uses
internal/cli, tui, gui    interfaces
internal/daemon           wiring, lock, notifications, popup launch
internal/clock            time abstraction with a fake for tests
```

Two rules are enforced by the database: only one session runs at a time (a partial unique index), and every transition is `UPDATE ... WHERE status = 'running'`, so two interfaces finishing at once cannot both win.

The GUI is a WebView. A WebView cannot reach a Unix socket, so `focus gui` runs a small loopback server that renders HTML fragments for HTMX and proxies to the daemon. A random per-window token, set as a cookie, keeps other local processes and web pages from using that port.

The daemon API is easy to poke at with curl:

```
curl --unix-socket $XDG_RUNTIME_DIR/focus/focusd.sock http://x/v1/status
curl --unix-socket $XDG_RUNTIME_DIR/focus/focusd.sock -d '{"goal":"Read","planned_seconds":1500}' http://x/v1/sessions
curl -N --unix-socket $XDG_RUNTIME_DIR/focus/focusd.sock http://x/v1/events
```

| Method | Path | |
|---|---|---|
| GET | `/v1/status` | current session, remaining time, `time_up` |
| POST | `/v1/sessions` | start `{goal, planned_seconds}` |
| POST | `/v1/sessions/{id}/finish` | `{reflection, start_break?}` |
| POST | `/v1/sessions/{id}/abandon` | |
| POST | `/v1/breaks` | `{planned_seconds?}` |
| GET | `/v1/sessions?from=&to=` | history (Unix seconds) |
| GET | `/v1/stats?range=day\|week` | |
| GET | `/v1/config` | presets and break settings for clients |
| GET | `/v1/events` | SSE: `session.started`, `session.time_up`, `session.finished`, `session.abandoned`, `heartbeat` |
| GET | `/v1/health` | |

Errors are `{"error": "..."}` with 409 (already running, or not running), 404, 422 (invalid input), or 400 (malformed JSON or unknown fields). Request bodies are optional where every field is.

## Development

```
make check          gofmt, go vet (with and without the gui tag), golangci-lint, tests with -race
make build          bin/focusd and bin/focus (GUI)
make run-daemon     daemon on throwaway paths under /tmp/focus-dev
scripts/e2e.sh      end-to-end checks of the real binaries in a sandbox
make icons          re-render the PNG icons from packaging/icons/focus.svg (needs rsvg-convert)
```

`webview_go` asks pkg-config for `webkit2gtk-4.0`, while current distros ship 4.1. The Makefile adds `packaging/pkgconfig` to `PKG_CONFIG_PATH` to map one to the other. Use `make` rather than a bare `go build -tags gui`, or set that path yourself.

The GUI and notifications are checked by hand with [docs/manual-checklist.md](docs/manual-checklist.md). Design decisions live in [docs/decisions](docs/decisions).

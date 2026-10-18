# Focus — Project Plan

A focus-session app for Linux that runs in the background and is controlled from three interfaces:

- **CLI** — command-line interface
- **TUI** — interactive terminal interface
- **GUI** — popup window with a countdown timer

> Status: planning draft. No code yet.

---

## 1. Goal

A personal tool for working in focus sessions with a clear goal and duration, recording what you accomplished, without the timer ever being lost when a window is closed.

### MVP success criterion

I can type in a terminal:

```
focus start "Write the design" 25m
```

then close the terminal. After 25 minutes I get a notification, a window pops up asking what I accomplished, and my answer is saved to the history.

---

## 2. Requirements

### Functional

| ID | Requirement |
|---|---|
| F1 | Start a session with a goal and a duration |
| F2 | Show status and remaining time from any interface |
| F3 | Finish a session with a reflection, or abandon it |
| F4 | System notification when time is up |
| F5 | Popup window with a countdown |
| F6 | Session history and daily/weekly stats |
| F7 | Control the window's position and size |

### Non-functional

| ID | Requirement |
|---|---|
| N1 | Timer stays correct after closing interfaces or rebooting |
| N2 | Local data only, no external network access |
| N3 | Single user, no login |
| N4 | All interfaces see the same state in real time |
| N5 | One-command install, auto-start on login |
| N6 | Very low resource usage in the background |

---

## 3. Architecture

The core idea: **one engine owns the state; thin clients only display it.**

```
   ┌────────┐     ┌────────┐     ┌──────────────┐
   │  CLI   │     │  TUI   │     │  GUI popup   │
   └───┬────┘     └───┬────┘     └──────┬───────┘
       │              │                 │
       └──────────────┼─────────────────┘
                      │   HTTP + JSON + SSE
                      │   over a Unix socket
                      ▼
            ┌───────────────────┐
            │      focusd       │   background daemon
            │  api → engine     │
            │        → store    │
            └─────────┬─────────┘
                      │
          ┌───────────┴───────────┐
          ▼                       ▼
       SQLite              System notifications
                           (D-Bus / notify-send)
```

### Responsibilities

| Component | Responsible for | Does NOT |
|---|---|---|
| `engine` | State machine, timer, business rules | Know about HTTP or SQL |
| `store` | Queries and migrations | Contain business rules |
| `api` | Map requests to engine calls, broadcast events | Render any UI |
| `client` | Shared library all clients use to talk to the daemon | Hold state |
| `cli` / `tui` / `gui` | Display and input only | Act as the source of truth for time |

---

## 4. Design Decisions

| # | Decision | Choice | Reason | Rejected alternative |
|---|---|---|---|---|
| D1 | Runtime model | Daemon + thin clients | State lives in one place; alerts don't depend on an open window | Each interface independent |
| D2 | Transport | Unix domain socket, mode `0600` | Local, secured by file permissions, no network port | TCP on `127.0.0.1` |
| D3 | Protocol | HTTP + JSON | Simple standard, in the stdlib, easy to test with `curl` | gRPC (heavier than needed) |
| D4 | Live updates | SSE (Server-Sent Events) | One direction is enough; simpler than WebSocket | WebSocket |
| D5 | Storage | SQLite in WAL mode | Single file, guaranteed atomicity | JSON file |
| D6 | Time source of truth | `started_at + planned_seconds` in the daemon | Survives closed clients and reboots | In-memory counter |
| D7 | Visible countdown | Each client counts locally from `ends_at` | No need for a message every second | Broadcasting a `tick` every second |
| D8 | Conflict prevention | `UPDATE ... WHERE status = 'running'` | Atomic; handles double-clicks from two interfaces | Read-then-write |
| D9 | One session at a time | Partial unique index on `running` | The database itself enforces the rule | Check in code only |
| D10 | Process management | `systemd --user` unit | Auto-start and restart on crash | Manual start |
| D11 | GUI | WebView + HTMX | Reuses HTMX; easy to design | Native GUI toolkit |

### Note on D11: where does HTMX fit?

A WebView can't talk to a Unix socket directly. So the GUI process acts as a small proxy:

```
WebView ──HTTP──► internal server in the GUI process (127.0.0.1, random port)
                      │  returns HTML fragments for HTMX
                      ▼
                 client ──Unix socket──► focusd
```

The daemon always returns JSON; only the GUI turns it into HTML.

---

## 5. Data Model

```sql
CREATE TABLE sessions (
  id              INTEGER PRIMARY KEY,
  goal            TEXT    NOT NULL,
  planned_seconds INTEGER NOT NULL CHECK (planned_seconds > 0),
  started_at      INTEGER NOT NULL,          -- Unix time
  ended_at        INTEGER,
  status          TEXT    NOT NULL
                  CHECK (status IN ('running', 'completed', 'abandoned')),
  reflection      TEXT
);

-- Only one running session at any time
CREATE UNIQUE INDEX one_running
  ON sessions(status) WHERE status = 'running';

-- For daily/weekly stats
CREATE INDEX idx_sessions_started ON sessions(started_at);
```

### File locations (XDG spec)

| File | Path |
|---|---|
| Database | `$XDG_DATA_HOME/focus/focus.db` |
| Config | `$XDG_CONFIG_HOME/focus/config.toml` |
| Socket | `$XDG_RUNTIME_DIR/focus/focusd.sock` |
| Single-instance lock | `$XDG_RUNTIME_DIR/focus/focusd.lock` |

---

## 6. State Machine

```
                start
   (none) ──────────► running ──── finish ────► completed
                         │
                         └──────── abandon ───► abandoned
```

- No other transitions exist.
- Every transition is an `UPDATE` conditioned on the previous state.
- If `RowsAffected() == 0`, the transition is rejected (the session already ended).

### Decision: time runs out while the app is closed

The session stays `running` until you write your reflection. When any interface opens after time is up, it shows: "Time's up — what did you accomplish?". The duration counted in stats is `planned_seconds`, not the time you clicked the button.

---

## 7. Daemon API

All over the Unix socket.

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/v1/status` | Current session and remaining time |
| `POST` | `/v1/sessions` | Start a session `{goal, planned_seconds}` |
| `POST` | `/v1/sessions/{id}/finish` | Finish `{reflection}` |
| `POST` | `/v1/sessions/{id}/abandon` | Abandon |
| `GET` | `/v1/sessions?from=&to=` | History |
| `GET` | `/v1/stats?range=day\|week` | Stats |
| `GET` | `/v1/events` | Event stream (SSE) |
| `GET` | `/v1/health` | Liveness check |

### Broadcast events

| Event | When |
|---|---|
| `session.started` | On start |
| `session.time_up` | When the duration elapses |
| `session.finished` | On finish |
| `session.abandoned` | On abandon |
| `heartbeat` | Every 15 seconds to keep the connection alive |

### Error codes

| Code | Meaning |
|---|---|
| `409` | A session is already running, or the session is not `running` |
| `404` | Session not found |
| `422` | Invalid input (zero duration, empty goal) |

---

## 8. CLI Commands

```
focus start "goal" 25m       start a session
focus status                 status and remaining time
focus finish "what I did"    finish
focus abandon                abandon
focus log [--week]           history
focus stats [--week]         stats
focus tui                    open the terminal UI
focus gui [--x --y --w --h]  open the popup window
focus daemon                 run the daemon manually (for development)
```

Machine-readable output:

```
focus status --json
```

Useful later for showing the countdown in a status bar (e.g. `waybar` or `polybar`).

---

## 9. Tools

### Language and core libraries

| Purpose | Tool | Reason |
|---|---|---|
| Language | Go (latest stable) | Single binary, easy concurrency |
| HTTP and routing | Standard `net/http` | Modern router supports `POST /v1/sessions/{id}/finish` |
| SQLite | `modernc.org/sqlite` | No CGO, keeps builds simple |
| Migrations | SQL files embedded with `embed` + a small runner | No external tool needed at first |
| Logging | Standard `log/slog` | Structured logs, no dependency |
| Config | `BurntSushi/toml` | Readable format |

### Interfaces

| Interface | Tool | Note |
|---|---|---|
| CLI | `spf13/cobra` | Subcommands and shell completion |
| TUI | `charmbracelet/bubbletea` | Elm architecture, easy to test |
| TUI styling | `charmbracelet/lipgloss` and `bubbles` | Colors and ready components (timer, input) |
| GUI | `webview/webview_go` | Lightweight window; needs CGO and WebKitGTK |
| GUI content | `html/template` + HTMX | HTML fragments from the internal proxy |

Alternative GUI if we need more control later: Wails.

### System integration

| Purpose | Tool |
|---|---|
| Notifications | `godbus/dbus` via `org.freedesktop.Notifications` (or `notify-send` to start) |
| Sound (optional) | `pw-play` or `paplay` |
| Auto-start | `systemd --user` unit |
| App menu entry | `.desktop` file |

### Development tools

| Purpose | Tool |
|---|---|
| Build and tasks | `make` |
| Static analysis | `golangci-lint` |
| Formatting | `gofmt` |
| Testing | `go test` with `-race` |
| TUI testing | `charmbracelet/x/exp/teatest` |
| Manual API testing | `curl --unix-socket` |
| CI | GitHub Actions |
| Packaging (later) | `goreleaser` and `nfpm` for `.deb` / `.rpm` |

### Build-time system requirements

```
Go
gcc                      # GUI only (CGO)
webkit2gtk-4.1 (dev)     # GUI only
libnotify                # if using notify-send
```

---

## 10. Project Layout

```
focus/
├── cmd/
│   ├── focusd/            background daemon
│   └── focus/             CLI, including the tui and gui subcommands
├── internal/
│   ├── engine/            states, timer, rules
│   ├── store/             SQLite and migrations
│   │   └── migrations/
│   ├── api/               HTTP over the socket, SSE
│   ├── client/            shared client for all interfaces
│   ├── notify/            notifications and sound
│   ├── config/            config loading and XDG paths
│   ├── clock/             time abstraction (for testing)
│   ├── tui/
│   └── gui/
│       ├── templates/
│       └── static/        htmx.min.js and CSS
├── packaging/
│   ├── focusd.service
│   └── focus.desktop
├── docs/
│   └── decisions/         decision records
├── Makefile
└── README.md
```

### Why a `clock` package?

The engine depends on time. If it calls `time.Now()` directly, we can't test "25 minutes have passed" without waiting 25 minutes. So we inject an interface:

```go
type Clock interface {
    Now() time.Time
    AfterFunc(d time.Duration, f func()) Timer
}
```

Tests use a fake clock that we advance manually.

---

## 11. Workflow

### A) Development workflow

```
idea or task
   │
   ▼
new branch:  feat/engine-state-machine
   │
   ▼
write the test first → write the code → make check
   │
   ▼
small commit using Conventional Commits
   │
   ▼
Pull Request → CI runs checks → merge into main
```

Commit message format:

```
feat(engine): add finish transition
fix(api): return 409 when session not running
test(store): cover one_running constraint
```

### B) `make` targets

| Target | Purpose |
|---|---|
| `make build` | Build `focusd` and `focus` |
| `make build-nogui` | Build without CGO (no GUI) |
| `make test` | Tests with `-race` |
| `make lint` | `golangci-lint` |
| `make check` | `lint` then `test` (before every commit) |
| `make run-daemon` | Run the daemon locally with temp paths |
| `make install` | Install files and enable the systemd unit |
| `make uninstall` | Remove everything |

### C) Install workflow

```
make install
  ├── ~/.local/bin/focusd
  ├── ~/.local/bin/focus
  ├── ~/.config/systemd/user/focusd.service
  ├── ~/.local/share/applications/focus.desktop
  └── systemctl --user enable --now focusd
```

### D) Daily usage workflow

```
log in → focusd starts automatically
   │
   ▼
focus start "task" 50m      (or from the TUI, or a keyboard shortcut that opens the GUI)
   │
   ▼
countdown appears in any open interface
   │
   ▼
time's up → notification + popup window
   │
   ▼
write what you accomplished → saved → stats update
```

### E) Decision records

Every new architectural decision gets a short file in `docs/decisions/`:

```
0001-daemon-and-thin-clients.md
0002-unix-socket-transport.md
```

Each contains: context, decision, alternatives, consequences.

---

## 12. Implementation Phases

Each phase has a clear **definition of done**; we don't move on until it's met.

### Phase 0: Foundation

- Repository, layout, `Makefile`, CI.
- **Done when:** `make check` passes on an empty project in CI.

### Phase 1: Engine and storage

- State machine, `clock` package, migrations, queries.
- **Done when:** tests cover every transition, double-finish, the single-session rule, and time-up with a fake clock.

### Phase 2: Daemon

- `focusd`, socket, routes, SSE, single-instance lock.
- **Done when:** I can start and finish a session with `curl --unix-socket` and see events on `/v1/events`.

### Phase 3: CLI

- `client` package and all commands from section 8.
- **Done when:** a full scenario works from the terminal alone, including `--json`.

### Phase 4: Notifications

- Notification on `time_up`, optional sound.
- **Done when:** I close everything and the notification still arrives on time.

### Phase 5: TUI

- Live countdown, start/finish actions, history view.
- **Done when:** opening the TUI during a session started from the CLI shows it immediately, and finishing from one updates the other.

### Phase 6: GUI

- WebView window with HTMX, countdown, reflection prompt on time-up, position and size options.
- **Done when:** the window pops up automatically on `time_up` at the configured position.

### Phase 7: Packaging and install

- systemd unit, `.desktop` file, `make install` / `uninstall`.
- **Done when:** on a clean machine, one command installs everything and it works after re-login.

### Phase 8 (later)

- Countdown in the status bar.
- Daily streaks.
- History export.
- `.deb` / `.rpm` packages.

---

## 13. Testing Strategy

| Level | What we test | Tool |
|---|---|---|
| Unit | Engine with a fake clock | `go test` |
| Storage | Constraints and indexes on in-memory SQLite | `go test` |
| Integration | API over a temporary socket | `httptest` with a socket in a temp dir |
| Concurrency | Two simultaneous finish requests; only one succeeds | `go test -race` |
| TUI | Key handling and rendering | `teatest` |
| Manual | GUI and notifications | Checklist in `docs/` |

---

## 14. Risks and Edge Cases

| Case | Handling |
|---|---|
| Daemon not running when a command is issued | Clear message suggesting `systemctl --user start focusd` (socket activation later) |
| Two daemon instances | File lock (`flock`) |
| Machine shut down mid-session | On boot, state is recomputed from `started_at` |
| System clock changes | Acceptable for a personal app; documented |
| Wayland blocks window positioning | Window rule in the desktop environment, documented per environment |
| WebKitGTK missing | `build-nogui` target; CLI and TUI still work |
| SSE connection drops | Client reconnects automatically and re-fetches `/v1/status` |

---

## 15. Open Questions

1. Desktop environment and session type:

```
echo $XDG_SESSION_TYPE $XDG_CURRENT_DESKTOP
```

This determines how window positioning is handled.

2. Default session lengths: 25 and 50 minutes? yes and i can enter a custom one
3. Automatic break after a session in v1, or later? yes

---

## 16. Out of Scope (for now)

- Login and multiple users.
- Sync across devices.
- Mobile app.
- Website/app blocking.

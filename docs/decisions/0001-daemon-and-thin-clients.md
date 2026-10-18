# 0001. Daemon and thin clients

**Context.** The timer must survive closed terminals and windows, and alerts must fire with no interface open. Three interfaces (CLI, TUI, GUI) need to agree on state.

**Decision.** `focusd` owns all state and the time-up timer. Interfaces are thin clients built on `internal/client`. They render state and send commands, and never decide time themselves. Clients count down locally from `ends_at` and learn about changes over SSE.

**Alternatives.** Each interface keeps its own timer. Rejected: no alert without an open interface, and the interfaces drift apart.

**Consequences.** The daemon must be running (systemd user unit). Clients print a clear message when it is not. The daemon also launches the popup, so it needs the graphical session's environment.

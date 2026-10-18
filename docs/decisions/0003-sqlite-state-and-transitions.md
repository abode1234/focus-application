# 0003. SQLite holds the truth; the database enforces the rules

**Context.** State must survive crashes and reboots, and two interfaces may act at the same moment.

**Decision.** SQLite in WAL mode through `modernc.org/sqlite` (no CGO). Time is stored as `started_at + planned_seconds`, never as a counter. A partial unique index allows one `running` row. Every transition is `UPDATE ... WHERE id = ? AND status = 'running'`, and zero affected rows means the transition is rejected. On start, the engine re-arms its timer from the running row. A focus session whose time passed while the daemon was down fires time-up right away.

**Alternatives.** A JSON file (no atomic conditional update). Read-then-write checks in Go (races between interfaces).

**Consequences.** `internal/store` stays rule-free, and the rules it relies on are visible in one migration. Changes to the system clock shift running sessions. That is acceptable for a personal tool.

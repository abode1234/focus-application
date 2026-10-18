# 0005. Breaks are sessions of kind "break"

**Context.** Version 1 includes an automatic break after each focus session.

**Decision.** A `kind` column (`focus` or `break`) on `sessions`. Breaks share the one-running rule, the timer and the events. Unlike focus sessions, a break completes on its own at time-up and needs no reflection. Starting a focus session during a break ends the break. Stats count focus sessions only.

**Alternatives.** Break state only in the daemon's memory. Rejected: a reboot would lose it, which breaks requirement N1.

**Consequences.** One state machine covers both. Clients branch on `kind` for display only.

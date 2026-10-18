#!/usr/bin/env bash
# End-to-end check of the real binaries in an isolated XDG sandbox.
# Usage: scripts/e2e.sh [bin-dir]   (default: bin, built by `make build`)
set -euo pipefail

BIN=$(cd "${1:-bin}" && pwd)
SANDBOX=$(mktemp -d)
export XDG_DATA_HOME=$SANDBOX/data XDG_CONFIG_HOME=$SANDBOX/config XDG_RUNTIME_DIR=$SANDBOX/run
unset FOCUS_SOCKET
SOCK=$XDG_RUNTIME_DIR/focus/focusd.sock
mkdir -p "$XDG_CONFIG_HOME/focus" "$XDG_RUNTIME_DIR"
chmod 700 "$XDG_RUNTIME_DIR"
cat > "$XDG_CONFIG_HOME/focus/config.toml" <<'TOML'
notify = false
popup = false
break_duration = "2s"
TOML

PID=
cleanup() { [[ -n $PID ]] && kill "$PID" 2>/dev/null; rm -rf "$SANDBOX"; }
trap cleanup EXIT

pass=0
ok()   { pass=$((pass + 1)); echo "  ok   $1"; }
fail() { echo "  FAIL $1"; exit 1; }
expect() { # expect <description> <needle> <haystack>
  [[ $3 == *"$2"* ]] && ok "$1" || fail "$1: wanted '$2' in: $3"
}
focus() { "$BIN/focus" "$@" 2>&1; }

start_daemon() {
  "$BIN/focusd" 2>>"$SANDBOX/focusd.log" &
  PID=$!
  for _ in $(seq 50); do [[ -S $SOCK ]] && return; sleep 0.1; done
  fail "daemon did not create its socket"
}

echo "daemon"
expect "clear message when daemon is down" "systemctl --user start focusd" "$(focus status || true)"
start_daemon
[[ $(stat -c %a "$SOCK") == 600 ]] && ok "socket is mode 0600" || fail "socket mode $(stat -c %a "$SOCK")"
expect "second daemon refused" "already running" "$("$BIN/focusd" 2>&1 || true)"
expect "health over curl --unix-socket" '"ok"' "$(curl -s --unix-socket "$SOCK" http://x/v1/health)"

echo "cli"
expect "start" 'Started "Write the design" for 25m' "$(focus start "Write the design" 25m)"
expect "status shows remaining" "remaining of 25m" "$(focus status)"
expect "status --json" '"goal": "Write the design"' "$(focus status --json)"
expect "second start conflicts" "already running" "$(focus start other 5m || true)"
expect "API 409 on conflict" "409" "$(curl -s -o /dev/null -w '%{http_code}' --unix-socket "$SOCK" -d '{"goal":"x","planned_seconds":60}' http://x/v1/sessions)"
expect "a lone duration is not taken as the goal" "looks like a duration" "$(focus start 25m || true)"
expect "API 422 on overflowing duration" "422" "$(curl -s -o /dev/null -w '%{http_code}' --unix-socket "$SOCK" -d '{"goal":"x","planned_seconds":18446745574}' http://x/v1/sessions)"
expect "API 422 on empty goal" "422" "$(curl -s -o /dev/null -w '%{http_code}' --unix-socket "$SOCK" -d '{"goal":"","planned_seconds":60}' http://x/v1/sessions)"

echo "restart recovery"
before=$(focus status --json | grep -o '"ends_at": [0-9]*')
kill -9 "$PID"; wait "$PID" 2>/dev/null || true
start_daemon
expect "session survives a crash with the same end time" "$before" "$(focus status --json)"
expect "finish with reflection, no break" 'Finished "Write the design"' "$(focus finish --no-break "drafted sections 1-3")"
expect "double finish refused" "no session is running" "$(focus finish x || true)"

echo "time up, events, auto break"
curl -sN --unix-socket "$SOCK" http://x/v1/events > "$SANDBOX/events" &
CURL=$!
sleep 0.3
focus start "Short one" 2s >/dev/null
sleep 2.5
expect "time-up keeps the session waiting for a reflection" "Time's up: Short one" "$(focus status)"
expect "finish starts the configured break" "Break for 2s" "$(focus finish "tiny task")"
sleep 2.5
expect "break ends by itself" "No session running" "$(focus status)"
kill "$CURL" 2>/dev/null || true
events=$(cat "$SANDBOX/events")
for ev in session.started session.time_up session.finished; do expect "SSE delivered $ev" "event: $ev" "$events"; done

echo "abandon, log, stats"
focus start "Give up" 10m >/dev/null
expect "abandon" 'Abandoned "Give up"' "$(focus abandon)"
log=$(focus log)
expect "log lists reflection" "drafted sections 1-3" "$log"
expect "log lists abandoned" "abandoned" "$log"
expect "stats count time spent, not planned" "2 completed, 1 abandoned" "$(focus stats)"
stats=$(focus stats)
[[ $stats != *25m* ]] && ok "early finish is not credited the full 25m" || fail "early finish credited planned time: $stats"
expect "weekly stats" "This week" "$(focus stats --week)"
expect "start during break ends the break" "Started" "$(focus break 5m >/dev/null; focus start "After break" 1m)"

echo "all $pass checks passed"

#!/usr/bin/env bash
# Checks what focusd launches and notifies, without touching the desktop:
# stub `focus` and `notify-send` executables record how they were called.
# Usage: scripts/e2e-desktop.sh [bin-dir]
set -euo pipefail

BIN=$(cd "${1:-bin}" && pwd)
SANDBOX=$(mktemp -d)
export XDG_DATA_HOME=$SANDBOX/data XDG_CONFIG_HOME=$SANDBOX/config XDG_RUNTIME_DIR=$SANDBOX/run XDG_STATE_HOME=$SANDBOX/state
unset FOCUS_SOCKET
mkdir -p "$XDG_CONFIG_HOME/focus" "$XDG_RUNTIME_DIR" "$SANDBOX/daemon" "$SANDBOX/stubs"
chmod 700 "$XDG_RUNTIME_DIR"
printf 'notify = true\nsound = false\npopup = true\nclock_on_start = true\nauto_break = false\n' > "$XDG_CONFIG_HOME/focus/config.toml"

LAUNCHED=$SANDBOX/launched NOTIFIED=$SANDBOX/notified
touch "$LAUNCHED" "$NOTIFIED"
# focusd launches the `focus` that sits next to it.
cp "$BIN/focusd" "$SANDBOX/daemon/focusd"
printf '#!/bin/sh\necho "$*" >> %s\n' "$LAUNCHED" > "$SANDBOX/daemon/focus"
printf '#!/bin/sh\nprintf "%%s|" "$@" >> %s; echo >> %s\ncase "$*" in *--action=*) echo new;; esac\n' "$NOTIFIED" "$NOTIFIED" > "$SANDBOX/stubs/notify-send"
chmod +x "$SANDBOX/daemon/focus" "$SANDBOX/stubs/notify-send"

PID=
cleanup() { [[ -n $PID ]] && kill "$PID" 2>/dev/null; rm -rf "$SANDBOX"; }
trap cleanup EXIT
pass=0
ok()   { pass=$((pass + 1)); echo "  ok   $1"; }
fail() { echo "  FAIL $1"; echo "--- launched:"; cat "$LAUNCHED"; echo "--- notified:"; cat "$NOTIFIED"; exit 1; }
wait_for() { # wait_for <file> <text> <seconds>
  for _ in $(seq $(($3 * 10))); do grep -qF -- "$2" "$1" && return 0; sleep 0.1; done
  return 1
}

PATH=$SANDBOX/stubs:$PATH "$SANDBOX/daemon/focusd" 2>"$SANDBOX/focusd.log" &
PID=$!
for _ in $(seq 50); do [[ -S $XDG_RUNTIME_DIR/focus/focusd.sock ]] && break; sleep 0.1; done

"$BIN/focus" start "Clock check" 2s >/dev/null
wait_for "$LAUNCHED" "clock" 3 && ok "starting a focus session opens the clock" || fail "clock not launched on start"
wait_for "$LAUNCHED" "gui --popup" 4 && ok "time up opens the reflection popup" || fail "popup not launched"
wait_for "$NOTIFIED" "Time complete" 3 && ok "time up sends a \"Time complete\" notification" || fail "no Time complete notification"
grep -qF -- "--action=new=New session" "$NOTIFIED" && ok "the notification offers New session" || fail "no New session action"
for _ in $(seq 30); do grep -qx "gui" "$LAUNCHED" && break; sleep 0.1; done
grep -qx "gui" "$LAUNCHED" && ok "choosing New session opens Focus" || fail "Focus not opened from the notification"

"$BIN/focus" finish "done" >/dev/null
before=$(grep -c '^clock$' "$LAUNCHED")
"$BIN/focus" break 5m >/dev/null
sleep 0.5
[[ $(grep -c '^clock$' "$LAUNCHED") == "$before" ]] && ok "a break does not open the clock" || fail "clock opened for a break"

echo "all $pass checks passed"

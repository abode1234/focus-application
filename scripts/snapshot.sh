#!/usr/bin/env bash
# Capture every user-facing output of the binaries in a sandbox, with clock
# times and timestamps masked, so two builds can be diffed byte for byte.
# Usage: scripts/snapshot.sh <bin-dir> <out-dir>
set -euo pipefail

BIN=$(cd "$1" && pwd)
OUT=$2
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
SANDBOX=$(mktemp -d)
export XDG_DATA_HOME=$SANDBOX/data XDG_CONFIG_HOME=$SANDBOX/config XDG_RUNTIME_DIR=$SANDBOX/run XDG_STATE_HOME=$SANDBOX/state
unset FOCUS_SOCKET
mkdir -p "$XDG_CONFIG_HOME/focus" "$XDG_RUNTIME_DIR"
chmod 700 "$XDG_RUNTIME_DIR"
printf 'notify = false\npopup = false\nclock_on_start = false\nbreak_duration = "2s"\n' > "$XDG_CONFIG_HOME/focus/config.toml"
PIDS=()
cleanup() { for p in "${PIDS[@]}"; do kill "$p" 2>/dev/null || true; done; rm -rf "$SANDBOX"; }
trap cleanup EXIT

mask() {
  sed -E \
    -e 's/[0-9]{1,2}:[0-9]{2}(:[0-9]{2})?/T/g' \
    -e 's/"(now|started_at|ends_at|ended_at|from|to)": [0-9]+/"\1": N/g' \
    -e 's/"(remaining_seconds)": [0-9]+/"\1": N/g' \
    -e 's/data-(ends|started)-at="[0-9]+"/data-\1-at="N"/g' \
    -e 's/(Mon|Tue|Wed|Thu|Fri|Sat|Sun) [0-9]{2} [A-Z][a-z]{2}/DATE/g' \
    -e 's/"date": "[0-9-]+"/"date": "D"/g' \
    -e 's/[0-9]+[hms]([0-9]+[ms])?([0-9]+s)? focused/X focused/g' \
    -e 's/"focus_seconds": [0-9]+/"focus_seconds": N/g' \
    -e 's/(█*) [0-9hms]+$/\1 D/'
}
snap() { # snap <name> <command...>
  { "${@:2}" 2>&1 || echo "[exit $?]"; } | mask > "$OUT/$1.txt"
}
focus() { "$BIN/focus" "$@"; }

snap status-daemon-down focus status
"$BIN/focusd" 2>/dev/null & PIDS+=($!)
for _ in $(seq 50); do [[ -S $XDG_RUNTIME_DIR/focus/focusd.sock ]] && break; sleep 0.1; done

gui_html() { # gui_html <name>: the main window's page and panel for the current state
  local log=$SANDBOX/gui.log url jar=$SANDBOX/jar
  "$BIN/focus" gui >"$log" 2>&1 & local pid=$!
  for _ in $(seq 50); do url=$(grep -o 'http://127.0.0.1:[0-9]*/?t=[0-9a-f]*' "$log" || true); [[ -n $url ]] && break; sleep 0.1; done
  curl -s -c "$jar" "$url" | mask > "$OUT/gui-page-$1.html"
  curl -s -b "$jar" "${url%%/\?*}/panel" | mask > "$OUT/gui-panel-$1.html"
  kill "$pid"; wait "$pid" 2>/dev/null || true
}

snap status-idle focus status
snap status-idle-json focus status --json
snap log-empty focus log
snap stats-empty focus stats
gui_html idle
snap start focus start "Write the design" 25m
snap start-conflict focus start "Other" 5m
snap status-running focus status
snap status-running-json focus status --json
gui_html running
snap finish-early focus finish --no-break "drafted it"
snap finish-none focus finish x
snap start-short focus start "Short" 2s
sleep 2.5
snap status-timeup focus status
gui_html timeup
snap finish-with-break focus finish "done"
snap status-break focus status
gui_html break
snap break-finish-text focus finish "ignored"
snap break-start focus break 5m
snap start-ends-break focus start "After" 10m
snap abandon focus abandon
snap abandon-none focus abandon
snap log-day focus log
snap log-json focus log --json
snap stats-day focus stats
snap stats-week focus stats --week
snap stats-json focus stats --json
snap start-lone-duration focus start 25m
snap start-bad-duration focus start "x" 0
echo "snapshot: $(ls "$OUT" | wc -l) files in $OUT"

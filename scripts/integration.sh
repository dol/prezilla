#!/usr/bin/env bash
# Runs the X11 integration tests against throwaway displays. Needs Xvfb, xfwm4
# and dbus-run-session. It never touches $DISPLAY: it starts its own X servers
# and stops only the processes it started.
set -euo pipefail

cd "$(dirname "$0")/.."

for tool in Xvfb xfwm4 dbus-run-session; do
	command -v "$tool" >/dev/null || { echo "integration: $tool not found" >&2; exit 1; }
done

NOWM_DISPLAY=:97 # About window tests: no window manager
WM_DISPLAY=:98   # minimize tests: xfwm4 running

pids=()
cleanup() {
	for pid in "${pids[@]:-}"; do
		[ -n "$pid" ] && kill "$pid" 2>/dev/null || true
	done
}
trap cleanup EXIT

start_xvfb() {
	Xvfb "$1" -screen 0 1280x800x24 -noreset >/dev/null 2>&1 &
	pids+=("$!")
	for _ in $(seq 1 50); do
		[ -S "/tmp/.X11-unix/X${1#:}" ] && return 0
		sleep 0.1
	done
	echo "integration: Xvfb $1 did not start" >&2
	exit 1
}

export PREZILLA_X11_LIVE=1

out="$(mktemp)"
trap 'cleanup; rm -f "$out"' EXIT

# check_ran fails when a live test was skipped: a skip would hide a broken setup.
check_ran() {
	cat "$out"
	if grep -q -- '--- SKIP' "$out"; then
		echo "integration: a live test was skipped" >&2
		exit 1
	fi
	if ! grep -q -- '--- PASS' "$out"; then
		echo "integration: no live test ran" >&2
		exit 1
	fi
}

echo "== About window (display $NOWM_DISPLAY, no window manager)"
start_xvfb "$NOWM_DISPLAY"
DISPLAY="$NOWM_DISPLAY" go test -v -race -count=1 -run 'LiveX11' ./internal/system/ >"$out" 2>&1 || { cat "$out"; exit 1; }
check_ran

echo "== Minimize switch (display $WM_DISPLAY, xfwm4)"
start_xvfb "$WM_DISPLAY"
OUT="$out" DISPLAY="$WM_DISPLAY" dbus-run-session -- bash -c '
	xfwm4 --display "$DISPLAY" >/dev/null 2>&1 &
	wm=$!
	trap "kill $wm 2>/dev/null || true" EXIT
	for _ in $(seq 1 50); do
		xprop -display "$DISPLAY" -root _NET_SUPPORTING_WM_CHECK 2>/dev/null | grep -q "window id" && break
		sleep 0.1
	done
	go test -v -race -count=1 -run "LiveMinimize" ./internal/platform/xfce/ >"$OUT" 2>&1 || { cat "$OUT"; exit 1; }
'

check_ran
echo "integration: passed"

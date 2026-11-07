#!/usr/bin/env bash
# Acceptance check for an install tree (make install DESTDIR=... or an extracted package).
# Usage: scripts/check-install.sh <install-root>
set -euo pipefail

root="${1:?usage: check-install.sh <install-root>}"
fail=0
err() { echo "check-install: $*" >&2; fail=1; }

bin=""
for p in "$root"/usr/bin/prezilla "$root"/usr/local/bin/prezilla "$root"/usr*/bin/prezilla; do
  if [ -f "$p" ]; then bin="$p"; break; fi
done
[ -n "$bin" ] && [ -x "$bin" ] || err "no executable bin/prezilla under $root"

prefix=""
[ -n "$bin" ] && prefix="$(dirname "$(dirname "$bin")")"

if [ -n "$prefix" ]; then
  desktop="$prefix/share/applications/prezilla.desktop"
  if [ ! -f "$desktop" ]; then
    err "missing $desktop"
  elif ! grep -q '^Icon=prezilla$' "$desktop"; then
    err "$desktop lacks Icon=prezilla"
  fi
  icons="$prefix/share/icons/hicolor"
  for s in 16 22 24 32 48 64 128 256 512; do
    [ -f "$icons/${s}x${s}/apps/prezilla.png" ] || err "missing icon ${s}x${s}"
  done
  [ -f "$icons/scalable/apps/prezilla.svg" ] || err "missing scalable icon"
else
  err "cannot locate prefix"
fi

[ "$fail" -eq 0 ] && echo "check-install: ok ($prefix)"
exit "$fail"

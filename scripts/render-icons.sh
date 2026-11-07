#!/usr/bin/env bash
# Regenerates the committed PNG icons from the SVG sources (needs rsvg-convert, package librsvg2-bin).
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
src="$root/packaging/icons"
hicolor="$src/hicolor"

for s in 16 22 24 32 48 64 128 256 512; do
  mkdir -p "$hicolor/${s}x${s}/apps"
  rsvg-convert -w "$s" -h "$s" "$src/prezilla.svg" -o "$hicolor/${s}x${s}/apps/prezilla.png"
done

mkdir -p "$hicolor/scalable/apps"
cp "$src/prezilla.svg" "$hicolor/scalable/apps/prezilla.svg"

tray="$root/internal/tray/icons"
rsvg-convert -w 64 -h 64 "$src/prezilla-tray-idle.svg" -o "$tray/idle.png"
rsvg-convert -w 64 -h 64 "$src/prezilla-tray-active.svg" -o "$tray/active.png"

# Changelog

All notable changes to prezilla are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

### Added
- `prezilla` CLI: `on`, `off`, `toggle`, `status` and `version`.
- System tray front-end (`prezilla tray`) with an autostart entry.
- Five XFCE switches, each saving the original value and restoring it on `off`:
  desktop icons, notifications, audio mute, minimize windows and wallpaper.
- Auto-disable timeout (`timeout_minutes`) that turns presentation mode off again.
- Application and tray icons (hicolor PNG sizes and scalable SVG).
- Packaging: `make install`, and `.deb`, `.rpm` and `.pkg.tar.zst` packages via nfpm.
- GitHub Actions CI and release workflows.

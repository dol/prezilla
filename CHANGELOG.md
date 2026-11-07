# Changelog

All notable changes to prezilla are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

### Added
- `prezilla` CLI: `on`, `off`, `toggle`, `status` and `version`.
- `prezilla about` prints version, license and website.
- System tray front-end (`prezilla tray`) with an autostart entry.
- Five XFCE switches, each saving the original value and restoring it on `off`:
  desktop icons, notifications, audio mute, minimize windows and wallpaper.
- Auto-disable timeout (`timeout_minutes`) that turns presentation mode off again.
- Application and tray icons (hicolor PNG sizes and scalable SVG).
- Packaging: `make install`, and `.deb`, `.rpm` and `.pkg.tar.zst` packages via nfpm.
- GitHub Actions CI and release workflows.

### Changed
- Mute and show-desktop no longer need external audio or window-manager tools: they use native PulseAudio and EWMH clients, and packages no longer recommend those tools.

### Fixed
- External commands time out after 10 s and engine operations after 25 s, so a hung `xfconf-query` or native audio/X11 call can no longer hold the state lock and block `off`, the watcher and the tray.
- Concurrent `prezilla` processes (tray, CLI, watcher) serialize on a state lock, so originals can no longer be lost; the state file is fsynced on save.
- `off` restores switches removed from the config since `on`, and `off`/`status`/`tray` fall back to the default config when `config.json` is broken.
- The auto-off deadline is enforced for sessions started from the tray and when the tray starts at login.
- `mute` unmutes the output it muted even if the default output changed; the tray tooltip shows toggle and expiry errors together; `wallpaper` without an image fails clearly; Ctrl-C no longer interrupts a half-applied `on`/`off`/`toggle`.

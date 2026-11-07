# Changelog

All notable changes to Prezilla are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

### Added
- Tray: the Quit item reads "Quit (presentation stays on)" while presentation mode is on, and "Quit" otherwise.
- Tray: if zenity cannot open its window because the display is dead, About falls back to the notification instead of doing nothing.
- Plain-language errors: the common failures (no sound server, no X display, missing `xfconf-query`, no wallpaper image, another action running, unreadable saved state, invalid `config.json`, unsupported desktop) are reported as a sentence with a next step (also timeouts of the sound and X servers, a state file that cannot be saved, a deadline timer that cannot be started, and a notification server without buttons); the technical text follows in parentheses on stderr and is left out of the tray tooltip.
- `prezilla` CLI: `on`, `off`, `toggle`, `status` and `version`.
- `prezilla about` prints version, license and website.
- Tray menu entry "About Prezilla": a small window (via the optional `zenity`) with the project link and a "Close" button; without `zenity`, or if it fails, a desktop notification with the website URL as text. The `.deb` and `.rpm` packages recommend `zenity`.
- System tray front-end (`prezilla tray`) with an autostart entry.
- Tray timer entry showing when presentation mode ends and the minutes left, with a "Reset timer to N min" entry below it once the label says less than N minutes are left (at least a minute has passed; right after turning on, the item is hidden because it would change nothing) (the end time becomes N minutes from now; a later one is kept).
- Left-click on the tray icon toggles presentation mode; the menu shows a "Presentation mode" checkbox instead of a state label. Toggle clicks within 1 s of a toggle starting or finishing are ignored, so a double click does not flip the mode twice.
- Tray "Keep presenting?" prompt when the screensaver starts during a timed session: a sticky critical notification with "Keep presenting for N min" and "Stop presenting" actions, plus the same entries at the top of the tray menu in case do-not-disturb hides the notification.
- Five XFCE switches, each saving the original value and restoring it on `off`:
  desktop icons, notifications, audio mute, minimize windows and wallpaper.
- Auto-disable timeout (`timeout_minutes`) that turns presentation mode off again.
- Application and tray icons (hicolor PNG sizes and scalable SVG).
- Packaging: `make install`, and `.deb`, `.rpm` and `.pkg.tar.zst` packages via nfpm.
- GitHub Actions CI and release workflows.

### Changed
- The product is written "Prezilla" in all user-visible text (About window, tray, notifications, desktop entry); the command, package and paths stay `prezilla`.
- Tray icons tell the state by shape, not only hue: an outlined grey (`#7A7A7A`) screen while off and a filled green (`#1A9B4A`) screen while presenting, both at least 3:1 against light and dark panels.
- Tray wording: "N min left" keeps its number and unit together (non-breaking space); the tooltip line while the screensaver question is open reads "Screensaver started. Keep presenting?".
- Mute and show-desktop no longer need external audio or window-manager tools: they use native PulseAudio and EWMH clients, and packages no longer recommend those tools.

### Fixed
- External commands time out after 10 s and engine operations after 25 s, so a hung `xfconf-query` or native audio/X11 call can no longer hold the state lock and block `off`, the watcher and the tray.
- Concurrent `prezilla` processes (tray, CLI, watcher) serialize on a state lock, so originals can no longer be lost; the state file is fsynced on save.
- `off` restores switches removed from the config since `on`, and `off`/`status`/`tray` fall back to the default config when `config.json` is broken.
- The auto-off deadline is enforced for sessions started from the tray and when the tray starts at login.
- `mute` unmutes the output it muted even if the default output changed; the tray tooltip shows toggle and expiry errors together; `wallpaper` without an image fails clearly; Ctrl-C no longer interrupts a half-applied `on`/`off`/`toggle`.

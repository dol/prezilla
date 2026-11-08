# Changelog

All notable changes to Prezilla are documented in this file.
The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/).

## [Unreleased]

### Added
- Tray: a tick box per switch (Hide desktop icons, Disable notifications, Keep screen awake, Mute sound, Minimize windows, Change wallpaper). Changes apply at once, also while presentation mode is on, and are saved to `config.json`; unknown keys in the file are kept, and a symlinked file stays a symlink with its permissions.
- Tray: "Choose wallpaper…" picks the wallpaper image with a file chooser (without one, a notification explains how to set `wallpaper` in `config.json`). Choosing an image while presenting applies it at once.
- Error sentences for a wallpaper image that no longer exists, a chosen file that is missing or not an image, a file chooser that cannot start, a `config.json` that cannot be saved, and turning presentation mode on with no switch ticked (which now fails with that sentence instead of doing nothing).
- Tray: About Prezilla shows a built-in X11 window with the logo, version, description, license, project link and a Close button, drawn with embedded fonts (no new runtime dependency); without an X display, a desktop notification is shown instead.
- `presentation` switch (on by default, after `notifications`): turns on `xfce4-power-manager`'s presentation mode, which also keeps the screensaver and screen blanking off, and restores the previous value (or removes the setting) on `off`. It needs `xfce4-power-manager` to be running.
- Tray: the Quit item reads "Quit (presentation stays on)" while presentation mode is on, and "Quit" otherwise.
- Plain-language errors: the common failures (no sound server, no X display, unreachable XFCE settings service, no wallpaper image, another action running, unreadable saved state, invalid `config.json`, unsupported desktop) are reported as a sentence with a next step (also timeouts of the sound and X servers, a state file that cannot be saved, a deadline timer that cannot be started, and a notification server without buttons); the technical text follows in parentheses on stderr and is left out of the tray tooltip.
- `prezilla` CLI: `on`, `off`, `toggle`, `status` and `version`.
- `prezilla about` prints version, license and website.
- Tray menu entry "About Prezilla": a small window with the project link and a "Close" button; without an X display, a desktop notification with the website URL as text. The `.deb` and `.rpm` packages recommend `zenity` for the wallpaper file chooser.
- System tray front-end (`prezilla tray`) with an autostart entry.
- Tray timer entry showing when presentation mode ends and the minutes left, with a "Reset timer to N min" entry below it once the label says less than N minutes are left (at least a minute has passed; right after turning on, the item is hidden because it would change nothing) (the end time becomes N minutes from now; a later one is kept).
- The menu shows a "Presentation mode" checkbox instead of a state label; it toggles the mode. A click on the tray icon toggles too where the panel sends one (a middle click on the XFCE panel when "menu is primary" is on, a left click otherwise).
- Tray "Keep presenting?" prompt when the screensaver starts during a timed session: a sticky critical notification with "Keep presenting for N min" and "Stop presenting" actions, plus the same entries at the top of the tray menu in case do-not-disturb hides the notification.
- Five XFCE switches, each saving the original value and restoring it on `off`:
  desktop icons, notifications, audio mute, minimize windows and wallpaper.
- Auto-disable timeout (`timeout_minutes`) that turns presentation mode off again.
- Application and tray icons (hicolor PNG sizes and scalable SVG).
- Packaging: `make install`, and `.deb`, `.rpm` and `.pkg.tar.zst` packages via nfpm.
- GitHub Actions CI and release workflows.
- Generic wallpaper switch for desktops other than XFCE: GNOME (also its dark picture), Unity, Pantheon, Cinnamon, MATE, Deepin, KDE Plasma, LXDE, macOS and Windows read the current wallpaper, set the configured image and restore the original on `off`. It uses `gsettings`, `dconf`, `pcmanfm` or `osascript` where the desktop has no other interface; KDE is reached over D-Bus. Prezilla still starts on XFCE only, so it is not reachable from the command line yet.

### Changed
- Tray: "Choose wallpaper…" uses the desktop's file chooser portal over D-Bus (for example `xdg-desktop-portal-gtk`), with `zenity` as the fallback. Without either, the notification explains how to set `wallpaper` in `config.json`. The `.deb` and `.rpm` packages also recommend `xdg-desktop-portal-gtk`.
- The wallpaper file choosers are tried in order of preference and each says whether it is supported on this system; the first supported one is used, and one that turns out to be missing hands over to the next.
- About no longer uses `zenity`; it is only used as the fallback wallpaper file chooser.
- `wallpaper` in `config.json` adds the `wallpaper` switch only when the file has no `switches` key; a `switches` list is used exactly as written. If your file lists `switches` and sets `wallpaper`, add "wallpaper" to `switches` to keep it.
- Icons, notifications and wallpaper (and the new presentation switch) talk to the XFCE settings daemon (`xfconfd`) directly over the D-Bus session bus instead of running `xfconf-query`, which is no longer needed. A settings service that cannot be reached is reported as a plain sentence with a next step.
- The product is written "Prezilla" in all user-visible text (About window, tray, notifications, desktop entry); the command, package and paths stay `prezilla`.
- Tray icons tell the state by shape, not only hue: an outlined grey (`#7A7A7A`) screen while off and a filled green (`#1A9B4A`) screen while presenting, both at least 3:1 against light and dark panels.
- Tray wording: "N min left" keeps its number and unit together (non-breaking space); the tooltip line while the screensaver question is open reads "Screensaver started. Keep presenting?".
- Mute and show-desktop no longer need external audio or window-manager tools: they use native PulseAudio and EWMH clients, and packages no longer recommend those tools.

### Fixed
- Tray: closing the file chooser dialog with Esc or the window's close button no longer shows a "file chooser portal failed" error; it counts as cancelled, like the Cancel button.
- `minimize`: after alt+tab to a window during presentation mode, turning the mode off brought back only that window. Prezilla now remembers the windows it minimized and restores them itself when the window manager has forgotten them; windows you had minimized before stay minimized.
- External commands time out after 10 s and engine operations after 25 s, so a hung `xfconf-query` or native audio/X11 call can no longer hold the state lock and block `off`, the watcher and the tray.
- Concurrent `prezilla` processes (tray, CLI, watcher) serialize on a state lock, so originals can no longer be lost; the state file is fsynced on save.
- `off` restores switches removed from the config since `on`, and `off`/`status`/`tray` fall back to the default config when `config.json` is broken.
- The auto-off deadline is enforced for sessions started from the tray and when the tray starts at login.
- `mute` unmutes the output it muted even if the default output changed; the tray tooltip shows toggle and expiry errors together; `wallpaper` without an image fails clearly; Ctrl-C no longer interrupts a half-applied `on`/`off`/`toggle`.

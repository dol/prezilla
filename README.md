# prezilla

Presentation mode for your desktop: one click (or one command) hides desktop
icons, silences notifications and audio, shows the desktop, and puts everything
back when you are done. XFCE on X11 first; other desktops are planned.

## Features
- Five switches, each saving the original value and restoring it on `off`:
  - `icons`: hides desktop icons
  - `notifications`: enables do-not-disturb in `xfce4-notifyd`
  - `mute`: mutes the default audio sink and later unmutes that same output, even if the default changed meanwhile (only if it was not muted before)
  - `minimize`: shows the desktop (minimizes all windows)
  - `wallpaper`: swaps the wallpaper on every monitor/workspace; off unless `wallpaper` is set in the config to a path to an image file
- Auto-disable after 30 minutes by default (`timeout_minutes`, or `--for` per call), enforced by a detached background watcher
- System tray icon with a toggle, a timer entry (end time and minutes left) and the remaining time in the tooltip; the same actions are available from the CLI
- When the screensaver starts while a timed session is still running, the tray asks whether to continue presenting

## Requirements
- An XFCE session on X11 (Wayland is not supported); `xfconf-query` comes with it
- For the tray: a panel with the Status Notifier plugin (the CLI works without it)

No other packages are needed. Mute talks to PulseAudio/PipeWire-pulse over its native protocol, and show-desktop sets the EWMH `_NET_SHOWING_DESKTOP` property directly. The PulseAudio server is found via `PULSE_SERVER` or `$XDG_RUNTIME_DIR/pulse/native`; `default-server` in `client.conf` is not read.

## Installation

### Release packages
Download from the project's GitHub releases page:

| Format | File | Install |
| --- | --- | --- |
| Debian/Ubuntu | `prezilla_<version>_amd64.deb` | `sudo apt install ./prezilla_<version>_amd64.deb` |
| Fedora/openSUSE | `prezilla_<version>_amd64.rpm` | `sudo dnf install ./prezilla_<version>_amd64.rpm` |
| Arch | `prezilla_<version>_amd64.pkg.tar.zst` | `sudo pacman -U prezilla_<version>_amd64.pkg.tar.zst` |
| Any Linux | `prezilla_<version>_linux_{amd64,arm64}.tar.gz` | unpack and copy `prezilla` onto your `PATH` |

The packages install the binary, the desktop entry, the icons and an autostart entry
(`/etc/xdg/autostart/prezilla.desktop`), so the tray starts at login.

### From source
Needs Go 1.23+.
```bash
git clone <repo-url>
cd prezilla
make build                          # ./prezilla
sudo make install                   # PREFIX=/usr/local by default; honours DESTDIR
```
`make uninstall` removes the installed files. Other targets: `lint`, `test`, `package` (needs `nfpm`).

### Autostart the tray
Packages already autostart the tray at login. After `make install` or a tarball install, enable it per user:
```bash
mkdir -p ~/.config/autostart
cp packaging/prezilla.desktop ~/.config/autostart/
```
If the panel's StatusNotifier host is not up yet when autostart runs, `prezilla tray` can exit 1. It still first restores a session whose deadline has passed and keeps a live deadline watched. Log in again or start `prezilla tray` from the panel's launcher; the CLI works regardless.
The application icon lives in `packaging/icons/`.

## Usage
```
prezilla on [--for DURATION]
prezilla off
prezilla toggle [--for DURATION]
prezilla status
prezilla tray
prezilla about
prezilla version
```

- `on` / `toggle` enable presentation mode with the configured timeout. `--for 45m` overrides it (Go duration syntax, e.g. `90s`, `1h30m`); `--for 0` means no auto-disable. Calling `on` while already active only replaces the deadline.
- `off` restores everything and is a no-op when inactive.
- `status` prints one of `inactive`, `active` or `active until HH:MM`, and first disables the mode if its deadline has passed.
- `tray` runs the tray icon until you choose Quit. Its menu has the toggle item, **About prezilla** (shows version, license and website as a desktop notification) and **Project website** (opens the website in your browser). Errors from these two entries appear in the tooltip and on stderr. Presentation mode's do-not-disturb may hide the About notification while the mode is on. `xdg-open` or `exo-open` is needed only for the website link.
- While a deadline is set, the top of the tray menu shows a greyed-out timer entry such as `Ends 15:04 · 12 min left`, refreshed every 2 seconds; the tooltip keeps `, N min left`.
- **Screensaver prompt**: if the screensaver starts while presentation mode is on and its deadline has not passed, the tray shows one sticky notification, "Presentation still running", with the actions **Continue presenting** (moves the deadline to the configured timeout from now, 30 minutes when `timeout_minutes` is 0; a later deadline, for example from `on --for 2h`, is kept, and the saved originals are untouched) and **End presentation** (same as `off`). Closing the notification changes nothing, and the timer still ends the session. While the question is open, the tray menu also shows **Continue presentation** and **End presentation**, and the tooltip adds `Screensaver started: continue presenting?`. The first answer wins, and the other is closed. If the session ends or expires first, the question is withdrawn. The screensaver is watched through the `ActiveChanged` D-Bus signal of `org.xfce.ScreenSaver`, `org.freedesktop.ScreenSaver` and `org.gnome.ScreenSaver`. Without any deadline (`timeout_minutes: 0` and no `--for`) there is never a question; a deadline set with `on --for` still gets one.
  - Caveat: presentation mode's own do-not-disturb may hide the notification. It is sent with critical urgency, which some notification daemons show anyway, but this has not been verified on every daemon, so the two menu entries are the guaranteed way to answer. If the notification server lacks the `actions` capability, the error appears in the tooltip and the menu entries still work.
- `about` prints the name, version, description, license and website. Like `version`, it needs no config and works on any desktop.
- When a deadline is set, `on`/`toggle` and turning the mode on from the tray start a detached `prezilla watch` that restores everything at expiry, even after the tray quits (the tray also checks every 2 seconds).

If `config.json` cannot be read, `on` and `toggle` fail, while `off`, `status` and `tray` print a warning and continue with the default configuration, so a broken config never blocks restoring. `off` restores every saved switch, even one removed from the config since `on`.

Exit codes: `0` success, `1` a switch, state or config error (the message goes to stderr), `2` usage error (unknown command, bad flag, invalid `--for`).

## Configuration
`~/.config/prezilla/config.json` (honours `XDG_CONFIG_HOME`). A missing file means the defaults shown first:

```json
{
  "switches": ["icons", "notifications", "mute", "minimize"],
  "timeout_minutes": 30
}
```

With a wallpaper (setting `wallpaper` adds the `wallpaper` switch automatically):

```json
{
  "switches": ["icons", "notifications", "mute", "minimize", "wallpaper"],
  "wallpaper": "/home/me/Pictures/presenting.png",
  "timeout_minutes": 45
}
```

`timeout_minutes: 0` disables the auto-off; negative values are rejected.

State (the saved original values and the deadline) is kept in `$XDG_STATE_HOME/prezilla/state.json`, by default `~/.local/state/prezilla/state.json`. Delete it only if you do not need the originals back.

## Troubleshooting
- **A switch fails**: the error names the cause. The other switches are still applied; fix the cause and run `prezilla off`, then `on`.
  - `pulse: connect: ...`: no PulseAudio/PipeWire-pulse server is reachable. Check `PULSE_SERVER` and `$XDG_RUNTIME_DIR/pulse/native`.
  - `x11: connect: ...`: no X display. `DISPLAY` is unset, for example in a Wayland-only session, which is not supported.
  - `xfconf-query` not found: this is not an XFCE session.
- **No tray icon**: `prezilla tray` reports that no StatusNotifier host was found. Add the Status Notifier plugin to the panel, or use `prezilla toggle` from a shortcut or terminal.
- **`unsupported desktop`**: `XDG_CURRENT_DESKTOP` must contain `XFCE`.

## Development
```bash
go test ./...
```
Tests never touch the real desktop: every switch talks to the system through the `system.Runner` interface, and tests substitute `system.FakeRunner`, which records command lines and replays canned output.

## Roadmap
GNOME, KDE, Windows and macOS backends are planned.

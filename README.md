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
- System tray icon: left-click toggles presentation mode, the icon is filled while presenting and outlined while not, and the menu has a **Presentation mode** checkbox, the end time with an **Extend** entry, and the remaining time in the tooltip; the same actions are available from the CLI
- When the screensaver starts while a timed session is still running, the tray asks whether to keep presenting

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
- `tray` runs the tray icon until you choose Quit. **Left-click the icon** to turn presentation mode on or off (if your panel does not send left clicks, use the menu). The icon shows the state by shape as well as colour: an outlined grey screen with the switch on the left while off, a filled green screen with the switch on the right while presenting. Right-click opens the menu, from top to bottom:
  - **Keep presenting (+30 min)** and **Stop presenting**, only while the screensaver question below is open.
  - **Presentation mode**, a checkbox that is ticked while the mode is on; clicking it toggles the mode, exactly like a left click on the icon.
  - A greyed-out timer entry such as `Ends 15:04 · 12 min left` and **Extend by 30 min**, only while a deadline is set. The timer is refreshed every 2 seconds; the tooltip keeps `, N min left`. **Extend** moves the deadline to 30 minutes from now (the configured timeout; 30 minutes when `timeout_minutes` is 0) and never shortens a later one; the running watcher picks up the new deadline.
  - **About prezilla** shows version, license and website as a desktop notification. Presentation mode's do-not-disturb may hide it while the mode is on.
  - **Quit** closes the tray icon and leaves presentation mode as it is.

  Errors from toggling, **Extend** and **About** appear in the tooltip and on stderr.
- **Screensaver prompt**: if the screensaver starts while presentation mode is on and its deadline has not passed, the tray shows one sticky notification, "Keep presenting?" ("The screensaver started with 12 min left."), with the actions **Keep presenting (+30 min)** (moves the deadline to the configured timeout from now, 30 minutes when `timeout_minutes` is 0; a later deadline, for example from `on --for 2h`, is kept, and the saved originals are untouched) and **Stop presenting** (same as `off`). Dismissing the notification changes nothing, and the timer still ends the session. If the notification server withdraws it instead (for example do-not-disturb or expiry), the question stays open in the tray menu. While the question is open, the tray menu also shows **Keep presenting (+30 min)** and **Stop presenting** at the top, and the tooltip adds `Screensaver started. Keep presenting?`. The first answer wins, and the other is closed. If the session ends or expires first, the question is withdrawn. The screensaver is watched through the `ActiveChanged` D-Bus signal of `org.xfce.ScreenSaver`, `org.freedesktop.ScreenSaver` and `org.gnome.ScreenSaver`. Without any deadline (`timeout_minutes: 0` and no `--for`) there is never a question; a deadline set with `on --for` still gets one.
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
  - `x11: connect: ...`: no reachable X display (`DISPLAY` is unset or unreachable). Wayland sessions are not supported.
  - `xfconf-query` not found: this is not an XFCE session.
- **No tray icon**: `prezilla tray` reports that no StatusNotifier host was found. Add the Status Notifier plugin to the panel, or use `prezilla toggle` from a shortcut or terminal.
- **`unsupported desktop`**: `XDG_CURRENT_DESKTOP` must contain `XFCE`.

## Development
```bash
go test ./...
```
Tests never touch the real desktop: icons, notifications and wallpaper use `system.Runner` (`xfconf-query`), mute uses `system.Audio`, minimize uses `system.Windows`, and the tray actions use `system.Notifier`, `system.Opener`, `system.ScreenSaver` and `system.Prompter`. Each interface has a fake (for example `system.FakeRunner`, which records command lines and replays canned output) that tests substitute.

## Roadmap
GNOME, KDE, Windows and macOS backends are planned.

# prezilla

Presentation mode for your desktop: one click (or one command) hides desktop
icons, silences notifications and audio, shows the desktop, and puts everything
back when you are done. XFCE on X11 first; other desktops are planned.

## Features
- Five switches, each saving the original value and restoring it on `off`:
  - `icons`: hides desktop icons
  - `notifications`: enables do-not-disturb in `xfce4-notifyd`
  - `mute`: mutes the default audio sink (only unmutes if it was not muted before)
  - `minimize`: shows the desktop (minimizes all windows)
  - `wallpaper`: swaps the wallpaper on every monitor/workspace; off unless `wallpaper` is set in the config
- Auto-disable after 30 minutes by default (`timeout_minutes`, or `--for` per call), enforced by a detached background watcher
- System tray icon with a toggle and remaining time in the tooltip; the same actions are available from the CLI

## Requirements
- XFCE on X11 (Wayland is not supported)
- `xfconf-query` (from `xfconf`)
- `pactl` (Debian/Ubuntu: `pulseaudio-utils`)
- `wmctrl`
- For the tray: a panel with the Status Notifier plugin (the CLI works without it)

On Debian/Ubuntu:
```
sudo apt install xfconf pulseaudio-utils wmctrl
```

## Installation

### From source
Needs Go 1.23+.
```bash
git clone https://github.com/dol/prezilla
cd prezilla
go build -o prezilla ./cmd/prezilla
sudo install -m 755 prezilla /usr/local/bin/prezilla   # any directory on your PATH
```

`make install` and release packages arrive with the packaging work.

### Autostart the tray
```bash
mkdir -p ~/.config/autostart
cp packaging/prezilla.desktop ~/.config/autostart/
```
The application icon lives in `packaging/icons/`.

## Usage
```
prezilla on [--for DURATION]
prezilla off
prezilla toggle [--for DURATION]
prezilla status
prezilla tray
prezilla version
```

- `on` / `toggle` enable presentation mode with the configured timeout. `--for 45m` overrides it (Go duration syntax, e.g. `90s`, `1h30m`); `--for 0` means no auto-disable. Calling `on` while already active only replaces the deadline.
- `off` restores everything and is a no-op when inactive.
- `status` prints one of `inactive`, `active` or `active until HH:MM`, and first disables the mode if its deadline has passed.
- `tray` runs the tray icon until you choose Quit.
- When a deadline is set, `on`/`toggle` start a detached `prezilla watch` that restores everything at expiry (the tray also checks every 2 seconds).

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
- **A tool is missing or fails**: the error names the tool (for example `pactl: ...`). The other switches are still applied; install the tool and run `prezilla off` / `on` again.
- **No tray icon**: `prezilla tray` reports that no StatusNotifier host was found. Add the Status Notifier plugin to the panel, or use `prezilla toggle` from a shortcut or terminal.
- **`unsupported desktop`**: `XDG_CURRENT_DESKTOP` must contain `XFCE`.

## Development
```bash
go test ./...
```
Tests never touch the real desktop: every switch talks to the system through the `system.Runner` interface, and tests substitute `system.FakeRunner`, which records command lines and replays canned output.

## Roadmap
GNOME, KDE, Windows and macOS backends are planned.

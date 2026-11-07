# Prezilla

One click (or one command) puts your desktop into presentation mode: icons
hidden, notifications and sound off, windows out of the way, screen kept awake.
When you are done, or after 30 minutes, everything goes back.

XFCE on X11 for now. GNOME, KDE, Windows and macOS are planned.

## Install

Packages (`.deb`, `.rpm`, Arch, tarball) are built by the release workflow;
none is published yet. From source (Go 1.23+):

```bash
git clone https://github.com/dol/prezilla
cd prezilla
make build && sudo make install
```

Needs an XFCE session on X11 and, for the tray, a panel with the Status
Notifier plugin. Nothing else: no `xfconf-query`, `wmctrl` or `pactl`.
`zenity` is optional and only draws the About window.

## Use

Start the tray with `prezilla tray` (the packages autostart it). Tick
**Presentation mode** in its menu to turn the mode on or off.

From a terminal or a keyboard shortcut:

```bash
prezilla on [--for 45m]    # --for 0 = no timeout
prezilla off
prezilla toggle
prezilla status            # inactive | active | active until 15:04
```

The mode ends by itself after `timeout_minutes` (30 by default), so the
screen can lock again. If the screensaver starts mid-talk, the tray asks
whether to keep presenting.

**Tray clicks:** whether a click on the icon itself toggles depends on the
panel. With the XFCE systray's "menu is primary" option on (the default), a
left click opens the menu and a middle click toggles.

## What it switches

| Switch | Effect |
| --- | --- |
| `icons` | hides desktop icons |
| `notifications` | turns on do-not-disturb |
| `presentation` | XFCE power manager's presentation mode (no screensaver, no blanking) |
| `mute` | mutes the default output |
| `minimize` | shows the desktop; windows come back on `off` |
| `wallpaper` | swaps the wallpaper (only if `wallpaper` is set) |

Each switch saves the original value and puts it back, so things that were
already off stay off.

## Configure

`~/.config/prezilla/config.json`; every key is optional:

```json
{
  "switches": ["icons", "notifications", "presentation", "mute", "minimize"],
  "wallpaper": "/home/me/Pictures/presenting.png",
  "timeout_minutes": 30
}
```

Setting `wallpaper` adds the `wallpaper` switch. `timeout_minutes: 0` turns
the auto-off off.

## Troubleshooting

Errors say what failed and what to do; the technical detail follows in
parentheses on stderr.

- **A switch fails:** the others still apply. Fix the cause (or remove that
  switch from `switches`), then run `prezilla off` and `on` again.
- **No tray icon:** the panel needs the Status Notifier plugin. The CLI works
  without it.
- **Saved state unreadable:** `~/.local/state/prezilla/state.json` is damaged.
  Put your settings back by hand, then delete the file.

Exit codes: `0` ok, `1` a switch, state or config error, `2` bad usage.

## Development

```bash
go test ./...
```

Tests never touch the real desktop; each backend sits behind an interface with
a fake. See [CHANGELOG.md](CHANGELOG.md) for what changed.

## License

[Apache-2.0](LICENSE)

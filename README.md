# Prezilla 🦖

Prezilla puts your desktop into presentation mode with one click or one command: it hides desktop icons, turns off notifications and sound, clears your windows and keeps the screen awake. When you finish, or after 30 minutes, everything returns to how it was.

It runs on XFCE with X11. GNOME, KDE, Windows and macOS are planned.

## Install

Build and install from source with Go 1.23 or newer:

```bash
git clone https://github.com/dol/prezilla
cd prezilla
make build && sudo make install
```

You need an XFCE session on X11. The tray icon also needs a panel with the Status Notifier plugin.

To start the tray at login:

```bash
mkdir -p ~/.config/autostart
cp packaging/prezilla.desktop ~/.config/autostart/
```

## Use

Run `prezilla tray`, then tick **Presentation mode** in the tray menu to turn the mode on or off.

Below it, the menu has a tick box for each switch. A ticked switch is part of presentation mode. Changes apply at once, even while you present, and are saved to `config.json`. **Choose wallpaper…** picks the image for the `wallpaper` switch. It needs a file chooser portal such as `xdg-desktop-portal-gtk`, or `zenity`.

The same actions are available from a terminal or a keyboard shortcut:

```bash
prezilla on --for 45m
prezilla off
prezilla toggle
prezilla status
```

`status` prints `inactive`, `active` or `active until 15:04`. `--for 0` turns the timer off for that session.

The mode ends by itself after 30 minutes, so your screen can lock again. If the screensaver starts during a talk, the tray asks whether to keep presenting.

Whether a click on the tray icon toggles the mode depends on your panel. With the default XFCE panel settings, a left click opens the menu and a middle click toggles.

## Switches

Each switch saves the original value and restores it, so settings that were already off stay off.

| Switch | Effect |
| --- | --- |
| `icons` | Hides desktop icons |
| `notifications` | Turns on do-not-disturb |
| `presentation` | Turns on the XFCE power manager's presentation mode, which stops the screensaver and screen blanking |
| `mute` | Mutes the default output |
| `minimize` | Shows the desktop and brings the windows back afterwards |
| `wallpaper` | Swaps the wallpaper, only when `wallpaper` is set |

## Configure

Prezilla reads `~/.config/prezilla/config.json`. Every key is optional:

```json
{
  "switches": ["icons", "notifications", "presentation", "mute", "minimize", "wallpaper"],
  "wallpaper": "/home/me/Pictures/presenting.png",
  "timeout_minutes": 30
}
```

The tray menu writes `switches` and `wallpaper` for you. The `wallpaper` switch needs `wallpaper` set to an image path. A `switches` list is used exactly as written, so list `wallpaper` there to change the wallpaper. In a file without a `switches` key, setting `wallpaper` adds the `wallpaper` switch. Setting `timeout_minutes` to `0` removes the timer.

## Troubleshooting

Errors name what failed and what to do next. Technical details follow in parentheses on stderr.

- **A switch fails**: the other switches still apply. Fix the cause or remove that switch from `switches`, then run `prezilla off` and `prezilla on` again
- **No tray icon**: add the Status Notifier plugin to your panel. The commands work without it
- **Saved state unreadable**: restore your settings by hand, then delete `~/.local/state/prezilla/state.json`

Exit codes: `0` success, `1` switch, state or config error, `2` usage error.

## Development

Run the tests with:

```bash
go test ./...
```

See [CHANGELOG.md](CHANGELOG.md) for release notes.

## License

[Apache-2.0](LICENSE)

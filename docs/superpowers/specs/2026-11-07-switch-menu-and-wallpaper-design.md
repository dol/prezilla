# Switch tick boxes and wallpaper choice in the tray menu

Date: 2026-11-07. Status: reviewed by a fresh builder; awaiting approval.

## Intent

Today the tray menu has one control, the **Presentation mode** tick box. Which things presentation mode changes (icons, notifications, sound, windows, wallpaper, screen awake) can only be edited by hand in `config.json`, and the wallpaper path too. The user wants to see and change this from the tray, like togglepresent.app's menu: one tick box per switch, plus a way to pick the wallpaper image. Success: a user who never opens `config.json` can decide what presentation mode does and which wallpaper it shows.

## Goals

- One tick box per switch in the tray menu, ticked when the switch is part of presentation mode.
- A tick change takes effect immediately, including while presentation mode is on: ticking a switch applies it at once, unticking it restores its original value at once. The deadline does not change.
- Tick changes are saved to `config.json`, so the CLI (`prezilla on`) and the next tray start use the same choice.
- **Choose wallpaper…** opens a file chooser for one image. The choice is saved, remembered, and ticks **Change wallpaper**.
- Originals are never lost: every live tick change goes through the same saved-state and locking rules as the existing on/off.

## Non-goals

- No settings window, no timeout editing, no per-monitor wallpaper choice.
- No file browser built into Prezilla. The chooser is `zenity`'s file dialog.
- No change to the CLI commands or their output.
- No other desktops (GNOME, KDE, Windows, macOS).

## Anti-goals

- A tick change that leaves a switch half applied, or drops an original value, even if the change itself failed.
- A tick that shows a state different from what the switch really is after a failure.
- The menu freezing while a switch is being changed (the existing toggle already runs off the menu loop; this must too).

## Menu

Top to bottom (hidden items as today): the screensaver question items, **Presentation mode**, the timer line, **Reset timer to N min**, separator, the six switch tick boxes, **Choose wallpaper…**, separator, **About Prezilla**, separator, **Quit**.

Switch labels (my call; they name what happens when ticked, like the screenshot): **Hide desktop icons**, **Disable notifications**, **Keep screen awake** (the `presentation` switch), **Mute sound**, **Minimize windows**, **Change wallpaper**.

## Behavior

- **Tick while inactive**: only `config.json` changes.
- **Tick while active**: the engine applies that one switch under the state lock, adds its original value to the saved state, and keeps the deadline. If the switch fails, nothing is saved for it, the tick goes back to unticked, and the error shows in the tooltip and on stderr like other tray errors.
- **Untick while active**: the engine restores that switch from its saved value and removes it from the saved state. On failure the tick stays ticked and the error shows.
- **Change wallpaper without an image**: ticking opens the chooser. Cancelling leaves it unticked.
- **Choose wallpaper… while the wallpaper switch is active**: the new image is applied at once; the original wallpaper stays the saved original.
- **Choose wallpaper… when `zenity` is missing**: a notification tells the user to set `wallpaper` in `config.json`. Ticking **Change wallpaper** with no image and no `zenity` shows the same message and stays unticked.
- **Chosen file**: must exist and have an image extension (`png`, `jpg`, `jpeg`, `webp`, `bmp`, `gif`, `tif`, `tiff`, `svg`); otherwise the choice is rejected with a plain message.
- Ticks always reflect the real state on every refresh, as the checkbox for **Presentation mode** does today.

## Config rules

- The tray writes the whole of `config.json` atomically (`switches`, `wallpaper`, `timeout_minutes`), creating the directory if needed.
- Today `LoadConfig` adds the `wallpaper` switch whenever `wallpaper` is set. That would undo an unticked **Change wallpaper**, so my call: the switch is added automatically only when the file has no `switches` key at all. A hand-written file that sets `wallpaper` without `switches` behaves as before.
- If `config.json` cannot be parsed, the tick boxes are disabled with the existing config-error sentence instead of overwriting the file.

## Constraints

- No new runtime dependencies; the static no-cgo binary stays. `zenity` stays optional.
- `go vet`, `gofmt`, `go test -race ./...` clean; fakes for every new system interface (file chooser, config writer) in the same style as the existing ones.
- The README and CHANGELOG are updated (README stays short, no internal decisions).

## Details settled after review (my calls)

- **Where ticks come from while inactive**: `config.json`, re-read on every refresh (every 2 seconds), so a hand edit or a CLI change shows up in the menu. Every write is a read-modify-write of the latest file, so another editor's change is not overwritten.
- **Where ticks come from while active**: the saved state, because it is what is really applied. A switch that is in the config but failed to apply shows unticked; ticking it tries again. Unticking a ticked switch restores it and removes it from the config.
- **Order of engine action and config write**: the engine acts first. If the config write then fails (disk full, permissions), the real state wins: the tick shows what is applied, and an error says the choice could not be saved and holds for this session only. No rollback.
- **Unknown keys in `config.json`** are kept when the tray writes. Formatting is not kept.
- **Writing `switches`**: the tray writes `switches` only when the user changes a tick or the wallpaper. The first write stores the effective list, including `wallpaper` if it was added automatically. A `switches` key that is present, even empty, means exactly that list, with no automatic addition.
- **Chooser**: one at a time; a second click on **Choose wallpaper…** while the dialog is open does nothing. The result is applied under the state at the moment the dialog closes: if presentation mode is on and the wallpaper switch is active, the image is applied at once; otherwise it is only saved and the tick is set in the config.
- **Stale wallpaper path**: if the saved image no longer exists, ticking **Change wallpaper** or turning the mode on shows a plain error naming the file and pointing at **Choose wallpaper…**, and the tick stays off. It does not open the chooser by itself.

## Left to the builder

- The engine API for single-switch enable and disable, and how the tray reaches switch objects that are not in its current list (the tray's engine is built once from the config at start).
- Exact `zenity` arguments and the file filter, and the notification wording, in the style of the existing About and error texts.
- Whether ticks use one menu loop case per item or a small generic helper.
- How an unticked, never-applied switch interacts with a running watcher (the watcher already restores every saved switch).

## My calls (to check)

1. The entry is called **Choose wallpaper…**, not **Settings…**, because it holds only that one action.
2. The config rule above (auto-add of the wallpaper switch only without a `switches` key).
3. The set of accepted image extensions.
4. Labels for the tick boxes.

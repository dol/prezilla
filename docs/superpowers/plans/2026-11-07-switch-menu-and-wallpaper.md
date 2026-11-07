# Switch tick boxes and wallpaper choice Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** The tray menu gets one tick box per switch (changes apply at once, even while presenting, and are saved to `config.json`) and a **Choose wallpaper…** entry that picks the image.

**Architecture:** `core` gains a read-modify-write config writer and two engine operations that apply or restore one switch inside an active session under the state lock. A new `internal/tray/settings.go` holds all tick and wallpaper logic behind small interfaces (no systray import, fully testable with fakes); `tray.go` only creates the menu items and forwards clicks. `zenity --file-selection` is the file chooser, behind a `system.FileChooser` interface.

**Tech Stack:** Go 1.23, existing `fyne.io/systray`, `system.Xfconf`, `zenity` (optional, via `system.Runner`). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-11-07-switch-menu-and-wallpaper-design.md` (read it first; its "Details settled after review" section binds).

## Global Constraints

- Static binary, `CGO_ENABLED=0`, no new Go modules; `zenity` stays optional.
- `gofmt -l .` empty, `go vet ./...`, `go test -race -count=1 ./...` green after every task.
- User-visible name is `Prezilla`; identifiers, paths and the command stay lowercase.
- Every user-visible error is a plain sentence with a next step, added to the `internal/usererr` rules table with the technical text kept as `(details: …)`.
- Menu order (hidden items as today): question items, **Presentation mode**, timer line, **Reset timer to N min**, separator, six switch tick boxes, **Choose wallpaper…**, separator, **About Prezilla**, separator, **Quit**.
- Tick labels (ticked = part of presentation mode): `Hide desktop icons` (icons), `Disable notifications` (notifications), `Keep screen awake` (presentation), `Mute sound` (mute), `Minimize windows` (minimize), `Change wallpaper` (wallpaper). Entry label: `Choose wallpaper…`.
- Accepted image extensions (case-insensitive): `png jpg jpeg webp bmp gif tif tiff svg`.
- Saved-state formats of every existing switch stay unchanged.
- README stays short and free of internal decisions; CHANGELOG `[Unreleased]` updated.

## Review Focus

- Unticking the last applied switch while active leaves the mode active with nothing saved; a later `prezilla off` still succeeds and the deadline still ends it.
- `config.json` unparsable while the tray runs: ticks are disabled with the config sentence and the file is never overwritten.
- `prezilla off` (CLI) or the deadline ends the mode while a tick change is in flight: the engine operation reports "not active", nothing is half saved, the tick shows the real state.
- zenity prints a path with spaces, quotes, non-ASCII or a trailing newline; cancel (exit 1) is not an error.
- A hand-written file with `wallpaper` but no `switches` key, and a file with unknown keys: the first tray write stores the explicit switch list (including `wallpaper`) and keeps the unknown keys.
- The saved wallpaper file was deleted: ticking **Change wallpaper** or turning the mode on gives the stale-path sentence and changes nothing.

---

### Task 1: Config writer and the `switches` rule

**Files:**
- Modify: `internal/core/switch.go`, `internal/core/config.go`
- Test: `internal/core/config_test.go`

**Interfaces:**
- Produces: `var SwitchOrder = []string{IDIcons, IDNotifications, IDPresentation, IDMute, IDMinimize, IDWallpaper}`; `func (c Config) Has(id string) bool`; `func (c Config) With(id string, on bool) Config` (returns a copy; adding inserts at its `SwitchOrder` position, unknown IDs stay at the end, removing deletes; never mutates the receiver); `func UpdateConfig(path string, fn func(*Config)) (Config, error)`.

- [ ] **Step 1: Write failing tests** in `config_test.go`: `TestLoadConfigAutoAddsWallpaperOnlyWithoutSwitchesKey` (file `{"wallpaper":"/a.png"}` → switches = defaults + wallpaper; file `{"switches":["icons"],"wallpaper":"/a.png"}` → exactly `["icons"]`; `{"switches":[],"wallpaper":"/a.png"}` → empty); `TestWithKeepsCanonicalOrderAndDoesNotMutate`; `TestUpdateConfigKeepsUnknownKeys` (file with `{"x":{"y":1},"timeout_minutes":10}`, fn removes `mute` → file still has `x`, writes `switches` explicitly, `timeout_minutes` 10); `TestUpdateConfigWritesEffectiveListIncludingAutoAddedWallpaper`; `TestUpdateConfigMissingFileCreatesDirAndFile`; `TestUpdateConfigRefusesUnparsableFileAndLeavesItUntouched`; `TestUpdateConfigDropsEmptyWallpaperKey`.
- [ ] **Step 2: Run** `go test ./internal/core -run 'Config|With' ` → FAIL (undefined).
- [ ] **Step 3: Implement** the three functions and the `LoadConfig` rule change. `UpdateConfig` decodes the file into `map[string]json.RawMessage` for unknown keys, derives the `Config` with the same logic as `LoadConfig` (extract a shared helper), applies `fn`, then writes `switches` and `timeout_minutes` always and `wallpaper` only when non-empty, 2-space indented JSON plus newline, atomically (temp file in the same dir, fsync, rename; reuse the helper `Store.Save` uses if one exists), `MkdirAll` 0o755, file 0o644. Unparsable file → return the same `parse config` error and write nothing.
- [ ] **Step 4: Run** `go test -race ./internal/core` → PASS. Update the README config sentence only in Task 7.
- [ ] **Step 5: Commit** `feat(core): config writer that keeps unknown keys; wallpaper joins switches only without a switches key`.

### Task 2: Engine operations for one switch

**Files:**
- Modify: `internal/core/engine.go`
- Test: `internal/core/engine_test.go` (use the existing fake-switch helpers there)

**Interfaces:**
- Consumes: `Store.Lock/Load/Save`, `e.restorers` (all switches).
- Produces: `func (e *Engine) EnableOne(ctx context.Context, sw Switch) (bool, error)`; `func (e *Engine) DisableOne(ctx context.Context, id string) (bool, error)`; `func (e *Engine) SetSwitches(sw []Switch)` (replaces the list `Enable` uses; safe against a concurrent `Enable` via a mutex). The bool is `false` with a nil error when the mode is not active (nothing done).

- [ ] **Step 1: Write failing tests**: `TestEnableOneAppliesAndSavesWhenActive`; `TestEnableOneInactiveDoesNothing` (returns `false, nil`, switch not called); `TestEnableOneAlreadySavedIsNoop`; `TestEnableOneFailureSavesNothing`; `TestEnableOneSaveFailureRollsBack` (store save fails → switch's `Disable` called with the saved value, error returned); `TestEnableOneKeepsDeadline`; `TestDisableOneRestoresAndRemovesSaved`; `TestDisableOneFailureKeepsSaved`; `TestDisableOneLastSwitchKeepsModeActive` (`Active` stays true, `Saved` empty, deadline unchanged, a later `Disable` returns nil); `TestDisableOneUnknownIDIsNoop`; `TestSetSwitchesChangesNextEnable`.
- [ ] **Step 2: Run** `go test ./internal/core -run 'One|SetSwitches'` → FAIL.
- [ ] **Step 3: Implement** under `e.store.Lock()`. `DisableOne` finds the switch by ID in `e.restorers`, then `e.switches`; a saved ID with no switch returns an error naming it, like `Disable` does for orphans.
- [ ] **Step 4: Run** `go test -race ./internal/core` → PASS.
- [ ] **Step 5: Commit** `feat(core): apply or restore a single switch inside an active session`.

### Task 3: Stale wallpaper check, switch factory, error sentences

**Files:**
- Modify: `internal/platform/xfce/wallpaper.go`, `internal/platform/xfce/xfce.go`, `internal/platform/detect.go`, `internal/usererr/usererr.go`
- Test: `internal/platform/xfce/xfce_test.go`, `internal/platform/detect_test.go`, `internal/usererr/usererr_test.go`

**Interfaces:**
- Produces: `func New(b system.Backends, id, image string) (core.Switch, bool)` in `xfce` (false for an unknown ID); `func Switch(desktop string, b system.Backends, id, image string) (core.Switch, error)` in `platform`; wallpaper `Enable` returns an error whose text starts `wallpaper: image not found: <path>` when `image` is set but the file does not exist (before any xfconf call).
- New usererr sentences (exact): stale image — `Couldn't change the wallpaper: the image <path> no longer exists. Choose another image from the tray menu, or fix "wallpaper" in config.json.`; config save — `Couldn't save your choice to <path>. It stays in effect until presentation mode ends, then the saved setting applies again.`; error text produced by `UpdateConfig`'s write step starts `save config <path>:`.

- [ ] **Step 1: Write failing tests**: wallpaper `Enable` with a missing file makes no `Set`/`List` call and returns the error; `New` returns every known ID and `false` for `"bogus"`; `platform.Switch` rejects an unsupported desktop; usererr rows for both new sentences (path inserted, details preserved, restore variants not needed).
- [ ] **Step 2: Run** the three packages' tests → FAIL.
- [ ] **Step 3: Implement** with `os.Stat` in `Enable`; `New` reuses the constructors `Switches` and `All` use; add the usererr rules in the file's existing style.
- [ ] **Step 4: Run** `go test -race ./...` → PASS.
- [ ] **Step 5: Commit** `feat(xfce): single-switch factory and a stale-image error for the wallpaper switch`.

### Task 4: Image file chooser

**Files:**
- Create: `internal/system/chooser.go`
- Test: `internal/system/chooser_test.go`

**Interfaces:**
- Produces: `type FileChooser interface { ChooseImage(ctx context.Context, startDir string) (string, error) }`; `var ErrCancelled = errors.New("file chooser: cancelled")`; `type ZenityChooser struct{ Runner Runner }` (missing zenity returns an error wrapping the existing `ErrNoZenity`); `type FakeChooser struct{ Path string; Err error; Calls []string }` (records `startDir`); `func ValidateImage(path string) error` (errors wrap new `ErrImageMissing` for a missing or non-regular file and `ErrNotImage` for a bad extension, messages contain the path).
- zenity call: `--file-selection --title=Choose wallpaper --filename=<startDir>/ --file-filter=Images | <*.ext for each accepted extension, lower and upper case>`; stdout is the path with the trailing newline trimmed; exit code 1 means cancelled; other failures are returned wrapped.

- [ ] **Step 1: Write failing tests** with `FakeRunner`: `TestZenityChooserReturnsTrimmedPath` (path with spaces, quotes and non-ASCII), `TestZenityChooserExit1IsErrCancelled`, `TestZenityChooserMissingIsErrNoZenity`, `TestZenityChooserArgs` (asserts the filter lists all nine extensions and `--filename` ends with `/`), `TestValidateImage` (table: ok png, uppercase `.JPG`, missing file, directory, `.txt`, no extension).
- [ ] **Step 2: Run** `go test ./internal/system -run 'Chooser|ValidateImage'` → FAIL.
- [ ] **Step 3: Implement.** Look at how `ZenityAbout` detects exit codes and a missing binary and reuse that approach.
- [ ] **Step 4: Run** `go test -race ./internal/system` → PASS.
- [ ] **Step 5: Commit** `feat(system): zenity image chooser and image validation`.

### Task 5: Settings controller (all tick and wallpaper logic)

**Files:**
- Create: `internal/tray/settings.go`
- Test: `internal/tray/settings_test.go`

**Interfaces:**
- Consumes: Task 1 `core.UpdateConfig`, `Config.Has/With`, `core.LoadConfig`; Task 2 engine methods; Task 3 usererr rules; Task 4 `system.FileChooser`, `ValidateImage`, `ErrCancelled`, `ErrNoZenity`.
- Produces: `type settingsEngine interface { Status() (core.State, error); EnableOne(context.Context, core.Switch) (bool, error); DisableOne(context.Context, string) (bool, error) }`; `type tickItem struct{ ID, Title, Tooltip string }`; `var tickItems []tickItem` (six, in `SwitchOrder`, with the exact labels from Global Constraints); `const chooseWallpaperTitle = "Choose wallpaper…"`; `type settings struct { ConfigPath string; Engine settingsEngine; SwitchFor func(id, image string) (core.Switch, error); Chooser system.FileChooser; Notify func(ctx context.Context, summary, body string) error; StartDir string }` with methods `Ticks() (map[string]bool, error)`, `SetTick(ctx context.Context, id string, on bool) error`, `ChooseWallpaper(ctx context.Context) error`.
- Behavior (all from the spec): `Ticks` reads the saved state when the mode is active (a ticked switch is one with a saved entry) and `config.json` otherwise, returning the config error when the file cannot be parsed. `SetTick` engine-first: active → `EnableOne` (switch built with the configured image) or `DisableOne`, then `UpdateConfig`; inactive → `UpdateConfig` only. Ticking `wallpaper` with no image configured runs `ChooseWallpaper` instead; a stale image returns the stale-path error without changes; a failed engine call changes nothing and returns the error; a failed config write after a successful engine call returns the save error (real state wins). `ChooseWallpaper`: `ErrCancelled` → nil and no change; `ErrNoZenity` → `Notify` with summary `Choose wallpaper` and body `Prezilla needs zenity to browse for an image. Install zenity, or set "wallpaper" in config.json.`, return nil; `ValidateImage` failure → returned error; success → config gets the path and the `wallpaper` tick; when the mode is active the wallpaper switch is applied at once (restore the saved original first with `DisableOne`, then `EnableOne` with a switch built for the new image).

- [ ] **Step 1: Write failing tests** with fakes (a `fakeSettingsEngine` recording calls and a `core.State`, `system.FakeChooser`, temp config file): `TestTicksInactiveReadConfig`, `TestTicksActiveReadSaved` (config ticks mute, saved lacks it → unticked), `TestTicksBrokenConfigReturnsError`, `TestSetTickInactiveWritesConfigOnly`, `TestSetTickActiveEnablesThenWrites`, `TestSetTickActiveUntickRestoresThenWrites`, `TestSetTickEngineFailureWritesNothing`, `TestSetTickConfigWriteFailureReturnsSaveError`, `TestSetTickNotActiveAnymoreFallsBackToConfigOnly` (engine returns `false, nil`), `TestSetTickWallpaperWithoutImageOpensChooser`, `TestSetTickWallpaperStaleImage`, `TestChooseWallpaperCancelled`, `TestChooseWallpaperNoZenityNotifies`, `TestChooseWallpaperRejectsNonImage`, `TestChooseWallpaperAppliesAtOnceWhenActive` (call order `DisableOne(wallpaper)` then `EnableOne`), `TestChooseWallpaperInactiveOnlySaves`.
- [ ] **Step 2: Run** `go test ./internal/tray -run 'Ticks|SetTick|ChooseWallpaper'` → FAIL.
- [ ] **Step 3: Implement** `settings.go`; no systray import.
- [ ] **Step 4: Run** `go test -race ./internal/tray` → PASS.
- [ ] **Step 5: Commit** `feat(tray): settings controller for switch ticks and the wallpaper image`.

### Task 6: Menu items, refresh and wiring

**Files:**
- Modify: `internal/tray/tray.go`, `cmd/prezilla/main.go`
- Test: `internal/tray/tray_test.go`

**Interfaces:**
- Consumes: Task 5 `settings`, `tickItems`, `chooseWallpaperTitle`; Task 3 `platform.Switch`; Task 2 `Engine.SetSwitches`.
- Produces: new `tray.Config` fields `ConfigPath string`, `Chooser system.FileChooser`, `SwitchFor func(id, image string) (core.Switch, error)`, `SwitchesFor func(core.Config) ([]core.Switch, error)`; a pure `tickModel(ticks map[string]bool, err error) []itemState` for the six items (disabled when `err != nil`).

- [ ] **Step 1: Write failing tests**: `TestTickModelLabelsAndOrder`, `TestTickModelDisabledOnConfigError`, `TestTickModelChecksFollowTicks`.
- [ ] **Step 2: Run** `go test ./internal/tray -run TickModel` → FAIL.
- [ ] **Step 3: Implement in `tray.go`**: create the separator, six checkbox items and **Choose wallpaper…** between **Reset timer** and **About Prezilla**, plus the extra separator, in menu order; `refresh()` sets each tick from `settings.Ticks()` (config error text joins the tooltip once, like the other errors) and disables the items on error. One forwarding goroutine per tick item sends its ID to a buffered channel; the menu loop case starts the change on a second `toggler` (one tick change at a time, detached context, bounded by `core.OperationTimeout`) with `on = !currentTick`, records the error in an `actionState`, and calls `refresh()` when done. **Choose wallpaper…** runs on its own `toggler` so ticks stay usable while the dialog is open. Before the tray turns the mode on (`toggle`), reload `config.json` with `LoadConfig` and call `Engine.SetSwitches(SwitchesFor(cfg))`; a load error keeps the current list. In `main.go` pass `ConfigPath: core.DefaultConfigPath()`, `Chooser: system.ZenityChooser{Runner: system.ExecRunner{Timeout: 30 * time.Minute, Dir: "/"}}`, `SwitchFor` and `SwitchesFor` built on `platform.Switch` and `platform.Switches`, `StartDir` the user's home directory.
- [ ] **Step 4: Run** `go test -race -count=1 ./...`, `go vet ./...`, `CGO_ENABLED=0 go build ./cmd/prezilla` → PASS. Verify the menu on a throwaway session only if one is available; otherwise report as not verified.
- [ ] **Step 5: Commit** `feat(tray): switch tick boxes and Choose wallpaper… in the menu`.

### Task 7: Docs

**Files:**
- Modify: `README.md`, `CHANGELOG.md`

- [ ] **Step 1:** README: replace the config paragraph's wallpaper sentence with the new rule, add two sentences under **Use** (tick boxes apply at once and are saved; **Choose wallpaper…** picks the image and needs `zenity` for the chooser), keep the README short and free of internal decisions (follow the `writing-guidelines` rules: one paragraph per line, no dashes as punctuation, sentence-case headings).
- [ ] **Step 2:** CHANGELOG `[Unreleased]` Added entries for the tick boxes, the chooser and the config rule (a `wallpaper` key adds the switch only when `switches` is absent), plus the stale-image error.
- [ ] **Step 3: Run** `go build ./... && git diff --stat`.
- [ ] **Step 4: Commit** `docs: tick boxes and wallpaper choice`.

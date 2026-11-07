# Prezilla Core + XFCE Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A Go binary `prezilla` that flips a machine into "presentation mode" and back, via a tray icon and a CLI, working on XFCE (X11) first, with a core that GNOME/KDE/Windows/macOS backends plug into later.

**Architecture:** A platform-neutral `core` package defines `Switch` (enable returns an opaque saved string, disable restores from it), an `Engine` that applies the configured switches and persists what it saved to a state file (so CLI and tray share state and restore survives restarts). Platform backends implement `Switch` by shelling out through an injectable `system.Runner`, so they are unit-tested with a fake runner. The CLI and the tray are thin front-ends over the Engine in one binary (`prezilla tray` runs the tray).

**Tech Stack:** Go 1.23+ (stdlib `testing`), `fyne.io/systray` (StatusNotifierItem over D-Bus on Linux, no cgo there; also covers Windows/macOS later), XFCE tools `xfconf-query`, `pactl`, `wmctrl`.

**Spec:** None written. Source is the user's request ("similar to https://www.togglepresent.app/, multi platform, XFCE first, tray icon + CLI, name `prezilla`") and the TogglePresent feature list: one click toggles five switches — hide desktop icons, disable notifications, mute speaker, minimize active apps, change wallpaper — and a second click restores everything. Other platforms (GNOME, KDE, Windows, macOS) are out of scope here and get their own plans, each only adding a `Switch` set plus a `Detect` branch.

## Global Constraints

- Binary and app name: `prezilla`. Go module path: `prezilla`.
- Switch IDs (exact): `icons`, `notifications`, `mute`, `minimize`, `wallpaper`.
- Toggling twice must leave the system exactly as it was (restore original values, never forced defaults).
- CLI subcommands (exact): `on`, `off`, `toggle`, `status`, `tray`, `version`. Exit codes: 0 success, 1 runtime error, 2 usage error.
- Config: `os.UserConfigDir()/prezilla/config.json`. State: `os.UserStateDir`-style, i.e. `$XDG_STATE_HOME/prezilla/state.json`, falling back to `~/.local/state/prezilla/state.json`.
- No dependencies beyond stdlib except `fyne.io/systray` (tray task only).
- XFCE on X11 only; Wayland sessions are not a target of this plan.
- Auto-disable timeout: default 30 minutes, config key `timeout_minutes` (0 = no timeout); `prezilla on --for <duration>` (Go duration, e.g. `45m`) overrides, `--for 0` means none. Deadline stored in state as `expires_at`. Expiry must still fire after the laptop was suspended past the deadline (deadline compared to wall clock, never a single long sleep).
- `status` prints `inactive`, `active`, or `active until HH:MM` (local 24h time) when a deadline is set, plus newline.

## Review Focus

1. Enabling twice (CLI `on` then tray toggle on) must not overwrite the saved originals with presentation values — Task 3.
2. One switch failing (e.g. `wmctrl` or `pactl` not installed) must not stop the others from applying/restoring, and the error must name the switch and tool — Task 3, Task 5.
3. User was already muted / already in do-not-disturb / desktop already hidden before `on`: `off` must leave it that way — Task 4.
4. Multi-monitor / multi-workspace wallpaper: every `last-image` property restored, not just the first — Task 5.
5. Corrupt or unreadable `state.json`: error naming the file path, no silent reset that would lose originals — Task 2.
6. Running on an unsupported desktop (no `XDG_CURRENT_DESKTOP` match): CLI exits 1 with `unsupported desktop "<value>"` — Task 6.
7. No tray host available (no StatusNotifier watcher): `prezilla tray` exits 1 with a hint to use the CLI instead of hanging — Task 8.
11. Installed package without its icon or desktop entry (missing size, wrong `Icon=` name) looks broken in menus and the panel: `scripts/check-install.sh` over a `DESTDIR` install and over the extracted `.deb` — Task 10.
12. A packaged binary that reports `dev` instead of the release version, or a release built without the pinned tag version — Task 10 (`prezilla version` on the extracted deb).
8. Deadline passes while the machine is suspended: next poll/watcher wake restores immediately — Task 3 (`DisableIfExpired` with injected clock) and Task 6 (watcher polls).
9. Running `on --for 10m` while already active only moves the deadline; switches are not re-applied and originals are not overwritten — Task 3.
10. Nobody runs the tray: the CLI alone must still enforce the timeout (detached `prezilla watch`), and `off` makes a leftover watcher exit — Task 6.

## File Structure

- `go.mod`, `.gitignore`
- `cmd/prezilla/main.go` — wires real deps, calls `cli.Run`.
- `internal/system/runner.go` — `Runner` interface, `ExecRunner`, `FakeRunner`.
- `internal/core/switch.go` — `Switch` interface, switch ID constants.
- `internal/core/config.go` — `Config`, `DefaultConfig`, `LoadConfig`.
- `internal/core/state.go` — `State`, `Store`.
- `internal/core/engine.go` — `Engine`.
- `internal/platform/detect.go` — `Detect`, `Switches`.
- `internal/platform/xfce/*.go` — one file per switch + `xfce.go` constructor.
- `internal/cli/cli.go` — `Run`.
- `internal/tray/tray.go` — tray front-end.
- `packaging/prezilla.desktop`, `README.md`.
- `packaging/icons/**`, `scripts/render-icons.sh`, `internal/tray/icons/` — icon sources, rendered hicolor set, embedded tray icons (Task 7).
- `Makefile`, `nfpm.yaml`, `CHANGELOG.md`, `LICENSE`, `.github/workflows/`, `scripts/check-install.sh` — build, install, packages, CI (Task 10).

---

### Task 1: Scaffold and command runner

**Files:**
- Create: `go.mod`, `.gitignore`, `internal/system/runner.go`, `internal/system/runner_test.go`

**Interfaces:**
- Produces:
  - `type Runner interface { Run(ctx context.Context, name string, args ...string) (string, error) }` — returns trimmed stdout; on non-zero exit or missing binary returns error containing the command name and stderr.
  - `type ExecRunner struct{}` implementing `Runner` via `exec.CommandContext`.
  - `type FakeRunner struct { Calls []string; Out map[string]string; Err map[string]error }` implementing `Runner`; a call is recorded and looked up as `strings.Join(append([]string{name}, args...), " ")`. Unknown keys return `("", nil)`.

- [ ] **Step 1: `go mod init prezilla`, set `go 1.23`, add `.gitignore` containing `/prezilla` and `/dist/`.**
- [ ] **Step 2: Write failing tests** in `runner_test.go`: `TestExecRunnerTrimsStdout` (runs `echo hi`, expects `"hi"`), `TestExecRunnerMissingBinaryErrorNamesCommand` (runs `prezilla-no-such-bin`, expects error containing that name), `TestExecRunnerNonZeroIncludesStderr` (`sh -c "echo boom >&2; exit 3"`, error contains `boom`), `TestFakeRunnerRecordsAndReplays`.
- [ ] **Step 3: Run** `go test ./internal/system/...` — Expected: FAIL (undefined).
- [ ] **Step 4: Implement** `runner.go` as specified.
- [ ] **Step 5: Run** `go test ./internal/system/...` — Expected: PASS.
- [ ] **Step 6: Commit** `git add -A && git commit -m "feat: scaffold module and command runner"`

---

### Task 2: Config and state store

**Files:**
- Create: `internal/core/switch.go`, `internal/core/config.go`, `internal/core/state.go`, `internal/core/config_test.go`, `internal/core/state_test.go`

**Interfaces:**
- Produces:
  - `const (IDIcons="icons"; IDNotifications="notifications"; IDMute="mute"; IDMinimize="minimize"; IDWallpaper="wallpaper")`
  - `type Switch interface { ID() string; Enable(ctx context.Context) (saved string, err error); Disable(ctx context.Context, saved string) error }`
  - `type Config struct { Switches []string \`json:"switches"\`; Wallpaper string \`json:"wallpaper"\`; TimeoutMinutes int \`json:"timeout_minutes"\` }`
  - `func DefaultConfig() Config` — all IDs except `wallpaper`, `Wallpaper: ""`, `TimeoutMinutes: 30`. A config file that omits `timeout_minutes` keeps 30 (decode onto defaults); a negative value is an error naming `path`.
  - `func (c Config) Timeout() time.Duration`
  - `func LoadConfig(path string) (Config, error)` — missing file → `DefaultConfig()`, nil; invalid JSON → error containing `path`; if `Wallpaper != ""` and `wallpaper` not in `Switches` it is appended.
  - `type State struct { Active bool \`json:"active"\`; Saved map[string]string \`json:"saved"\`; ExpiresAt time.Time \`json:"expires_at"\` }` (zero `ExpiresAt` = no deadline)
  - `type Store struct{ ... }`; `func NewStore(path string) *Store`; `func (s *Store) Load() (State, error)` (missing file → zero `State`, nil; corrupt → error containing path); `func (s *Store) Save(State) error` (atomic: write temp file in same dir, `os.Rename`; creates parent dirs, mode 0600).
  - `func DefaultStatePath() string`, `func DefaultConfigPath() string` per Global Constraints.

- [ ] **Step 1: Write failing tests:** `TestLoadConfigMissingFileGivesDefaults`, `TestLoadConfigWallpaperImpliesSwitch`, `TestLoadConfigInvalidJSONNamesPath`, `TestStoreRoundTrip`, `TestStoreMissingFileIsZeroState`, `TestStoreCorruptFileErrorNamesPathAndKeepsFile` (file bytes unchanged after failed Load), `TestStoreSaveCreatesParentDirs`.
- [ ] **Step 2: Run** `go test ./internal/core/...` — Expected: FAIL.
- [ ] **Step 3: Implement** the above.
- [ ] **Step 4: Run** `go test ./internal/core/...` — Expected: PASS.
- [ ] **Step 5: Commit** `git commit -am "feat: config and state store"` (after `git add -A`).

---

### Task 3: Engine

**Files:**
- Create: `internal/core/engine.go`, `internal/core/engine_test.go`

**Interfaces:**
- Consumes: `Switch`, `State`, `Store` from Task 2.
- Produces:
  - `func NewEngine(switches []Switch, store *Store, now func() time.Time) *Engine` — switches applied in given order, restored in reverse; `now` is the injected clock (`time.Now` in production).
  - `func (e *Engine) Enable(ctx context.Context, timeout time.Duration) error` — `timeout <= 0` means no deadline.
  - `func (e *Engine) Disable(ctx context.Context) error`
  - `func (e *Engine) Toggle(ctx context.Context, timeout time.Duration) (active bool, err error)`
  - `func (e *Engine) DisableIfExpired(ctx context.Context) (disabled bool, err error)` — if active and `ExpiresAt` non-zero and `!now().Before(ExpiresAt)`, calls `Disable`.
  - `func (e *Engine) Status() (State, error)`
  - Test helper in the test file: `fakeSwitch{id string; enableErr, disableErr error; enabled, disabled int; savedVal string}`.

Semantics: `Enable` sets `ExpiresAt = now()+timeout` (zero if `timeout <= 0`). `Enable` when `State.Active` is true calls no switch and leaves `Saved` untouched; it only replaces `ExpiresAt`. `Enable` runs every switch even if earlier ones fail; successes are stored in `Saved[id]`; errors are combined with `errors.Join`, each wrapped as `"<id>: %w"`; `Active` becomes true if at least one switch succeeded. `Disable` when inactive is a no-op. `Disable` restores every switch in `Saved` in reverse order; successfully restored entries are deleted from `Saved`; failures stay in `Saved` and are joined into the returned error; `Active` becomes false only when `Saved` is empty. State is saved after each of Enable/Disable.

- [ ] **Step 1: Write failing tests:** `TestEnableSavesEachSwitchValueAndMarksActive`, `TestEnableTwiceDoesNotCallSwitchesAgainOrOverwriteSaved`, `TestEnableContinuesAfterFailureAndReportsSwitchID` (error string contains `"mute:"`, other switches enabled and saved), `TestEnableAllFailLeavesInactive`, `TestDisablePassesSavedValueBackInReverseOrder`, `TestDisableFailureKeepsEntryAndStaysActive` then a retry succeeds, `TestDisableWhenInactiveIsNoop`, `TestToggleReturnsNewState`, `TestStateSurvivesNewEngineSameStore` (Enable with engine A, Disable with engine B on same store restores), `TestEnableSetsExpiresAtFromClock`, `TestEnableZeroTimeoutHasNoDeadline`, `TestEnableWhileActiveOnlyMovesDeadline`, `TestDisableIfExpiredBeforeDeadlineDoesNothing`, `TestDisableIfExpiredAfterJumpPastDeadlineRestores` (clock advanced by hours, simulating suspend), `TestDisableIfExpiredWithoutDeadlineNeverFires`, `TestDisableClearsExpiresAt`.
- [ ] **Step 2: Run** `go test ./internal/core/...` — Expected: FAIL.
- [ ] **Step 3: Implement** `Engine` per the semantics above.
- [ ] **Step 4: Run** `go test ./internal/core/...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat: presentation engine`.

---

### Task 4: XFCE icons, notifications, mute switches

**Files:**
- Create: `internal/platform/xfce/icons.go`, `notifications.go`, `mute.go`, `xfce_test.go`

**Interfaces:**
- Consumes: `system.Runner`/`FakeRunner`, `core.Switch`, ID constants.
- Produces: `func NewIcons(r system.Runner) core.Switch`, `func NewNotifications(r system.Runner) core.Switch`, `func NewMute(r system.Runner) core.Switch`.

Exact commands (FakeRunner keys in tests):
- icons: read `xfconf-query -c xfce4-desktop -p /desktop-icons/style`; enable sets `xfconf-query -c xfce4-desktop -p /desktop-icons/style -n -t int -s 0`; saved = value read, or `""` if the read errors (property unset); disable with saved `""` runs `xfconf-query -c xfce4-desktop -p /desktop-icons/style -r`, otherwise sets `-n -t int -s <saved>`.
- notifications: read `xfconf-query -c xfce4-notifyd -p /do-not-disturb` (error → saved `"false"`); enable sets `... -n -t bool -s true`; disable sets `... -n -t bool -s <saved>`.
- mute: read `pactl get-sink-mute @DEFAULT_SINK@` (output `Mute: yes|no`, saved = `yes`/`no`; read error is returned as the Enable error, tool name in message); enable runs `pactl set-sink-mute @DEFAULT_SINK@ 1`; disable runs `... 0` only if saved is `no`, else does nothing.

- [ ] **Step 1: Write failing tests:** `TestIconsEnableHidesAndSavesPrevious` (Out `"2"`, asserts set-to-0 call and saved `"2"`), `TestIconsDisableRestoresPrevious`, `TestIconsUnsetPropertyIsRemovedOnRestore`, `TestNotificationsAlreadyDNDStaysDNDAfterDisable` (saved `"true"` → set true), `TestMuteAlreadyMutedStaysMuted` (no unmute call), `TestMuteRestoresUnmuted`, `TestMuteReadFailureErrorMentionsPactl`.
- [ ] **Step 2: Run** `go test ./internal/platform/...` — Expected: FAIL.
- [ ] **Step 3: Implement** the three constructors; IDs from `core` constants.
- [ ] **Step 4: Run** `go test ./internal/platform/...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(xfce): icons, notifications, mute switches`.

---

### Task 5: XFCE minimize and wallpaper switches

**Files:**
- Create: `internal/platform/xfce/minimize.go`, `wallpaper.go`; Modify: `xfce_test.go`

**Interfaces:**
- Produces: `func NewMinimize(r system.Runner) core.Switch`, `func NewWallpaper(r system.Runner, image string) core.Switch`.

Exact behavior:
- minimize: read `wmctrl -m`; saved = `"on"` if output contains `mode: ON` else `"off"`; enable runs `wmctrl -k on`; disable runs `wmctrl -k off` only if saved is `"off"`. Missing `wmctrl` surfaces as the Enable error with `wmctrl` in the message.
- wallpaper: list with `xfconf-query -c xfce4-desktop -l`, keep lines ending in `/last-image`; for each read `xfconf-query -c xfce4-desktop -p <prop>`; saved = JSON object `{prop: value}`; enable sets each prop with `xfconf-query -c xfce4-desktop -p <prop> -s <image>`; disable parses saved JSON and sets each prop back. Zero matching properties → Enable error `"no wallpaper properties found"`.

- [ ] **Step 1: Write failing tests:** `TestMinimizeEnableShowsDesktopAndSavesState`, `TestMinimizeAlreadyShowingDesktopNotRestored`, `TestMinimizeMissingWmctrlErrorMentionsTool`, `TestWallpaperChangesEveryMonitorAndWorkspaceProperty` (two props in fake list plus one non-matching prop), `TestWallpaperDisableRestoresEachOriginal`, `TestWallpaperNoPropertiesErrors`.
- [ ] **Step 2: Run** `go test ./internal/platform/...` — Expected: FAIL.
- [ ] **Step 3: Implement** both.
- [ ] **Step 4: Run** `go test ./internal/platform/...` — Expected: PASS.
- [ ] **Step 5: Commit** `feat(xfce): minimize and wallpaper switches`.

---

### Task 6: Platform detection, CLI, main

**Files:**
- Create: `internal/platform/detect.go`, `internal/platform/xfce/xfce.go`, `internal/platform/detect_test.go`, `internal/cli/cli.go`, `internal/cli/cli_test.go`, `cmd/prezilla/main.go`

**Interfaces:**
- Consumes: everything above; `tray` is injected as a func so this task does not depend on Task 7.
- Produces:
  - `xfce.Switches(r system.Runner, cfg core.Config) []core.Switch` — builds only IDs listed in `cfg.Switches`, in config order; unknown IDs are ignored.
  - `platform.Detect(getenv func(string) string) (string, error)` — splits `XDG_CURRENT_DESKTOP` on `:`, case-insensitive `xfce` → `"xfce"`; otherwise error `unsupported desktop "<value>"`.
  - `platform.Switches(desktop string, r system.Runner, cfg core.Config) ([]core.Switch, error)`.
  - `type cli.Deps struct { Engine *core.Engine; Tray func(ctx context.Context) error; SpawnWatcher func() error; DefaultTimeout time.Duration; PollInterval time.Duration; Version string }`
  - CLI commands now: `on [--for DURATION]` (timeout = `--for` if given else `DefaultTimeout`; invalid duration → exit 2), `watch` (internal; loops every `PollInterval` calling `DisableIfExpired`, returns 0 once state is inactive or after a disable), plus `off`, `toggle [--for DURATION]`, `status`, `tray`, `version`. `on`/`toggle` that end active with a deadline call `SpawnWatcher`; `status` and `toggle` first call `DisableIfExpired`.
  - `main.go` implements `SpawnWatcher` by starting `os.Executable() watch` detached (`SysProcAttr{Setsid: true}`, stdio to `/dev/null`); `PollInterval` 10s.
  - `func cli.Run(ctx context.Context, args []string, out, errOut io.Writer, d Deps) int`.
  - `cmd/prezilla/main.go`: loads config (`core.DefaultConfigPath`), detects desktop from `os.Getenv`, builds engine with `core.NewStore(core.DefaultStatePath())`; on detect/config error prints message to stderr and exits 1 (except `version`, which works anywhere); `Version` set via `-ldflags "-X main.version=..."`, default `dev`.

- [ ] **Step 1: Write failing tests:** `TestDetectXFCE` (`"XFCE"`, `"xfce"`, `"X-Cinnamon:XFCE"`), `TestDetectUnsupportedNamesValue` (`"GNOME"` and `""`), `TestSwitchesRespectsConfigOrderAndIgnoresUnknown`, CLI: `TestRunOnEnablesAndExitsZero`, `TestRunOffRestores`, `TestRunToggle`, `TestRunStatusPrintsActiveOrInactive`, `TestRunUnknownCommandExits2WithUsage`, `TestRunNoArgsExits2`, `TestRunVersionPrintsVersion`, `TestRunEngineErrorExits1AndPrintsToStderr`, `TestRunTrayCallsDepsTray`, `TestRunOnUsesDefaultTimeout`, `TestRunOnForOverridesTimeout`, `TestRunOnForZeroMeansNoDeadline`, `TestRunOnForInvalidDurationExits2`, `TestRunOnSpawnsWatcherOnlyWhenDeadlineSet`, `TestRunStatusShowsUntilTime`, `TestRunStatusExpiredRestoresAndPrintsInactive`, `TestRunWatchDisablesAfterDeadlineWithFakeClock`, `TestRunWatchExitsWhenAlreadyInactive`. CLI tests use an Engine built from fake `core.Switch` values and a temp `Store`.
- [ ] **Step 2: Run** `go test ./...` — Expected: FAIL.
- [ ] **Step 3: Implement** the above.
- [ ] **Step 4: Run** `go test ./...` — Expected: PASS; then `go build -o prezilla ./cmd/prezilla && ./prezilla version` prints `dev`.
- [ ] **Step 5: Manual XFCE check** (this machine is XFCE): `./prezilla on; ./prezilla status; ./prezilla off`. Expected: desktop icons vanish and return, notification DND toggles (`xfconf-query -c xfce4-notifyd -p /do-not-disturb`), default sink mutes and unmutes, windows hide and return. Install `wmctrl` if missing (`sudo apt install wmctrl`) and record any deviation in the commit message.
- [ ] **Step 6: Commit** `feat: platform detection, cli and entrypoint`.

---

### Task 7: Icon assets

**Files:**
- Create: `packaging/icons/prezilla.svg`, `packaging/icons/prezilla-tray-idle.svg`, `packaging/icons/prezilla-tray-active.svg`, `scripts/render-icons.sh`, `packaging/icons/hicolor/scalable/apps/prezilla.svg` (copy of `prezilla.svg`), `packaging/icons/hicolor/<S>x<S>/apps/prezilla.png` for S in 16, 22, 24, 32, 48, 64, 128, 256, 512, `internal/tray/icons/idle.png`, `internal/tray/icons/active.png`, `internal/tray/icons/icons.go`, `internal/tray/icons/icons_test.go`

Design (decided; in the style of the sibling project `/home/dol/project/tunewise/icons/tunewise.png`: one bold flat silhouette, rounded joins, 512×512 viewBox, transparent background): a presentation screen (large rounded rectangle) on a two-legged easel, with a toggle switch (white pill, round knob) centred on the screen. App icon: teal `#1F9D8A` silhouette, white pill, knob right. Tray idle: grey `#8A8F98`, knob left. Tray active: green `#2ECC71`, knob right. No stroke or gap thinner than 32 units, so it stays readable at 16 px.

**Interfaces:**
- Produces: `package icons` (`internal/tray/icons`) with `//go:embed` variables `var Idle []byte` and `var Active []byte` (64×64 PNG each); the hicolor tree above, which later tasks install verbatim.

- [ ] **Step 1: Write failing tests** in `icons_test.go`: `TestTrayIconsAre64x64PNG` (both decode, bounds 64×64), `TestTrayIconsDiffer` (bytes unequal), `TestTrayIconsAreNotBlank` (at least 5% of pixels have alpha > 0, and fewer than 80% so it is a glyph rather than a filled square), `TestHicolorSetIsComplete` (for each S listed above `../../../packaging/icons/hicolor/SxS/apps/prezilla.png` exists and decodes to S×S; scalable SVG exists and starts with `<svg` or `<?xml`).
- [ ] **Step 2: Run** `go test ./internal/tray/icons/...` — Expected: FAIL.
- [ ] **Step 3: Write the three SVGs** per the design above.
- [ ] **Step 4: Write `scripts/render-icons.sh`** (bash, `set -euo pipefail`): renders every size with `rsvg-convert -w S -h S` into the hicolor tree, renders the two tray SVGs at 64×64 into `internal/tray/icons/`, copies the scalable SVG. The generated PNGs are committed, so building or packaging does not need `rsvg-convert` (package `librsvg2-bin`, already present on this machine).
- [ ] **Step 5: Run** `bash scripts/render-icons.sh`, then implement `icons.go` with `go:embed idle.png active.png`.
- [ ] **Step 6: Run** `go test ./internal/tray/icons/...` — Expected: PASS. Then look at the 16 px and 512 px PNGs and the idle/active pair (open them with the Read tool) and confirm the glyph is recognisable at 16 px; adjust the SVGs and re-render if not.
- [ ] **Step 7: Commit** `feat: icon assets and embedded tray icons`.

---

### Task 8: Tray front-end and autostart

**Files:**
- Create: `internal/tray/tray.go`, `internal/tray/tray_test.go`, `packaging/prezilla.desktop`; Modify: `cmd/prezilla/main.go` (set `Deps.Tray`, replacing the "tray not built yet" stub), `go.mod`

**Interfaces:**
- Consumes: `core.Engine` (`Toggle`, `Status`, `DisableIfExpired`); `icons.Idle` / `icons.Active` from Task 7.
- Produces:
  - `func Labels(active bool) (title, tooltip string)` — `("Presenting", "prezilla: presentation mode on")` / `("Not presenting", "prezilla: presentation mode off")`.
  - `func Icon(active bool) []byte` — returns `icons.Active` when active, else `icons.Idle` (64×64 PNG).
  - `func Run(ctx context.Context, e *core.Engine, timeout time.Duration) error` — uses `fyne.io/systray`: each 2 s poll first calls `e.DisableIfExpired`, and the tooltip appends the remaining time (`, N min left`) when a deadline exists; one menu item showing `Labels(active).title` that calls `e.Toggle(ctx, timeout)` on click (errors shown by updating the tooltip and writing to stderr), a `Quit` item; polls `e.Status()` every 2 s so CLI changes are reflected in icon, title and tooltip.
- `packaging/prezilla.desktop`: `[Desktop Entry]` with `Type=Application`, `Name=prezilla`, `Exec=prezilla tray`, `X-GNOME-Autostart-enabled=true`, `Icon=prezilla`, `Terminal=false`.

- [ ] **Step 1: Write failing tests:** `TestLabelsActiveAndInactive` (exact strings above), `TestIconReturnsEmbeddedPNGPerState` (`Icon(true)` equals `icons.Active`, `Icon(false)` equals `icons.Idle`, and they differ).
- [ ] **Step 2: Run** `go test ./internal/tray/...` — Expected: FAIL.
- [ ] **Step 3: Implement** `Labels` and `Icon`; add dependency with `go get fyne.io/systray@latest`.
- [ ] **Step 4: Run** `go test ./internal/tray/...` — Expected: PASS.
- [ ] **Step 5: Implement `Run`** with systray; if registering the tray fails (no StatusNotifier host), return an error whose text includes `use "prezilla toggle" from the command line`.
- [ ] **Step 6: Wire** `Deps.Tray` in `main.go`; `go build ./... && go test ./...` — Expected: PASS.
- [ ] **Step 7: Manual check:** run `./prezilla tray` in the XFCE panel (requires the panel's "Status Notifier" plugin); click toggles presentation mode, `./prezilla off` from another terminal flips the tray icon within ~2 s, Quit exits. Also run it with the status notifier plugin removed and confirm exit code 1 and the hint.
- [ ] **Step 8: Commit** `feat: tray front-end and autostart entry`.

---

### Task 9: README

**Files:**
- Create: `README.md`

- [ ] **Step 1: Write README** covering: what prezilla does (the five switches and the 30-minute auto-disable, `--for`, `timeout_minutes`), CLI usage with exit codes, config file example (including `wallpaper`), XFCE prerequisites (`xfconf-query`, `pactl` from `pulseaudio-utils`, `wmctrl`, panel Status Notifier plugin), install (`make install`; from-source and release-package sections are completed by Task 10), and a roadmap line: GNOME, KDE, Windows, macOS backends planned.
- [ ] **Step 2: Commit** `docs: add README`.

---

### Task 10: Packaging, Makefile, changelog, CI

**Files:**
- Create: `Makefile`, `nfpm.yaml`, `CHANGELOG.md`, `LICENSE` (Apache-2.0, copy of `/home/dol/project/tunewise/LICENSE`), `.github/workflows/ci.yml`, `.github/workflows/release.yml`, `scripts/check-install.sh`; Modify: `README.md` (install section), `.gitignore` (`/dist/`)

**Interfaces:**
- Consumes: `packaging/prezilla.desktop` (Task 8), `packaging/icons/hicolor/**` (Task 7), `main.version` ldflag (Task 6).
- Produces `make` targets (exact names): `build` (→ `./prezilla`, version from `git describe --tags --always --dirty` passed as `-ldflags "-X main.version=$(VERSION)"`, override with `VERSION=`), `test`, `lint` (`gofmt -l` must print nothing and `go vet ./...`), `run`, `icons` (runs `scripts/render-icons.sh`), `install` (honours `PREFIX` default `/usr/local` and `DESTDIR`; installs `bin/prezilla`, `share/applications/prezilla.desktop`, the hicolor tree under `share/icons/hicolor/`), `uninstall`, `package` (builds `dist/prezilla_<version>_amd64.{deb,rpm,pkg.tar.zst}` with `nfpm package -p deb|rpm|archlinux` for linux/amd64), `ci` (`lint` + `test`), `clean`.
- `nfpm.yaml`: name `prezilla`, arch `amd64`, version from env `${VERSION}`, license `Apache-2.0`, homepage `https://github.com/dol/prezilla` (assumed from the sibling project's `github.com/dol/tunewise`; edit if the repo lives elsewhere), contents installing the same files as `make install` under `/usr` plus `packaging/prezilla.desktop` also to `/etc/xdg/autostart/prezilla.desktop`; deb `depends`/`recommends`: `recommends: wmctrl, pulseaudio-utils` (rpm: `wmctrl, pulseaudio-utils`; archlinux: `wmctrl, libpulse`).
- `scripts/check-install.sh <install-root>`: exits non-zero unless `<root>/usr*/bin/prezilla` exists and is executable, `.../share/applications/prezilla.desktop` exists and contains `Icon=prezilla`, and every hicolor size from Task 7 exists under `share/icons/hicolor/`.
- `release.yml`: on tag `v*.*.*`, builds linux/amd64 and linux/arm64 binaries (`CGO_ENABLED=0`) with the tag version, runs `make package`, creates a tarball, and uploads everything to the GitHub release; `ci.yml`: on push/PR runs `make ci` and `make build` with Go from `go.mod`.

- [ ] **Step 1: Write `scripts/check-install.sh`** (the acceptance check for the next steps) and run `bash scripts/check-install.sh /nonexistent` — Expected: exits non-zero.
- [ ] **Step 2: Write `Makefile`**; run `make lint test build && ./prezilla version` — Expected: all pass, version output is not `dev` and matches `git describe`.
- [ ] **Step 3: Verify install:** `make install PREFIX=/usr DESTDIR=$(mktemp -d)` then `bash scripts/check-install.sh <that dir>` — Expected: exit 0. Also `make uninstall PREFIX=/usr DESTDIR=<same dir>` leaves no prezilla files behind.
- [ ] **Step 4: Write `nfpm.yaml`**; run `make package VERSION=0.0.0-test` — Expected: `.deb`, `.rpm` and `.pkg.tar.zst` in `dist/`; `dpkg-deb -c dist/prezilla_0.0.0-test_amd64.deb` lists `usr/bin/prezilla`, `usr/share/applications/prezilla.desktop`, `etc/xdg/autostart/prezilla.desktop` and all hicolor PNGs; extract the deb into a temp dir (`dpkg-deb -x`) and run `bash scripts/check-install.sh` on it — Expected: exit 0; `<extracted>/usr/bin/prezilla version` prints `0.0.0-test`.
- [ ] **Step 5: Write `CHANGELOG.md`** (Keep a Changelog format as in the sibling project, `[Unreleased]` listing the CLI, tray, five XFCE switches, auto-disable timeout, icons, packaging), `LICENSE`, `.github/workflows/ci.yml` and `release.yml`; validate both workflow files parse as YAML (`python3 -c "import yaml,sys;[yaml.safe_load(open(f)) for f in sys.argv[1:]]" .github/workflows/*.yml`).
- [ ] **Step 6: Update `README.md` install section** (release packages table: deb / rpm / pkg.tar.zst / tarball; from source: `make install`; autostart note) and add `/dist/` to `.gitignore` if not present.
- [ ] **Step 7: Commit** `feat: packaging, makefile, changelog and ci`.

---

## Self-Review

- **Spec coverage:** five TogglePresent switches → Tasks 4–5; tray → Task 8 (icons: Task 7); CLI → Task 6; packaging/Makefile/CI/changelog → Task 10; XFCE first → all backends; multi-platform → `core.Switch` + `platform.Detect/Switches` seam (other desktops explicitly deferred to separate plans).
- **Types:** `Switch.Enable` returns `(string, error)` and `Disable(ctx, saved string)` in Tasks 2, 3, 4, 5 consistently; `xfce.Switches(r, cfg)` and `platform.Switches(desktop, r, cfg)` match in Task 6; `Deps.Tray` signature matches `tray.Run` wrapped in main.
- **Review Focus:** items 1–2 → Task 3 tests; 3 → Task 4; 4 → Task 5; 5 → Task 2; 6 → Task 6; 7 → Task 8; 8–10 → Tasks 3 and 6; 11–12 → Task 10.
- **Risk to verify during execution:** exact `wmctrl -m` wording (`Showing the desktop mode`) and `xfconf-query` unset-property behavior on this machine; adjust fake outputs to real ones in Task 4/5 if they differ.

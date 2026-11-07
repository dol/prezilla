// Package tray is the StatusNotifierItem front-end for prezilla.
package tray

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	"fyne.io/systray"
	"github.com/godbus/dbus/v5"

	"prezilla/internal/about"
	"prezilla/internal/core"
	"prezilla/internal/system"
	"prezilla/internal/tray/icons"
	"prezilla/internal/usererr"
)

const (
	pollEvery   = 2 * time.Second
	watcherName = "org.kde.StatusNotifierWatcher"
)

var errNoHost = errors.New(`no system tray (StatusNotifier host) found; use "prezilla toggle" from the command line`)

// Labels returns the tooltip's first words for the given state.
func Labels(active bool) string {
	if active {
		return "Prezilla: presentation mode on"
	}
	return "Prezilla: presentation mode off"
}

// Icon returns the embedded PNG for the given state.
func Icon(active bool) []byte {
	if active {
		return icons.Active
	}
	return icons.Idle
}

// remainingText formats the time left until expires, rounded up to whole
// minutes, with a non-breaking space so "12 min" never wraps; it is empty
// when there is no deadline or it has passed.
func remainingText(now, expires time.Time) string {
	if expires.IsZero() || !expires.After(now) {
		return ""
	}
	mins := int((expires.Sub(now) + time.Minute - 1) / time.Minute)
	return fmt.Sprintf("%d\u00a0min left", mins)
}

// tooltip composes the tooltip: the state label, the remaining time when
// there is one, and the last toggle and expiry error texts when non-empty
// (both are shown, joined with "; "), plus a second line while a screensaver
// prompt is pending.
func tooltip(active bool, remaining, toggleErr, expiryErr string, prompting bool) string {
	tip := Labels(active)
	if remaining != "" {
		tip += ", " + remaining
	}
	if joined := usererr.JoinSentences(toggleErr, expiryErr); joined != "" {
		tip += " (error: " + joined + ")"
	}
	if prompting {
		tip += "\n" + promptLine
	}
	return tip
}

// The checkbox's title and hover text never change; its checkmark shows
// the state.
const (
	modeTitle   = "Presentation mode"
	modeTooltip = "Turn presentation mode on or off"
)

// itemState is what one menu item should show.
type itemState struct {
	Title   string
	Tooltip string
	Visible bool
	Enabled bool
	Checked bool
}

// menuModel is the state of the menu items that change at runtime, named
// after their role. About and Quit never change.
type menuModel struct {
	Keep, Stop, Mode, Timer, Reset itemState
	// Quit is not part of ordered: it never hides and is always last.
	Quit itemState
}

// ordered returns the items in menu order.
func (m menuModel) ordered() []itemState {
	return []itemState{m.Keep, m.Stop, m.Mode, m.Timer, m.Reset}
}

// menuState decides the changing menu items. timer is timerLabel's text and
// is empty without a live deadline; remaining is the time left until that
// deadline; pending means a screensaver question is open. Keep presenting
// and Reset timer both set the end time to step from now (Engine.Extend
// never adds and never shortens), so Reset is shown only when that would
// change something (at least a minute less than step left, so right after
// turning on, while the label still reads "N min left", there is no no-op
// item) and not while the question is open, where Keep presenting answers it.
func menuState(active bool, timer string, remaining time.Duration, pending bool, step time.Duration) menuModel {
	hasDeadline := timer != ""
	mins := stepMinutes(step)
	return menuModel{
		Keep: itemState{
			Title:   keepTitle(step),
			Tooltip: fmt.Sprintf("Keeps presentation mode on until %d minutes from now.", mins),
			Visible: pending, Enabled: true,
		},
		Stop: itemState{
			Title: stopTitle, Tooltip: "Turns presentation mode off now.",
			Visible: pending, Enabled: true,
		},
		Mode: itemState{Title: modeTitle, Tooltip: modeTooltip, Visible: true, Enabled: true, Checked: active},
		Timer: itemState{
			Title: timer, Tooltip: "Presentation mode turns off by itself at this time.",
			Visible: hasDeadline,
		},
		Reset: itemState{
			Title:   fmt.Sprintf("Reset timer to %d min", mins),
			Tooltip: fmt.Sprintf("Sets the end time to %d minutes from now.", mins),
			Visible: hasDeadline && remaining <= step-time.Minute && !pending, Enabled: true,
		},
		Quit: itemState{Title: quitTitle(active), Tooltip: quitTooltip, Visible: true, Enabled: true},
	}
}

// tickModel decides the six switch tick boxes, in tickItems order: each is
// checked as in ticks. A config error (err != nil) disables them all, since
// a change would have to write the unreadable file.
func tickModel(ticks map[string]bool, err error) []itemState {
	items := make([]itemState, len(tickItems))
	for i, t := range tickItems {
		items[i] = itemState{
			Title: t.Title, Tooltip: t.Tooltip,
			Visible: true, Enabled: err == nil, Checked: ticks[t.ID],
		}
	}
	return items
}

// quitTooltip is Quit's hover text; the title carries the consequence too,
// because the library does not export hover text on Linux.
const quitTooltip = "Closes the tray icon; presentation mode stays as it is."

// quitTitle says in the title itself that Quit leaves the mode as it is.
func quitTitle(active bool) string {
	if active {
		return "Quit (presentation stays on)"
	}
	return "Quit"
}

// startExtend runs one Engine.Extend call for the Reset timer item on xt, detached from ctx's cancellation
// and bounded by core.OperationTimeout, unless one is already running. A
// (false, nil) result (the session ended meanwhile) changes nothing; an
// error is recorded in a for the tooltip and printed to stderr. done runs
// afterwards either way. It reports whether the call was started.
func startExtend(ctx context.Context, xt *toggler, a *actionState,
	extend func(context.Context, time.Duration) (bool, error), step time.Duration, done func()) bool {
	return xt.start(ctx, func(xctx context.Context) {
		a.run(xctx, func(c context.Context) error {
			_, err := extend(c, step)
			return err
		}, done)
	})
}

// toggler runs at most one toggle at a time, detached from cancellation.
// timeout bounds each run; zero means core.OperationTimeout.
type toggler struct {
	busy    sync.Mutex
	timeout time.Duration
}

// start runs fn in a goroutine unless a toggle is already running. fn gets a
// context that is not cancelled when parent is, so Quit or a signal cannot
// abort a half-applied toggle. It reports whether fn was started.
func (t *toggler) start(parent context.Context, fn func(ctx context.Context)) bool {
	if !t.busy.TryLock() {
		return false
	}
	limit := t.timeout
	if limit <= 0 {
		limit = core.OperationTimeout
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), limit)
	go func() {
		defer t.busy.Unlock()
		defer cancel()
		fn(ctx)
	}()
	return true
}

// wait blocks until any running toggle has finished.
func (t *toggler) wait() {
	t.busy.Lock()
	t.busy.Unlock() //nolint:staticcheck // empty critical section is the point
}

// hostAvailable reports whether a StatusNotifier watcher owns its bus name.
// fyne.io/systray only logs registration failures, so check up front.
func hostAvailable() bool {
	conn, err := dbus.SessionBus()
	if err != nil {
		return false
	}
	var ok bool
	err = conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, watcherName).Store(&ok)
	return err == nil && ok
}

// spawnIfDeadline calls spawn when status reports the mode active with a
// deadline, so a tray-started session is enforced after the tray quits. A
// status error spawns nothing (refresh reports it); spawn errors are returned.
func spawnIfDeadline(status func() (core.State, error), spawn func() error) error {
	if spawn == nil {
		return nil
	}
	st, err := status()
	if err != nil || !st.Active || st.ExpiresAt.IsZero() {
		return nil
	}
	if err := spawn(); err != nil {
		return fmt.Errorf("start watcher: %w", err)
	}
	return nil
}

// actionTimeout bounds one About notification.
const actionTimeout = 10 * time.Second

// Config holds what the tray needs.
type Config struct {
	Engine  *core.Engine
	Timeout time.Duration
	// Spawn starts the detached deadline watcher after a toggle turns the mode on.
	Spawn    func() error
	About    about.Info
	Notifier system.Notifier
	// AboutWindow shows About Prezilla (with the clickable project link);
	// nil or a failure falls back to the About notification.
	AboutWindow system.AboutWindow
	// ScreenSaver reports screensaver activation; nil disables the
	// "continue presenting?" prompt entirely.
	ScreenSaver system.ScreenSaver
	// Prompter shows that prompt as a notification; nil leaves only the
	// fallback menu items.
	Prompter system.Prompter
	// ConfigPath is config.json, read for the switch tick boxes and written
	// when one changes.
	ConfigPath string
	// Chooser picks the wallpaper image for Choose wallpaper…; nil behaves
	// like a missing zenity.
	Chooser system.FileChooser
	// StartDir is where the chooser opens (the user's home directory).
	StartDir string
	// SwitchFor builds one switch; image is the wallpaper path.
	SwitchFor func(id, image string) (core.Switch, error)
	// SwitchesFor builds the configured switches; the tray reloads
	// config.json and uses it before turning the mode on. nil keeps the
	// engine's list.
	SwitchesFor func(core.Config) ([]core.Switch, error)
}

// chooseTimeout bounds one Choose wallpaper… run: the dialog stays open as
// long as its runner allows (30 minutes), then the engine call follows.
const chooseTimeout = 30*time.Minute + core.OperationTimeout

// quitChooser closes the dialog of inner when quit ends (Quit or a signal),
// while the rest of Choose wallpaper… keeps its detached context; a dialog
// closed that way counts as cancelled. A nil inner reports a missing zenity.
type quitChooser struct {
	quit  context.Context
	inner system.FileChooser
}

func (q quitChooser) ChooseImage(ctx context.Context, startDir string) (string, error) {
	if q.inner == nil {
		return "", fmt.Errorf("%w: no file chooser configured", system.ErrNoZenity)
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer context.AfterFunc(q.quit, cancel)()
	path, err := q.inner.ChooseImage(cctx, startDir)
	if err != nil && q.quit.Err() != nil {
		return "", system.ErrCancelled
	}
	return path, err
}

// newSettings builds the tick box and wallpaper logic from cfg; quit closes
// an open chooser. The missing-zenity message goes out as a notification
// when there is a notifier, otherwise it is an error.
func newSettings(quit context.Context, cfg Config) *settings {
	s := &settings{
		ConfigPath: cfg.ConfigPath,
		Engine:     cfg.Engine,
		SwitchFor:  cfg.SwitchFor,
		Chooser:    quitChooser{quit: quit, inner: cfg.Chooser},
		StartDir:   cfg.StartDir,
	}
	if s.SwitchFor == nil {
		s.SwitchFor = func(id, _ string) (core.Switch, error) {
			return nil, fmt.Errorf("switch %s: no switch builder configured", id)
		}
	}
	if n := cfg.Notifier; n != nil {
		s.Notify = func(ctx context.Context, summary, body string) error {
			nctx, cancel := context.WithTimeout(ctx, actionTimeout)
			defer cancel()
			return n.Notify(nctx, summary, body, about.IconName)
		}
	}
	return s
}

// reloadSwitches reads config.json again and hands the switches it
// configures to e, so tick changes and hand edits made since the start take
// effect on the next "on". A load or build error keeps the current list
// (refresh reports a config error in the tooltip).
func reloadSwitches(e *core.Engine, path string, build func(core.Config) ([]core.Switch, error)) {
	if path == "" || build == nil {
		return
	}
	loaded, err := core.LoadConfig(path)
	if err != nil {
		return
	}
	sw, err := build(loaded)
	if err != nil {
		return
	}
	e.SetSwitches(sw)
}

// aboutMessage composes the About notification.
func aboutMessage(info about.Info) (summary, body string) {
	return "Prezilla " + info.Version, info.Comments + "\nLicense: " + info.License + "\n" + info.Website
}

func aboutAction(n system.Notifier, info about.Info) func(context.Context) error {
	return func(ctx context.Context) error {
		if n == nil {
			return errors.New("about: no notifier configured")
		}
		summary, body := aboutMessage(info)
		return n.Notify(ctx, summary, body, info.IconName)
	}
}

// aboutShower runs About Prezilla: the window when there is one, otherwise
// (or when it fails) the notification. At most one runs at a time.
type aboutShower struct {
	window   system.AboutWindow
	notifier system.Notifier
	info     about.Info
	stderr   io.Writer // nil means os.Stderr

	busy     sync.Mutex
	warnOnce sync.Once
}

func (s *aboutShower) logf(format string, args ...any) {
	w := s.stderr
	if w == nil {
		w = os.Stderr
	}
	fmt.Fprintf(w, format+"\n", args...)
}

// show opens the window and waits for it. A cancelled ctx (Quit) returns
// nil without a notification; a missing window tool, a missing display or another
// window failure falls back to the notification, whose error is returned if it fails too.
func (s *aboutShower) show(ctx context.Context) error {
	if s.window != nil {
		err := s.window.Show(ctx, s.info)
		switch {
		case err == nil:
			return nil
		case ctx.Err() != nil:
			return nil
		case errors.Is(err, system.ErrNoZenity):
			s.warnOnce.Do(func() { s.logf("about: zenity not found; showing a notification instead") })
		case errors.Is(err, system.ErrNoDisplay):
			s.warnOnce.Do(func() { s.logf("about: no X display; showing a notification instead") })
		default:
			s.logf("%v; showing a notification instead", err)
		}
	}
	nctx, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	return aboutAction(s.notifier, s.info)(nctx)
}

// start runs show in a goroutine, recording its result in a and calling
// done, unless an About window is already open. Unlike toggler it keeps
// ctx's cancellation, so Quit closes the window. It reports whether it
// started.
func (s *aboutShower) start(ctx context.Context, a *actionState, done func()) bool {
	if !s.busy.TryLock() {
		return false
	}
	go func() {
		defer s.busy.Unlock()
		a.run(ctx, s.show, done)
	}()
	return true
}

// wait blocks until any open About window has closed.
func (s *aboutShower) wait() {
	s.busy.Lock()
	s.busy.Unlock() //nolint:staticcheck // empty critical section is the point
}

// expiryNote is what one refresh does with the expiry error.
type expiryNote struct {
	explained string // stderr text; the de-duplication key
	short     string // tooltip text
	print     bool   // true when explained is new and non-empty
}

// noteExpiry converts the expiry error and decides whether it still has to be
// printed: the same explained text is printed only once until it changes.
func noteExpiry(prev string, err error) expiryNote {
	n := expiryNote{explained: usererr.Explain(err), short: usererr.Short(err)}
	n.print = n.explained != "" && n.explained != prev
	return n
}

// statusTooltip is the tooltip when the state cannot be read: the sentence
// with the "Prezilla: " prefix, unless it already starts with the name.
func statusTooltip(sentence string) string {
	if strings.HasPrefix(sentence, "Prezilla") {
		return sentence
	}
	return "Prezilla: " + sentence
}

// joinErrText joins two error texts (see usererr.JoinSentences).
func joinErrText(a, b string) string { return usererr.JoinSentences(a, b) }

// actionState holds the last About error for the tooltip.
type actionState struct {
	mu  sync.Mutex
	err string
}

// clear forgets the last error text.
func (a *actionState) clear() {
	a.mu.Lock()
	a.err = ""
	a.mu.Unlock()
}

// sessionEnded reports whether the session went inactive or lost its
// deadline since the previous refresh; the error texts of the About, Reset
// and prompt actions are then stale.
func sessionEnded(wasActive, wasDeadline, active, deadline bool) bool {
	return wasActive && !active || wasDeadline && !deadline
}

// clearActionErrors forgets the error texts of the About, Reset and prompt
// actions once their session is over.
func clearActionErrors(actions, extend *actionState, coord *promptCoordinator) {
	actions.clear()
	extend.clear()
	coord.clearErr()
}

func (a *actionState) text() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.err
}

// run executes fn, records its error (cleared on success), prints it to
// stderr and calls done.
func (a *actionState) run(ctx context.Context, fn func(context.Context) error, done func()) {
	err := fn(ctx)
	a.mu.Lock()
	a.err = ""
	if err != nil {
		a.err = usererr.Short(err)
	}
	a.mu.Unlock()
	if err != nil {
		fmt.Fprintln(os.Stderr, usererr.Explain(err))
	}
	done()
}

// Run shows the tray icon and blocks until Quit is chosen or ctx is cancelled.
func Run(ctx context.Context, cfg Config) error {
	if !hostAvailable() {
		return errNoHost
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// toggle, Reset timer, tick change and Choose wallpaper…: at most one of
	// each at a time. The chooser has its own, so ticks stay usable while
	// its dialog is open.
	var tg, xt, kt toggler
	ct := toggler{timeout: chooseTimeout}
	shower := &aboutShower{window: cfg.AboutWindow, notifier: cfg.Notifier, info: cfg.About}
	// redraw asks the menu loop to refresh; coalesced, never blocks.
	redraw := make(chan struct{}, 1)
	// toggleReq carries left clicks on the icon and checkbox clicks to the
	// one toggle handler; coalesced, never blocks the D-Bus goroutine.
	toggleReq := make(chan struct{}, 1)
	e := cfg.Engine
	step := continueStep(cfg.Timeout)
	coord := newPromptCoordinator(promptDeps{
		prompter: cfg.Prompter,
		info:     cfg.About,
		step:     step,
		status:   e.Status,
		extend:   func(ctx context.Context) (bool, error) { return e.Extend(ctx, step) },
		disable:  e.Disable,
		now:      time.Now,
		changed: func() {
			select {
			case redraw <- struct{}{}:
			default:
			}
		},
	})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		if err := coord.watch(ctx, cfg.ScreenSaver); err != nil && ctx.Err() == nil {
			fmt.Fprintln(os.Stderr, usererr.Explain(err)) // once; the tray carries on without the prompt
		}
	}()
	// Set before systray.Run: the library reads it when it exports the item
	// (ItemIsMenu), and the host then sends left clicks as Activate. A host
	// that never calls Activate still opens the menu.
	systray.SetOnTapped(func() { requestToggle(toggleReq) })
	systray.Run(func() { onReady(ctx, cancel, cfg, &tg, &xt, &kt, &ct, shower, coord, redraw, toggleReq) }, cancel)
	cancel()
	<-watchDone
	shower.wait()
	coord.wait()
	tg.wait()
	xt.wait()
	kt.wait()
	ct.wait()
	return nil
}

// requestToggle queues one toggle request without blocking; a request that
// arrives while one is queued is merged into it. A request that arrives while
// a toggle is running is dropped by toggler.start, and that toggle's final
// refresh puts the real state back.
func requestToggle(req chan<- struct{}) {
	select {
	case req <- struct{}{}:
	default:
	}
}

func onReady(ctx context.Context, cancel context.CancelFunc, cfg Config, tg, xt, kt, ct *toggler,
	shower *aboutShower, coord *promptCoordinator, redraw <-chan struct{}, toggleReq chan struct{}) {
	e, timeout, spawn := cfg.Engine, cfg.Timeout, cfg.Spawn
	step := continueStep(timeout)
	systray.SetIcon(Icon(false))
	systray.SetTitle("Prezilla")

	// Items cannot be reordered after creation: create them in menu order,
	// hide the conditional ones, and let refresh show them.
	m := menuState(false, "", 0, false, step)
	keepItem := systray.AddMenuItem(m.Keep.Title, m.Keep.Tooltip)
	keepItem.Hide()
	stopItem := systray.AddMenuItem(m.Stop.Title, m.Stop.Tooltip)
	stopItem.Hide()
	modeItem := systray.AddMenuItemCheckbox(modeTitle, modeTooltip, false)
	timerItem := systray.AddMenuItem("", m.Timer.Tooltip)
	timerItem.Disable()
	timerItem.Hide()
	resetItem := systray.AddMenuItem(m.Reset.Title, m.Reset.Tooltip)
	resetItem.Hide()
	systray.AddSeparator()
	tickMenu := make([]*systray.MenuItem, len(tickItems))
	for i, it := range tickModel(nil, nil) {
		tickMenu[i] = systray.AddMenuItemCheckbox(it.Title, it.Tooltip, false)
	}
	chooseItem := systray.AddMenuItem(chooseWallpaperTitle, "Opens a file chooser to pick the wallpaper image.")
	systray.AddSeparator()
	aboutItem := systray.AddMenuItem("About Prezilla", "Shows the version, license and website.")
	systray.AddSeparator()
	quit := systray.AddMenuItem(m.Quit.Title, m.Quit.Tooltip)

	set := newSettings(ctx, cfg)
	var mu sync.Mutex // guards lastErr, lastExpiryErr and the visibility flags
	var lastErr error
	var actions, extendErr, tickErr, chooseErr actionState
	var lastExpiryErr, lastStatusErr, lastConfigErr string
	ticksEnabled := true // guarded by mu
	var timerShown, resetShown, promptShown bool
	var wasActive, wasDeadline bool // previous refresh, guarded by mu
	quitShown := m.Quit.Title       // guarded by mu

	refresh := func() {
		opCtx, opCancel := context.WithTimeout(ctx, core.OperationTimeout)
		_, expErr := e.DisableIfExpired(opCtx)
		opCancel()
		st, err := e.Status()
		mu.Lock()
		defer mu.Unlock()
		note := noteExpiry(lastExpiryErr, expErr)
		lastExpiryErr = note.explained
		expText := note.short
		if note.print {
			fmt.Fprintln(os.Stderr, note.explained)
		}
		stNote := noteExpiry(lastStatusErr, err)
		lastStatusErr = stNote.explained
		if err != nil {
			if stNote.print {
				fmt.Fprintln(os.Stderr, stNote.explained)
			}
			systray.SetTooltip(statusTooltip(stNote.short))
			return
		}
		now := time.Now()
		hasDeadline := st.Active && !st.ExpiresAt.IsZero()
		if sessionEnded(wasActive, wasDeadline, st.Active, hasDeadline) {
			clearActionErrors(&actions, &extendErr, coord)
			tickErr.clear()
			chooseErr.clear()
		}
		wasActive, wasDeadline = st.Active, hasDeadline
		coord.check(st, now)
		prompting := coord.pending()
		var toggleText string
		if lastErr != nil {
			toggleText = usererr.Short(lastErr)
		}
		remaining, timer := "", ""
		if st.Active {
			remaining = remainingText(now, st.ExpiresAt)
			timer = timerLabel(now, st.ExpiresAt.Local())
		}
		// Ticks come from the saved state while active, from config.json
		// otherwise; a config error disables them and is told once.
		ticks, cfgErr := set.Ticks()
		cfgNote := noteExpiry(lastConfigErr, cfgErr)
		lastConfigErr = cfgNote.explained
		if cfgNote.print {
			fmt.Fprintln(os.Stderr, cfgNote.explained)
		}
		errText := joinErrText(joinErrText(toggleText, actions.text()), joinErrText(extendErr.text(), coord.errText()))
		errText = joinErrText(errText, settingsErrText(tickErr.text(), chooseErr.text(), cfgNote.short))
		tip := tooltip(st.Active, remaining, errText, expText, prompting)
		var left time.Duration
		if timer != "" {
			left = st.ExpiresAt.Sub(now)
		}
		m := menuState(st.Active, timer, left, prompting, step)
		if m.Timer.Visible {
			timerItem.SetTitle(m.Timer.Title)
		}
		if m.Timer.Visible != timerShown {
			timerShown = m.Timer.Visible
			setVisible(timerItem, timerShown)
		}
		if m.Quit.Title != quitShown {
			quitShown = m.Quit.Title
			quit.SetTitle(quitShown)
		}
		if m.Reset.Visible != resetShown {
			resetShown = m.Reset.Visible
			if !resetShown {
				extendErr.clear()
			}
			setVisible(resetItem, resetShown)
		}
		if m.Keep.Visible != promptShown {
			promptShown = m.Keep.Visible
			setVisible(keepItem, promptShown)
			setVisible(stopItem, m.Stop.Visible)
		}
		// The engine state is the only source of the checkmark: set it on
		// every refresh, so a failed toggle, a host that ticked the box
		// itself or a change from the CLI never leaves it wrong.
		if m.Mode.Checked {
			modeItem.Check()
		} else {
			modeItem.Uncheck()
		}
		// Same for the tick boxes: the real state on every refresh.
		ticksModel := tickModel(ticks, cfgErr)
		for i, it := range ticksModel {
			if it.Checked {
				tickMenu[i].Check()
			} else {
				tickMenu[i].Uncheck()
			}
		}
		if enabled := cfgErr == nil; enabled != ticksEnabled {
			ticksEnabled = enabled
			for _, item := range tickMenu {
				setEnabled(item, enabled)
			}
		}
		systray.SetIcon(Icon(st.Active))
		systray.SetTooltip(tip)
	}
	refresh()

	// toggle is the one handler for the checkbox and a click on the icon.
	// A click while a toggle runs is dropped.
	toggle := func() {
		tg.start(ctx, func(tctx context.Context) {
			if st, err := e.Status(); err == nil && !st.Active {
				reloadSwitches(e, cfg.ConfigPath, cfg.SwitchesFor)
			}
			_, err := e.Toggle(tctx, timeout)
			if serr := spawnIfDeadline(e.Status, spawn); serr != nil {
				err = errors.Join(err, serr)
			}
			mu.Lock()
			lastErr = err
			mu.Unlock()
			if err != nil {
				fmt.Fprintln(os.Stderr, usererr.Explain(err))
			}
			refresh()
		})
	}

	// tickReq carries tick box clicks (the switch ID) to the menu loop; one
	// forwarding goroutine per item, never blocking the D-Bus goroutine.
	tickReq := make(chan string, len(tickItems))
	for i, item := range tickMenu {
		go forwardClicks(ctx, item.ClickedCh, tickItems[i].ID, tickReq)
	}
	// setTick flips switch id from its real tick, read when the change
	// starts (not the item's own checkmark). A click while a tick change
	// runs is dropped; the next refresh puts the real state back.
	setTick := func(id string) {
		kt.start(ctx, func(kctx context.Context) {
			tickErr.run(kctx, func(c context.Context) error {
				ticks, err := set.Ticks()
				if err != nil {
					return err
				}
				return set.SetTick(c, id, !ticks[id])
			}, refresh)
		})
	}

	// choose runs Choose wallpaper… on its own toggler, whose bound allows
	// for the open dialog; a click while the dialog is open is ignored.
	choose := func() {
		ct.start(ctx, func(cctx context.Context) {
			chooseErr.run(cctx, set.ChooseWallpaper, refresh)
		})
	}

	go func() {
		t := time.NewTicker(pollEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				systray.Quit()
				return
			case <-t.C:
				refresh()
			case <-redraw:
				refresh()
			case <-keepItem.ClickedCh:
				coord.menuAnswer(ctx, keyContinue)
			case <-stopItem.ClickedCh:
				coord.menuAnswer(ctx, keyEnd)
			case <-modeItem.ClickedCh:
				requestToggle(toggleReq)
			case <-toggleReq:
				toggle()
			case <-resetItem.ClickedCh:
				startExtend(ctx, xt, &extendErr, e.Extend, step, refresh)
			case id := <-tickReq:
				if tickOpensChooser(cfg.ConfigPath, set, id) {
					choose()
				} else {
					setTick(id)
				}
			case <-chooseItem.ClickedCh:
				choose()
			case <-aboutItem.ClickedCh:
				shower.start(ctx, &actions, refresh) // ignored while the window is open
			case <-quit.ClickedCh:
				cancel()
			}
		}
	}()
}

// settingsErrText joins the tick, chooser and config error texts for the
// tooltip, leaving out a tick or chooser text that repeats an earlier one
// (a click on a broken config reports the config sentence again).
func settingsErrText(tick, choose, config string) string {
	if tick == config {
		tick = ""
	}
	if choose == config || choose == tick {
		choose = ""
	}
	return joinErrText(joinErrText(tick, choose), config)
}

// opensChooser reports whether a click on tick box id opens the image
// chooser: ticking Change wallpaper while no image is configured.
func opensChooser(id string, ticks map[string]bool, image string) bool {
	return id == core.IDWallpaper && !ticks[id] && image == ""
}

// tickOpensChooser applies opensChooser to the current ticks and config, so
// that click runs on the chooser's toggler instead of the tick toggler
// (SetTick would open the dialog under the short tick bound). A read error
// returns false, and SetTick reports it.
func tickOpensChooser(path string, s *settings, id string) bool {
	if id != core.IDWallpaper {
		return false
	}
	ticks, err := s.Ticks()
	if err != nil {
		return false
	}
	c, err := core.LoadConfig(path)
	return err == nil && opensChooser(id, ticks, c.Wallpaper)
}

// forwardClicks sends id to req for every click on clicked until ctx ends;
// a click while req is full is dropped.
func forwardClicks(ctx context.Context, clicked <-chan struct{}, id string, req chan<- string) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-clicked:
			select {
			case req <- id:
			default:
			}
		}
	}
}

func setEnabled(item *systray.MenuItem, on bool) {
	if on {
		item.Enable()
	} else {
		item.Disable()
	}
}

func setVisible(item *systray.MenuItem, show bool) {
	if show {
		item.Show()
	} else {
		item.Hide()
	}
}

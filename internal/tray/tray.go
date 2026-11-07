// Package tray is the StatusNotifierItem front-end for prezilla.
package tray

import (
	"context"
	"errors"
	"fmt"
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
)

const (
	pollEvery   = 2 * time.Second
	watcherName = "org.kde.StatusNotifierWatcher"
)

var errNoHost = errors.New(`no system tray (StatusNotifier host) found; use "prezilla toggle" from the command line`)

// Labels returns the tooltip's first words for the given state.
func Labels(active bool) string {
	if active {
		return "prezilla: presentation mode on"
	}
	return "prezilla: presentation mode off"
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
	var errs []string
	for _, e := range []string{toggleErr, expiryErr} {
		if e != "" {
			errs = append(errs, e)
		}
	}
	if len(errs) > 0 {
		tip += " (error: " + strings.Join(errs, "; ") + ")"
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
// change something (less than step left) and not while the question is
// open, where Keep presenting answers it.
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
			Visible: hasDeadline && remaining < step && !pending, Enabled: true,
		},
	}
}

// toggleCooldown is how long after an accepted toggle further toggle clicks
// (icon or checkbox) are ignored, so a double click cannot flip the mode
// on and straight back off.
const toggleCooldown = 500 * time.Millisecond

// clickGate drops toggle clicks within toggleCooldown of the last accepted
// toggle.
type clickGate struct {
	now func() time.Time

	mu   sync.Mutex
	last time.Time // start of the last accepted toggle; zero before the first
}

// open reports whether the cooldown has passed.
func (g *clickGate) open() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.last.IsZero() || g.now().Sub(g.last) >= toggleCooldown
}

// tryToggle starts fn on tg unless the cooldown is running or a toggle is
// already running; an accepted toggle starts the cooldown whatever its
// result. It reports whether fn was started.
func tryToggle(ctx context.Context, g *clickGate, tg *toggler, fn func(context.Context)) bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.now()
	if !g.last.IsZero() && now.Sub(g.last) < toggleCooldown {
		return false
	}
	if !tg.start(ctx, fn) {
		return false
	}
	g.last = now
	return true
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
type toggler struct{ busy sync.Mutex }

// start runs fn in a goroutine unless a toggle is already running. fn gets a
// context that is not cancelled when parent is, so Quit or a signal cannot
// abort a half-applied toggle. It reports whether fn was started.
func (t *toggler) start(parent context.Context, fn func(ctx context.Context)) bool {
	if !t.busy.TryLock() {
		return false
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(parent), core.OperationTimeout)
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

// actionTimeout bounds one About action.
const actionTimeout = 10 * time.Second

// Config holds what the tray needs.
type Config struct {
	Engine  *core.Engine
	Timeout time.Duration
	// Spawn starts the detached deadline watcher after a toggle turns the mode on.
	Spawn    func() error
	About    about.Info
	Notifier system.Notifier
	// Opener opens URLs; the tray menu does not use it (the About window
	// will). nil is harmless.
	Opener system.Opener
	// ScreenSaver reports screensaver activation; nil disables the
	// "continue presenting?" prompt entirely.
	ScreenSaver system.ScreenSaver
	// Prompter shows that prompt as a notification; nil leaves only the
	// fallback menu items.
	Prompter system.Prompter
}

// aboutMessage composes the About notification.
func aboutMessage(info about.Info) (summary, body string) {
	return "prezilla " + info.Version, info.Comments + "\nLicense: " + info.License + "\n" + info.Website
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

// joinErrText joins two non-empty error texts with "; ".
func joinErrText(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	}
	return a + "; " + b
}

// actionState holds the last About error for the tooltip.
type actionState struct {
	mu  sync.Mutex
	err string
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
		a.err = err.Error()
	}
	a.mu.Unlock()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
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

	var tg, xt toggler // toggle and Reset timer: at most one of each at a time
	gate := &clickGate{now: time.Now}
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
			fmt.Fprintln(os.Stderr, err) // once; the tray carries on without the prompt
		}
	}()
	// Set before systray.Run: the library reads it when it exports the item
	// (ItemIsMenu), and the host then sends left clicks as Activate. A host
	// that never calls Activate still opens the menu.
	systray.SetOnTapped(func() { requestToggle(gate, toggleReq) })
	systray.Run(func() { onReady(ctx, cancel, cfg, &tg, &xt, gate, coord, redraw, toggleReq) }, cancel)
	cancel()
	<-watchDone
	coord.wait()
	tg.wait()
	xt.wait()
	return nil
}

// requestToggle queues one toggle request without blocking. A click during
// the cooldown is dropped, not queued; a request that arrives while one is
// queued is merged into it.
func requestToggle(g *clickGate, req chan<- struct{}) {
	if !g.open() {
		return
	}
	select {
	case req <- struct{}{}:
	default:
	}
}

func onReady(ctx context.Context, cancel context.CancelFunc, cfg Config, tg, xt *toggler, gate *clickGate,
	coord *promptCoordinator, redraw <-chan struct{}, toggleReq chan struct{}) {
	e, timeout, spawn := cfg.Engine, cfg.Timeout, cfg.Spawn
	step := continueStep(timeout)
	systray.SetIcon(Icon(false))
	systray.SetTitle("prezilla")

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
	aboutItem := systray.AddMenuItem("About prezilla", "Shows the version, license and website.")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "Closes the tray icon; presentation mode stays as it is.")

	var mu sync.Mutex // guards lastErr, lastExpiryErr and the visibility flags
	var lastErr error
	var actions, extendErr actionState
	var lastExpiryErr string
	var timerShown, resetShown, promptShown bool

	refresh := func() {
		opCtx, opCancel := context.WithTimeout(ctx, core.OperationTimeout)
		_, expErr := e.DisableIfExpired(opCtx)
		opCancel()
		st, err := e.Status()
		mu.Lock()
		defer mu.Unlock()
		expText := ""
		if expErr != nil {
			expText = expErr.Error()
		}
		if expText != lastExpiryErr {
			lastExpiryErr = expText
			if expText != "" {
				fmt.Fprintln(os.Stderr, expErr)
			}
		}
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			systray.SetTooltip("prezilla: " + err.Error())
			return
		}
		now := time.Now()
		coord.check(st, now)
		prompting := coord.pending()
		var toggleText string
		if lastErr != nil {
			toggleText = lastErr.Error()
		}
		remaining, timer := "", ""
		if st.Active {
			remaining = remainingText(now, st.ExpiresAt)
			timer = timerLabel(now, st.ExpiresAt.Local())
		}
		errText := joinErrText(joinErrText(toggleText, actions.text()), joinErrText(extendErr.text(), coord.errText()))
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
		if m.Reset.Visible != resetShown {
			resetShown = m.Reset.Visible
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
		systray.SetIcon(Icon(st.Active))
		systray.SetTooltip(tip)
	}
	refresh()

	// toggle is the one handler for the checkbox and the icon's left click.
	// The cooldown applies here and in requestToggle, never to Reset timer
	// or the question items.
	toggle := func() {
		tryToggle(ctx, gate, tg, func(tctx context.Context) {
			_, err := e.Toggle(tctx, timeout)
			if serr := spawnIfDeadline(e.Status, spawn); serr != nil {
				err = errors.Join(err, serr)
			}
			mu.Lock()
			lastErr = err
			mu.Unlock()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
			}
			refresh()
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
				requestToggle(gate, toggleReq)
			case <-toggleReq:
				toggle()
			case <-resetItem.ClickedCh:
				startExtend(ctx, xt, &extendErr, e.Extend, step, refresh)
			case <-aboutItem.ClickedCh:
				go runAction(ctx, &actions, aboutAction(cfg.Notifier, cfg.About), refresh)
			case <-quit.ClickedCh:
				cancel()
			}
		}
	}()
}

func setVisible(item *systray.MenuItem, show bool) {
	if show {
		item.Show()
	} else {
		item.Hide()
	}
}

// runAction runs fn bounded by actionTimeout and stopped by Quit.
func runAction(ctx context.Context, a *actionState, fn func(context.Context) error, done func()) {
	actx, cancel := context.WithTimeout(ctx, actionTimeout)
	defer cancel()
	a.run(actx, fn, done)
}

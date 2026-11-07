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

// Labels returns the menu title and tooltip for the given state.
func Labels(active bool) (title, tooltip string) {
	if active {
		return "Presenting", "prezilla: presentation mode on"
	}
	return "Not presenting", "prezilla: presentation mode off"
}

// Icon returns the embedded PNG for the given state.
func Icon(active bool) []byte {
	if active {
		return icons.Active
	}
	return icons.Idle
}

// remainingText formats the time left until expires, rounded up to whole
// minutes; it is empty when there is no deadline or it has passed.
func remainingText(now, expires time.Time) string {
	if expires.IsZero() || !expires.After(now) {
		return ""
	}
	mins := int((expires.Sub(now) + time.Minute - 1) / time.Minute)
	return fmt.Sprintf("%d min left", mins)
}

// tooltip composes the tooltip: the state label, the remaining time when
// there is one, and the last toggle and expiry error texts when non-empty
// (both are shown, joined with "; "), plus a second line while a screensaver
// prompt is pending.
func tooltip(active bool, remaining, toggleErr, expiryErr string, prompting bool) string {
	_, tip := Labels(active)
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

// actionTimeout bounds one About or website action.
const actionTimeout = 10 * time.Second

// Config holds what the tray needs.
type Config struct {
	Engine  *core.Engine
	Timeout time.Duration
	// Spawn starts the detached deadline watcher after a toggle turns the mode on.
	Spawn    func() error
	About    about.Info
	Notifier system.Notifier
	Opener   system.Opener
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

func websiteAction(o system.Opener, info about.Info) func(context.Context) error {
	return func(ctx context.Context) error {
		if o == nil {
			return errors.New("website: no opener configured")
		}
		return o.Open(ctx, info.Website)
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

// actionState holds the last About or website error for the tooltip.
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

	var tg toggler
	// redraw asks the menu loop to refresh; coalesced, never blocks.
	redraw := make(chan struct{}, 1)
	e := cfg.Engine
	coord := newPromptCoordinator(promptDeps{
		prompter: cfg.Prompter,
		info:     cfg.About,
		status:   e.Status,
		enable:   func(ctx context.Context) error { return e.Enable(ctx, cfg.Timeout) },
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
	systray.Run(func() { onReady(ctx, cancel, cfg, &tg, coord, redraw) }, cancel)
	cancel()
	<-watchDone
	coord.wait()
	tg.wait()
	return nil
}

func onReady(ctx context.Context, cancel context.CancelFunc, cfg Config, tg *toggler, coord *promptCoordinator, redraw <-chan struct{}) {
	e, timeout, spawn := cfg.Engine, cfg.Timeout, cfg.Spawn
	systray.SetIcon(Icon(false))
	systray.SetTitle("prezilla")
	timerItem := systray.AddMenuItem("", "When presentation mode ends")
	timerItem.Disable()
	timerItem.Hide()
	toggle := systray.AddMenuItem("", "")
	continueItem := systray.AddMenuItem("Continue presentation", "Keep presentation mode on for another full timeout")
	continueItem.Hide()
	endItem := systray.AddMenuItem("End presentation", "Turn presentation mode off now")
	endItem.Hide()
	systray.AddSeparator()
	aboutItem := systray.AddMenuItem("About prezilla", "Show version and license")
	siteItem := systray.AddMenuItem("Project website", "Open the project website")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "Quit prezilla")

	var mu sync.Mutex // guards lastErr, lastExpiryErr and the visibility flags
	var lastErr error
	var actions actionState
	var lastExpiryErr string
	var timerShown, promptShown bool

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
		title, _ := Labels(st.Active)
		var toggleText string
		if lastErr != nil {
			toggleText = lastErr.Error()
		}
		remaining, timer := "", ""
		if st.Active {
			remaining = remainingText(now, st.ExpiresAt)
			timer = timerLabel(now, st.ExpiresAt.Local())
		}
		tip := tooltip(st.Active, remaining,
			joinErrText(joinErrText(toggleText, actions.text()), coord.errText()), expText, prompting)
		if timer != "" {
			timerItem.SetTitle(timer)
		}
		if show := timer != ""; show != timerShown {
			timerShown = show
			setVisible(timerItem, show)
		}
		if prompting != promptShown {
			promptShown = prompting
			setVisible(continueItem, prompting)
			setVisible(endItem, prompting)
		}
		systray.SetIcon(Icon(st.Active))
		toggle.SetTitle(title)
		systray.SetTooltip(tip)
	}
	refresh()

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
			case <-continueItem.ClickedCh:
				coord.menuAnswer(ctx, keyContinue)
			case <-endItem.ClickedCh:
				coord.menuAnswer(ctx, keyEnd)
			case <-toggle.ClickedCh:
				tg.start(ctx, func(tctx context.Context) {
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
			case <-aboutItem.ClickedCh:
				go runAction(ctx, &actions, aboutAction(cfg.Notifier, cfg.About), refresh)
			case <-siteItem.ClickedCh:
				go runAction(ctx, &actions, websiteAction(cfg.Opener, cfg.About), refresh)
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

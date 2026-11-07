// Package tray is the StatusNotifierItem front-end for prezilla.
package tray

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"fyne.io/systray"
	"github.com/godbus/dbus/v5"

	"prezilla/internal/core"
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
// there is one, and the last error text when non-empty.
func tooltip(active bool, remaining, errText string) string {
	_, tip := Labels(active)
	if remaining != "" {
		tip += ", " + remaining
	}
	if errText != "" {
		tip += " (error: " + errText + ")"
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
	ctx := context.WithoutCancel(parent)
	go func() {
		defer t.busy.Unlock()
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

// Run shows the tray icon and blocks until Quit is chosen or ctx is cancelled.
func Run(ctx context.Context, e *core.Engine, timeout time.Duration) error {
	if !hostAvailable() {
		return errNoHost
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var tg toggler
	systray.Run(func() { onReady(ctx, cancel, e, timeout, &tg) }, cancel)
	tg.wait()
	return nil
}

func onReady(ctx context.Context, cancel context.CancelFunc, e *core.Engine, timeout time.Duration, tg *toggler) {
	systray.SetIcon(Icon(false))
	systray.SetTitle("prezilla")
	toggle := systray.AddMenuItem("", "")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "Quit prezilla")

	var mu sync.Mutex // guards lastErr and lastExpiryErr
	var lastErr error
	var lastExpiryErr string

	refresh := func() {
		_, expErr := e.DisableIfExpired(ctx)
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
		title, _ := Labels(st.Active)
		var errText string
		switch {
		case lastErr != nil:
			errText = lastErr.Error()
		case expText != "":
			errText = expText
		}
		tip := tooltip(st.Active, func() string {
			if st.Active {
				return remainingText(time.Now(), st.ExpiresAt)
			}
			return ""
		}(), errText)
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
			case <-toggle.ClickedCh:
				tg.start(ctx, func(tctx context.Context) {
					_, err := e.Toggle(tctx, timeout)
					mu.Lock()
					lastErr = err
					mu.Unlock()
					if err != nil {
						fmt.Fprintln(os.Stderr, err)
					}
					refresh()
				})
			case <-quit.ClickedCh:
				cancel()
			}
		}
	}()
}

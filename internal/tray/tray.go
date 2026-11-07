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

	systray.Run(func() { onReady(ctx, cancel, e, timeout) }, cancel)
	return nil
}

func onReady(ctx context.Context, cancel context.CancelFunc, e *core.Engine, timeout time.Duration) {
	systray.SetIcon(Icon(false))
	systray.SetTitle("prezilla")
	toggle := systray.AddMenuItem("", "")
	systray.AddSeparator()
	quit := systray.AddMenuItem("Quit", "Quit prezilla")

	var mu sync.Mutex // guards lastErr
	var lastErr error
	var busy sync.Mutex // serialises toggles; overlapping clicks are dropped

	refresh := func() {
		_, _ = e.DisableIfExpired(ctx)
		st, err := e.Status()
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			systray.SetTooltip("prezilla: " + err.Error())
			return
		}
		title, tip := Labels(st.Active)
		if r := remainingText(time.Now(), st.ExpiresAt); st.Active && r != "" {
			tip += ", " + r
		}
		if lastErr != nil {
			tip += " (error: " + lastErr.Error() + ")"
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
			case <-toggle.ClickedCh:
				if !busy.TryLock() {
					continue
				}
				go func() {
					defer busy.Unlock()
					_, err := e.Toggle(ctx, timeout)
					mu.Lock()
					lastErr = err
					mu.Unlock()
					if err != nil {
						fmt.Fprintln(os.Stderr, err)
					}
					refresh()
				}()
			case <-quit.ClickedCh:
				cancel()
			}
		}
	}()
}

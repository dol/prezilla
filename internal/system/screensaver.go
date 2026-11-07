package system

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// ScreenSaver reports screensaver activation changes.
type ScreenSaver interface {
	// Watch calls onChange with each new screensaver state until ctx is done,
	// then returns nil. onChange runs on the watching goroutine and must not
	// block. A failure to subscribe is returned immediately.
	Watch(ctx context.Context, onChange func(active bool)) error
}

// screenSaverInterfaces are the D-Bus interfaces whose ActiveChanged(b)
// signal is watched. Desktops often emit on several of them.
var screenSaverInterfaces = []string{
	"org.xfce.ScreenSaver",
	"org.freedesktop.ScreenSaver",
	"org.gnome.ScreenSaver",
}

const screenSaverMember = "ActiveChanged"

// DBusScreenSaver watches the ActiveChanged signal of the XFCE, freedesktop
// and GNOME screensaver interfaces on the session bus, matched by interface
// and member only so any implementation (and any object path) works. It uses
// its own private connection for the lifetime of Watch. The zero value is
// valid.
type DBusScreenSaver struct {
	// Timeout bounds connecting and subscribing; zero means 5 seconds.
	Timeout time.Duration
}

func (s DBusScreenSaver) timeout() time.Duration {
	if s.Timeout > 0 {
		return s.Timeout
	}
	return defaultNotifyTimeout
}

// Watch implements ScreenSaver. Consecutive identical states (the same change
// announced on several interfaces) are delivered once. No signal ever
// arriving is not an error: Watch then just waits for ctx.
func (s DBusScreenSaver) Watch(ctx context.Context, onChange func(active bool)) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("screensaver: %w", err)
	}
	timeout := s.timeout()
	conn, err := connectSession(ctx, timeout, "screensaver")
	if err != nil {
		return err
	}
	defer conn.Close()
	ch := make(chan *dbus.Signal, 16)
	conn.Signal(ch)
	defer conn.RemoveSignal(ch)
	sctx, cancel := context.WithTimeout(ctx, timeout)
	for _, iface := range screenSaverInterfaces {
		err := conn.AddMatchSignalContext(sctx,
			dbus.WithMatchInterface(iface), dbus.WithMatchMember(screenSaverMember))
		if err != nil {
			cancel()
			if cerr := boundErr("screensaver: subscribe", sctx, ctx, timeout); cerr != nil {
				return cerr
			}
			return fmt.Errorf("screensaver: subscribe %s: %w", iface, err)
		}
	}
	cancel()
	var d activeDedup
	for {
		select {
		case <-ctx.Done():
			return nil
		case sig, ok := <-ch:
			if !ok {
				if ctx.Err() != nil {
					return nil
				}
				return errors.New("screensaver: connection closed")
			}
			if active, ok := screenSaverActive(sig); ok && d.changed(active) {
				onChange(active)
			}
		}
	}
}

// screenSaverActive extracts the state from an ActiveChanged(b) signal of
// one of the watched interfaces.
func screenSaverActive(sig *dbus.Signal) (active, ok bool) {
	if sig == nil || len(sig.Body) < 1 {
		return false, false
	}
	i := strings.LastIndexByte(sig.Name, '.')
	if i < 0 || sig.Name[i+1:] != screenSaverMember {
		return false, false
	}
	known := false
	for _, iface := range screenSaverInterfaces {
		if sig.Name[:i] == iface {
			known = true
			break
		}
	}
	if !known {
		return false, false
	}
	active, ok = sig.Body[0].(bool)
	return active, ok
}

// activeDedup drops a state equal to the previously delivered one.
type activeDedup struct {
	seen bool
	last bool
}

func (d *activeDedup) changed(active bool) bool {
	if d.seen && d.last == active {
		return false
	}
	d.seen, d.last = true, active
	return true
}

// connectSession opens a private session-bus connection bounded by parent and
// timeout. If the bound fires first the connection, should it still be
// established, is closed in the background. Errors start with prefix.
func connectSession(parent context.Context, timeout time.Duration, prefix string) (*dbus.Conn, error) {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	type result struct {
		conn *dbus.Conn
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := dbus.ConnectSessionBus()
		ch <- result{c, err}
	}()
	select {
	case r := <-ch:
		if r.err != nil {
			return nil, fmt.Errorf("%s: connect: %w", prefix, r.err)
		}
		// The context may have fired while connecting; never act then.
		if err := ctx.Err(); err != nil {
			r.conn.Close()
			return nil, boundErr(prefix+": connect", ctx, parent, timeout)
		}
		return r.conn, nil
	case <-ctx.Done():
		go func() {
			if r := <-ch; r.conn != nil {
				r.conn.Close()
			}
		}()
		return nil, boundErr(prefix+": connect", ctx, parent, timeout)
	}
}

// boundErr describes why bounded (derived from parent with timeout) ended:
// its own timeout or the parent's end. It is nil while bounded is live.
func boundErr(prefix string, bounded, parent context.Context, timeout time.Duration) error {
	if bounded.Err() == nil {
		return nil
	}
	if perr := parent.Err(); perr != nil {
		return fmt.Errorf("%s: %w", prefix, perr)
	}
	return fmt.Errorf("%s: timed out after %s: %w", prefix, timeout, context.DeadlineExceeded)
}

// FakeScreenSaver delivers states passed to Emit to the running Watch. With
// Err set, Watch returns it at once.
type FakeScreenSaver struct {
	Err error

	once sync.Once
	ch   chan bool
}

func (f *FakeScreenSaver) events() chan bool {
	f.once.Do(func() { f.ch = make(chan bool, 64) })
	return f.ch
}

// Emit queues a state for Watch.
func (f *FakeScreenSaver) Emit(active bool) { f.events() <- active }

// Watch implements ScreenSaver.
func (f *FakeScreenSaver) Watch(ctx context.Context, onChange func(active bool)) error {
	if f.Err != nil {
		return f.Err
	}
	ch := f.events()
	for {
		select {
		case <-ctx.Done():
			return nil
		case v := <-ch:
			onChange(v)
		}
	}
}

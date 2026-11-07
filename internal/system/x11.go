package system

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/xevent"
	"github.com/jezek/xgbutil/xprop"
)

// X11Windows talks EWMH to the X server named by DISPLAY. Each call opens its
// own connection and closes it again. The zero value is valid.
type X11Windows struct {
	// Timeout bounds each call; zero means DefaultTimeout.
	Timeout time.Duration
}

func (w X11Windows) timeout() time.Duration {
	if w.Timeout > 0 {
		return w.Timeout
	}
	return DefaultTimeout
}

// withX runs fn with a fresh connection, bounded by ctx and the timeout. The
// library is not context-aware, so fn runs in a goroutine; if it outlives the
// bound it finishes (and closes its connection) in the background.
func withX[T any](parent context.Context, w X11Windows, fn func(context.Context, *xgbutil.XUtil) (T, error)) (T, error) {
	var zero T
	if err := parent.Err(); err != nil {
		return zero, fmt.Errorf("x11: %w", err)
	}
	timeout := w.timeout()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		// A library panic must not take the tray down.
		defer func() {
			if r := recover(); r != nil {
				ch <- result{err: fmt.Errorf("x11: internal error: %v", r)}
			}
		}()
		xu, err := xgbutil.NewConn()
		if err != nil {
			ch <- result{err: fmt.Errorf("x11: connect: %w", err)}
			return
		}
		defer xu.Conn().Close()
		// The context may have fired while connecting; never act then.
		if err := ctx.Err(); err != nil {
			ch <- result{err: fmt.Errorf("x11: %w", err)}
			return
		}
		v, err := fn(ctx, xu)
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && parent.Err() == nil {
			return zero, fmt.Errorf("x11: timed out after %s: %w", timeout, context.DeadlineExceeded)
		}
		return zero, fmt.Errorf("x11: %w", parent.Err())
	}
}

// ShowingDesktop implements Windows by reading _NET_SHOWING_DESKTOP.
func (w X11Windows) ShowingDesktop(ctx context.Context) (bool, error) {
	return withX(ctx, w, func(_ context.Context, xu *xgbutil.XUtil) (bool, error) {
		v, err := showingDesktopGet(xu)
		if err != nil {
			return false, fmt.Errorf("x11: read showing desktop: %w", err)
		}
		return v, nil
	})
}

// showingDesktopGet reads _NET_SHOWING_DESKTOP from the root window. Unlike
// ewmh.ShowingDesktopGet it treats a missing property as false: xfwm4 only
// sets it after the desktop has been shown once, which means not showing.
// Other errors (bad reply, connection) still fail.
func showingDesktopGet(xu *xgbutil.XUtil) (bool, error) {
	atom, err := xprop.Atm(xu, "_NET_SHOWING_DESKTOP")
	if err != nil {
		return false, err
	}
	reply, err := xproto.GetProperty(xu.Conn(), false, xu.RootWin(), atom,
		xproto.GetPropertyTypeAny, 0, 1).Reply()
	if err != nil {
		return false, err
	}
	if reply.Format == 0 {
		return false, nil
	}
	val, err := xprop.PropValNum(reply, nil)
	if err != nil {
		return false, err
	}
	return val == 1, nil
}

// SetShowingDesktop implements Windows by asking the window manager through
// the _NET_SHOWING_DESKTOP client message.
func (w X11Windows) SetShowingDesktop(ctx context.Context, show bool) error {
	_, err := withX(ctx, w, func(ctx context.Context, xu *xgbutil.XUtil) (struct{}, error) {
		intern := func(name string) (xproto.Atom, error) { return xprop.Atm(xu, name) }
		send := func(atom xproto.Atom, n int) error {
			// ewmh.ShowingDesktopReq panics (it passes uint where
			// NewClientMessage wants int), and ewmh.ClientEvent interns the
			// atom itself, so build the message from the resolved atom and
			// send it to the root window with
			// SubstructureNotify|SubstructureRedirect.
			cm, err := xevent.NewClientMessage(32, xu.RootWin(), atom, n)
			if err != nil {
				return err
			}
			mask := xproto.EventMaskSubstructureNotify | xproto.EventMaskSubstructureRedirect
			return xevent.SendRootEvent(xu, cm, uint32(mask))
		}
		return struct{}{}, sendShowingDesktop(ctx, show, intern, send)
	})
	return err
}

// sendShowingDesktop resolves the _NET_SHOWING_DESKTOP atom (an X round
// trip), then re-checks ctx, and only then sends the request, so a caller
// that gave up during the round trip never has the desktop shown or hidden
// behind its back.
func sendShowingDesktop(ctx context.Context, show bool,
	intern func(name string) (xproto.Atom, error), send func(atom xproto.Atom, n int) error) error {
	atom, err := intern("_NET_SHOWING_DESKTOP")
	if err != nil {
		return fmt.Errorf("x11: set showing desktop: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("x11: %w", err)
	}
	n := 0
	if show {
		n = 1
	}
	if err := send(atom, n); err != nil {
		return fmt.Errorf("x11: set showing desktop: %w", err)
	}
	return nil
}

package system

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/ewmh"
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
func withX[T any](parent context.Context, w X11Windows, fn func(*xgbutil.XUtil) (T, error)) (T, error) {
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
		v, err := fn(xu)
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
	return withX(ctx, w, func(xu *xgbutil.XUtil) (bool, error) {
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
	_, err := withX(ctx, w, func(xu *xgbutil.XUtil) (struct{}, error) {
		// ewmh.ShowingDesktopReq panics (it passes uint where
		// NewClientMessage wants int), so send the same message ourselves:
		// to the root window with SubstructureNotify|SubstructureRedirect.
		n := 0
		if show {
			n = 1
		}
		if err := ewmh.ClientEvent(xu, xu.RootWin(), "_NET_SHOWING_DESKTOP", n); err != nil {
			return struct{}{}, fmt.Errorf("x11: set showing desktop: %w", err)
		}
		return struct{}{}, nil
	})
	return err
}

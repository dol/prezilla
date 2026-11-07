package system

import (
	"context"
	"fmt"
	"slices"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/xevent"
	"github.com/jezek/xgbutil/xprop"
)

// regularVisible reports whether a window with the given _NET_WM_WINDOW_TYPE
// and _NET_WM_STATE atom names is a regular window that is not hidden: normal
// or dialog (or untyped), neither HIDDEN nor SKIP_TASKBAR.
func regularVisible(types, states []string) bool {
	for _, s := range states {
		if s == "_NET_WM_STATE_HIDDEN" || s == "_NET_WM_STATE_SKIP_TASKBAR" {
			return false
		}
	}
	if len(types) == 0 {
		return true
	}
	for _, t := range types {
		if t == "_NET_WM_WINDOW_TYPE_NORMAL" || t == "_NET_WM_WINDOW_TYPE_DIALOG" {
			return true
		}
	}
	return false
}

// isHidden reports whether the state atom names contain _NET_WM_STATE_HIDDEN.
func isHidden(states []string) bool {
	return slices.Contains(states, "_NET_WM_STATE_HIDDEN")
}

// restoreOrder picks, from want (bottom to top), the ids that exist in
// clients and whose hidden(id) is true, keeping the order and dropping
// duplicates.
func restoreOrder(want, clients []uint32, hidden func(uint32) bool) []uint32 {
	var out []uint32
	for _, id := range want {
		if slices.Contains(out, id) || !slices.Contains(clients, id) || !hidden(id) {
			continue
		}
		out = append(out, id)
	}
	return out
}

// u32Prop reads a property of 32-bit values. A missing property is an empty
// list; a property of another format is an error.
func u32Prop(xu *xgbutil.XUtil, win xproto.Window, name string) ([]uint32, error) {
	atom, err := xprop.Atm(xu, name)
	if err != nil {
		return nil, err
	}
	reply, err := xproto.GetProperty(xu.Conn(), false, win, atom,
		xproto.GetPropertyTypeAny, 0, 1<<16).Reply()
	if err != nil {
		return nil, err
	}
	if reply.Format == 0 {
		return nil, nil
	}
	if reply.Format != 32 {
		return nil, fmt.Errorf("property %s has format %d", name, reply.Format)
	}
	out := make([]uint32, 0, len(reply.Value)/4)
	for i := 0; i+4 <= len(reply.Value); i += 4 {
		out = append(out, xgb.Get32(reply.Value[i:]))
	}
	return out, nil
}

// atomNames reads a property of atoms and resolves their names. A window that
// vanished (BadWindow) reads as having no such property.
func atomNames(xu *xgbutil.XUtil, win xproto.Window, name string) []string {
	ids, err := u32Prop(xu, win, name)
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range ids {
		r, err := xproto.GetAtomName(xu.Conn(), xproto.Atom(a)).Reply()
		if err != nil {
			continue
		}
		out = append(out, r.Name)
	}
	return out
}

// VisibleWindows implements Windows by reading _NET_CLIENT_LIST_STACKING.
func (w X11Windows) VisibleWindows(ctx context.Context) ([]uint32, error) {
	return withX(ctx, w, func(_ context.Context, xu *xgbutil.XUtil) ([]uint32, error) {
		list, err := u32Prop(xu, xu.RootWin(), "_NET_CLIENT_LIST_STACKING")
		if err != nil {
			return nil, fmt.Errorf("x11: read window list: %w", err)
		}
		if len(list) == 0 {
			// xfwm4 does not always publish the stacking list; the plain
			// list is in mapping order, which is good enough.
			if list, err = u32Prop(xu, xu.RootWin(), "_NET_CLIENT_LIST"); err != nil {
				return nil, fmt.Errorf("x11: read window list: %w", err)
			}
		}
		var out []uint32
		for _, id := range list {
			win := xproto.Window(id)
			if regularVisible(atomNames(xu, win, "_NET_WM_WINDOW_TYPE"), atomNames(xu, win, "_NET_WM_STATE")) {
				out = append(out, id)
			}
		}
		return out, nil
	})
}

// RestoreWindows implements Windows. It asks the window manager to activate
// each hidden window with a _NET_ACTIVE_WINDOW message (source: pager), then
// activates the window that was active at the start again.
func (w X11Windows) RestoreWindows(ctx context.Context, ids []uint32) error {
	_, err := withX(ctx, w, func(ctx context.Context, xu *xgbutil.XUtil) (struct{}, error) {
		return struct{}{}, restoreWindows(ctx, xu, ids)
	})
	return err
}

func restoreWindows(ctx context.Context, xu *xgbutil.XUtil, ids []uint32) error {
	root := xu.RootWin()
	clients, err := u32Prop(xu, root, "_NET_CLIENT_LIST")
	if err != nil {
		return fmt.Errorf("x11: read window list: %w", err)
	}
	active, err := u32Prop(xu, root, "_NET_ACTIVE_WINDOW")
	if err != nil {
		return fmt.Errorf("x11: read active window: %w", err)
	}
	atom, err := xprop.Atm(xu, "_NET_ACTIVE_WINDOW")
	if err != nil {
		return fmt.Errorf("x11: restore windows: %w", err)
	}
	activate := func(id uint32) error {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("x11: %w", err)
		}
		// Source indication 2 (pager), no timestamp, no requestor window.
		cm, err := xevent.NewClientMessage(32, xproto.Window(id), atom, 2, 0, 0)
		if err != nil {
			return err
		}
		mask := xproto.EventMaskSubstructureNotify | xproto.EventMaskSubstructureRedirect
		return xevent.SendRootEvent(xu, cm, uint32(mask))
	}
	hidden := func(id uint32) bool { return isHidden(atomNames(xu, xproto.Window(id), "_NET_WM_STATE")) }
	var errs []error
	for _, id := range restoreOrder(ids, clients, hidden) {
		if err := activate(id); err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("x11: %w", ctx.Err())
			}
			errs = append(errs, err)
		}
	}
	// xfwm4 publishes two values (window, 0); the first is the window.
	if len(active) >= 1 && active[0] != 0 && slices.Contains(clients, active[0]) &&
		!hidden(active[0]) {
		if err := activate(active[0]); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("x11: restore windows: %w", errs[0])
	}
	return nil
}

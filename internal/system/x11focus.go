package system

import (
	"context"
	"fmt"
	"slices"
	"strconv"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/ewmh"
	"github.com/jezek/xgbutil/icccm"
)

// listable reports whether a window with the given _NET_WM_WINDOW_TYPE and
// _NET_WM_STATE atom names belongs in the window list: a normal or dialog
// (or untyped) window that is not skipped by the taskbar. Unlike
// regularVisible it keeps minimized windows, which the list exists to bring
// back.
func listable(types, states []string) bool {
	return !slices.Contains(states, "_NET_WM_STATE_SKIP_TASKBAR") &&
		regularVisible(types, slices.DeleteFunc(slices.Clone(states), func(s string) bool {
			return s == "_NET_WM_STATE_HIDDEN"
		}))
}

// List implements WindowFocus by reading _NET_CLIENT_LIST.
func (w X11Windows) List(ctx context.Context) ([]Window, error) {
	return withX(ctx, w, func(_ context.Context, xu *xgbutil.XUtil) ([]Window, error) {
		list, err := u32Prop(xu, xu.RootWin(), "_NET_CLIENT_LIST")
		if err != nil {
			return nil, fmt.Errorf("x11: read window list: %w", err)
		}
		var out []Window
		for _, id := range list {
			win := xproto.Window(id)
			if !listable(atomNames(xu, win, "_NET_WM_WINDOW_TYPE"), atomNames(xu, win, "_NET_WM_STATE")) {
				continue
			}
			title, err := ewmh.WmNameGet(xu, win)
			if err != nil || title == "" {
				title, _ = icccm.WmNameGet(xu, win)
			}
			if title == "" {
				continue
			}
			out = append(out, Window{ID: strconv.FormatUint(uint64(id), 10), Title: title})
		}
		return out, nil
	})
}

// Focus implements WindowFocus with a _NET_ACTIVE_WINDOW message (source:
// pager), which also unminimizes the window and switches to its workspace.
func (w X11Windows) Focus(ctx context.Context, id string) error {
	n, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		return fmt.Errorf("%w: %q", ErrNoWindow, id)
	}
	_, err = withX(ctx, w, func(_ context.Context, xu *xgbutil.XUtil) (struct{}, error) {
		clients, err := u32Prop(xu, xu.RootWin(), "_NET_CLIENT_LIST")
		if err != nil {
			return struct{}{}, fmt.Errorf("x11: read window list: %w", err)
		}
		if !slices.Contains(clients, uint32(n)) {
			return struct{}{}, fmt.Errorf("%w: %s", ErrNoWindow, id)
		}
		if err := ewmh.ActiveWindowReq(xu, xproto.Window(n)); err != nil {
			return struct{}{}, fmt.Errorf("x11: activate window: %w", err)
		}
		return struct{}{}, nil
	})
	return err
}

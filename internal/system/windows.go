package system

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
)

// Windows reads and changes the window manager's "showing desktop" state.
type Windows interface {
	// ShowingDesktop reports whether the desktop is currently shown.
	ShowingDesktop(ctx context.Context) (bool, error)
	// SetShowingDesktop shows (true) or hides (false) the desktop.
	SetShowingDesktop(ctx context.Context, show bool) error
	// VisibleWindows returns the regular top-level windows that are not
	// hidden (minimized), bottom to top.
	VisibleWindows(ctx context.Context) ([]uint32, error)
	// RestoreWindows shows the listed windows (bottom to top) that still
	// exist and are hidden, then re-activates the window that was active
	// when it started.
	RestoreWindows(ctx context.Context, ids []uint32) error
}

// FakeWindows is an in-memory Windows for tests. Calls records every call as
// "ShowingDesktop", "SetShowingDesktop <bool>", "VisibleWindows" or
// "RestoreWindows <id>,<id>...".
type FakeWindows struct {
	mu sync.Mutex

	Calls []string

	Showing    bool  // current state; updated by a successful SetShowingDesktop
	GetErr     error // returned by ShowingDesktop when set
	SetErr     error // returned by SetShowingDesktop when set
	SetErrOnce error // returned by the first SetShowingDesktop only, then cleared

	Visible    []uint32 // returned by VisibleWindows
	VisibleErr error    // returned by VisibleWindows when set
	RestoreErr error    // returned by RestoreWindows when set
	Restored   []uint32 // ids of the last RestoreWindows call

	// OnRestore, if set, is called with each RestoreWindows's context and ids.
	OnRestore func(ctx context.Context, ids []uint32)

	// OnSet, if set, is called with each SetShowingDesktop's context and
	// value before the result is decided.
	OnSet func(ctx context.Context, show bool)
}

func (f *FakeWindows) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, s)
}

// ShowingDesktop implements Windows.
func (f *FakeWindows) ShowingDesktop(context.Context) (bool, error) {
	f.record("ShowingDesktop")
	if f.GetErr != nil {
		return false, f.GetErr
	}
	return f.Showing, nil
}

// SetShowingDesktop implements Windows. It also updates Showing.
func (f *FakeWindows) SetShowingDesktop(ctx context.Context, show bool) error {
	f.record(fmt.Sprintf("SetShowingDesktop %t", show))
	if f.OnSet != nil {
		f.OnSet(ctx, show)
	}
	if f.SetErrOnce != nil {
		err := f.SetErrOnce
		f.SetErrOnce = nil
		return err
	}
	if f.SetErr != nil {
		return f.SetErr
	}
	f.Showing = show
	return nil
}

// VisibleWindows implements Windows.
func (f *FakeWindows) VisibleWindows(context.Context) ([]uint32, error) {
	f.record("VisibleWindows")
	if f.VisibleErr != nil {
		return nil, f.VisibleErr
	}
	return slices.Clone(f.Visible), nil
}

// RestoreWindows implements Windows.
func (f *FakeWindows) RestoreWindows(ctx context.Context, ids []uint32) error {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatUint(uint64(id), 10)
	}
	f.record("RestoreWindows " + strings.Join(parts, ","))
	if f.OnRestore != nil {
		f.OnRestore(ctx, ids)
	}
	f.mu.Lock()
	f.Restored = slices.Clone(ids)
	f.mu.Unlock()
	return f.RestoreErr
}

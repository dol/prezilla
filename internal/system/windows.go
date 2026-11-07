package system

import (
	"context"
	"fmt"
	"sync"
)

// Windows reads and changes the window manager's "showing desktop" state.
type Windows interface {
	// ShowingDesktop reports whether the desktop is currently shown.
	ShowingDesktop(ctx context.Context) (bool, error)
	// SetShowingDesktop shows (true) or hides (false) the desktop.
	SetShowingDesktop(ctx context.Context, show bool) error
}

// FakeWindows is an in-memory Windows for tests. Calls records every call as
// "ShowingDesktop" or "SetShowingDesktop <bool>".
type FakeWindows struct {
	mu sync.Mutex

	Calls []string

	Showing bool  // current state; updated by a successful SetShowingDesktop
	GetErr  error // returned by ShowingDesktop when set
	SetErr  error // returned by SetShowingDesktop when set
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
func (f *FakeWindows) SetShowingDesktop(_ context.Context, show bool) error {
	f.record(fmt.Sprintf("SetShowingDesktop %t", show))
	if f.SetErr != nil {
		return f.SetErr
	}
	f.Showing = show
	return nil
}

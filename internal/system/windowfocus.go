package system

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
)

// Window is a top-level window a user can bring to the front.
type Window struct {
	// ID names the window to Focus; it is only valid for the WindowFocus
	// that returned it, and for as long as the window exists.
	ID    string
	Title string
	// AppID is the application's identifier, when the system tells it.
	AppID string
}

// WindowFocus lists the open windows and brings one to the front, including
// a minimized one.
type WindowFocus interface {
	// List returns the windows in a stable order.
	List(ctx context.Context) ([]Window, error)
	// Focus raises and activates the window with the given ID.
	Focus(ctx context.Context, id string) error
}

// ErrNoWindowFocus means this session has no way to list and focus windows.
var ErrNoWindowFocus = errors.New("window list: not supported by this session")

// ErrNoWindow means the window to focus no longer exists.
var ErrNoWindow = errors.New("window list: no such window")

// DefaultWindowFocus picks the WindowFocus of the session: the Wayland one
// when WAYLAND_DISPLAY is set, otherwise the X11 one when DISPLAY is set.
// A session with neither yields one whose calls return ErrNoWindowFocus.
func DefaultWindowFocus(getenv func(string) string) WindowFocus {
	switch {
	case getenv("WAYLAND_DISPLAY") != "":
		return &WaylandToplevels{Getenv: getenv}
	case getenv("DISPLAY") != "":
		return X11Windows{}
	}
	return noWindowFocus{}
}

// DefaultWindowPicker returns the picker that goes with DefaultWindowFocus,
// or nil when the session has none: the built-in picker needs X11 window
// ids, so it is offered on X11 sessions only.
func DefaultWindowPicker(getenv func(string) string) WindowPicker {
	if getenv("WAYLAND_DISPLAY") == "" && getenv("DISPLAY") != "" {
		return &X11Picker{}
	}
	return nil
}

type noWindowFocus struct{}

func (noWindowFocus) List(context.Context) ([]Window, error) { return nil, ErrNoWindowFocus }
func (noWindowFocus) Focus(context.Context, string) error    { return ErrNoWindowFocus }

// FakeWindowFocus is an in-memory WindowFocus for tests. Calls records
// "List" and "Focus <id>".
type FakeWindowFocus struct {
	mu sync.Mutex

	Windows  []Window
	ListErr  error
	FocusErr error
	Calls    []string
}

// List implements WindowFocus.
func (f *FakeWindowFocus) List(context.Context) ([]Window, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "List")
	if f.ListErr != nil {
		return nil, f.ListErr
	}
	return slices.Clone(f.Windows), nil
}

// Focus implements WindowFocus.
func (f *FakeWindowFocus) Focus(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "Focus "+id)
	if f.FocusErr != nil {
		return f.FocusErr
	}
	for _, w := range f.Windows {
		if w.ID == id {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrNoWindow, id)
}

// FocusCalls returns the recorded calls that start with prefix.
func (f *FakeWindowFocus) FocusCalls(prefix string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.Calls {
		if strings.HasPrefix(c, prefix) {
			out = append(out, c)
		}
	}
	return out
}

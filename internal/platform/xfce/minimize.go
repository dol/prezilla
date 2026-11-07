package xfce

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

type minimize struct{ w system.Windows }

// NewMinimize shows the desktop (minimizes all windows) while presenting.
func NewMinimize(w system.Windows) core.Switch { return minimize{w} }

func (minimize) ID() string { return core.IDMinimize }

func (s minimize) Enable(ctx context.Context) (string, error) {
	showing, err := s.w.ShowingDesktop(ctx)
	if err != nil {
		return "", fmt.Errorf("read showing desktop: %w", err)
	}
	var ids []uint32
	if !showing {
		// The window manager forgets what it hid when the user activates a
		// window meanwhile (alt+tab), so remember the visible windows too.
		// Failing to read them only costs that safety net.
		ids, _ = s.w.VisibleWindows(ctx)
	}
	if err := s.w.SetShowingDesktop(ctx, true); err != nil {
		if !showing {
			// The request may still have reached the window manager while
			// nothing is saved, so undo it best effort.
			fix, cancel := context.WithTimeout(context.WithoutCancel(ctx), core.CorrectiveTimeout)
			_ = s.w.SetShowingDesktop(fix, false)
			cancel()
		}
		return "", fmt.Errorf("show desktop: %w", err)
	}
	if showing {
		return "on", nil
	}
	return formatMinimizeState(ids), nil
}

// formatMinimizeState is "off" or "off:<id>,<id>,..." (decimal, bottom to top).
func formatMinimizeState(ids []uint32) string {
	if len(ids) == 0 {
		return "off"
	}
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = strconv.FormatUint(uint64(id), 10)
	}
	return "off:" + strings.Join(parts, ",")
}

// parseMinimizeState reports whether saved asks for a restore and which
// windows to bring back. Anything but "on", "off" and "off:<ids>" is invalid.
func parseMinimizeState(saved string) (restore bool, ids []uint32, ok bool) {
	switch {
	case saved == "on":
		return false, nil, true
	case saved == "off":
		return true, nil, true
	case strings.HasPrefix(saved, "off:"):
		for _, p := range strings.Split(saved[len("off:"):], ",") {
			n, err := strconv.ParseUint(p, 10, 32)
			if err != nil || p != strconv.FormatUint(n, 10) {
				return false, nil, false
			}
			ids = append(ids, uint32(n))
		}
		return true, ids, true
	}
	return false, nil, false
}

func (s minimize) Disable(ctx context.Context, saved string) error {
	restore, ids, ok := parseMinimizeState(saved)
	if !ok || !restore {
		return nil
	}
	var errs []error
	// While the window manager still remembers the desktop as shown, it
	// brings back exactly what it hid. Without recorded windows (legacy
	// state) always ask, as before.
	showing := true
	if len(ids) > 0 {
		if v, err := s.w.ShowingDesktop(ctx); err == nil {
			showing = v
		}
	}
	if showing {
		if err := s.w.SetShowingDesktop(ctx, false); err != nil {
			errs = append(errs, fmt.Errorf("restore desktop: %w", err))
		}
	}
	// Windows the window manager forgot (alt+tab clears its memory) stay
	// iconified; bring the recorded ones back.
	if len(ids) > 0 {
		if err := s.w.RestoreWindows(ctx, ids); err != nil {
			errs = append(errs, fmt.Errorf("restore desktop: restore windows: %w", err))
		}
	}
	return errors.Join(errs...)
}

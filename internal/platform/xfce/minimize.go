package xfce

import (
	"context"
	"fmt"

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
	if err := s.w.SetShowingDesktop(ctx, true); err != nil {
		return "", fmt.Errorf("show desktop: %w", err)
	}
	if showing {
		return "on", nil
	}
	return "off", nil
}

func (s minimize) Disable(ctx context.Context, saved string) error {
	if saved != "off" {
		return nil
	}
	if err := s.w.SetShowingDesktop(ctx, false); err != nil {
		return fmt.Errorf("restore desktop: %w", err)
	}
	return nil
}

package xfce

import (
	"context"
	"fmt"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

const (
	powerChannel     = "xfce4-power-manager"
	presentationProp = "/xfce4-power-manager/presentation-mode"
)

type presentation struct{ x system.Xfconf }

// NewPresentation turns on the power manager's presentation mode, which also
// keeps the screensaver and screen blanking off while presenting. It needs
// xfce4-power-manager to be running to have any effect.
func NewPresentation(x system.Xfconf) core.Switch { return presentation{x} }

func (presentation) ID() string { return core.IDPresentation }

// Enable saves the previous value as "true", "false" or "unset".
func (s presentation) Enable(ctx context.Context) (string, error) {
	saved := "unset"
	v, ok, err := s.x.Get(ctx, powerChannel, presentationProp)
	if err != nil {
		return "", fmt.Errorf("read presentation mode: %w", err)
	}
	if ok {
		if v.Kind != system.XfconfBool {
			return "", fmt.Errorf("read presentation mode: unexpected value %q", v.String())
		}
		saved = v.String()
	}
	if err := s.x.Set(ctx, powerChannel, presentationProp, system.XfBool(true)); err != nil {
		if saved != "true" {
			// The request may still have landed while nothing is saved, so
			// undo it best effort (the caller may have given up already).
			fix, cancel := context.WithTimeout(context.WithoutCancel(ctx), core.CorrectiveTimeout)
			_ = s.restore(fix, saved)
			cancel()
		}
		return "", fmt.Errorf("set presentation mode: %w", err)
	}
	return saved, nil
}

func (s presentation) restore(ctx context.Context, saved string) error {
	switch saved {
	case "unset":
		return s.x.Reset(ctx, powerChannel, presentationProp)
	case "true", "false":
		return s.x.Set(ctx, powerChannel, presentationProp, system.XfBool(saved == "true"))
	}
	return fmt.Errorf("invalid saved value %q", saved)
}

func (s presentation) Disable(ctx context.Context, saved string) error {
	if err := s.restore(ctx, saved); err != nil {
		return fmt.Errorf("restore presentation mode: %w", err)
	}
	return nil
}

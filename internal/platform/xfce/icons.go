// Package xfce implements presentation-mode switches for the XFCE desktop.
package xfce

import (
	"context"
	"fmt"
	"strconv"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

const (
	desktopChannel = "xfce4-desktop"
	iconsProp      = "/desktop-icons/style"
)

type icons struct{ x system.Xfconf }

// NewIcons hides desktop icons while presenting.
func NewIcons(x system.Xfconf) core.Switch { return icons{x} }

func (icons) ID() string { return core.IDIcons }

// Enable saves the previous style as a decimal number, or "" when unset.
func (s icons) Enable(ctx context.Context) (string, error) {
	v, ok, err := s.x.Get(ctx, desktopChannel, iconsProp)
	if err != nil {
		return "", fmt.Errorf("read icon style: %w", err)
	}
	saved := ""
	if ok {
		saved = v.String()
	}
	if err := s.x.Set(ctx, desktopChannel, iconsProp, system.XfInt(0)); err != nil {
		return saved, fmt.Errorf("hide icons: %w", err)
	}
	return saved, nil
}

func (s icons) Disable(ctx context.Context, saved string) error {
	if saved == "" {
		if err := s.x.Reset(ctx, desktopChannel, iconsProp); err != nil {
			return fmt.Errorf("restore icon style: %w", err)
		}
		return nil
	}
	n, err := strconv.ParseInt(saved, 10, 32)
	if err != nil {
		return fmt.Errorf("restore icon style: invalid saved value %q", saved)
	}
	if err := s.x.Set(ctx, desktopChannel, iconsProp, system.XfInt(int32(n))); err != nil {
		return fmt.Errorf("restore icon style: %w", err)
	}
	return nil
}

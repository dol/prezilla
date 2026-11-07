package xfce

import (
	"context"
	"fmt"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

const (
	notifyChannel = "xfce4-notifyd"
	dndProp       = "/do-not-disturb"
)

type notifications struct{ x system.Xfconf }

// NewNotifications enables do-not-disturb while presenting.
func NewNotifications(x system.Xfconf) core.Switch { return notifications{x} }

func (notifications) ID() string { return core.IDNotifications }

// Enable saves the previous value as "true" or "false" ("false" when unset).
func (s notifications) Enable(ctx context.Context) (string, error) {
	saved := "false"
	v, ok, err := s.x.Get(ctx, notifyChannel, dndProp)
	if err != nil {
		return "", fmt.Errorf("read do-not-disturb: %w", err)
	}
	if ok {
		saved = v.String()
	}
	if err := s.x.Set(ctx, notifyChannel, dndProp, system.XfBool(true)); err != nil {
		return saved, fmt.Errorf("set do-not-disturb: %w", err)
	}
	return saved, nil
}

func (s notifications) Disable(ctx context.Context, saved string) error {
	if saved != "true" && saved != "false" {
		return fmt.Errorf("restore do-not-disturb: invalid saved value %q", saved)
	}
	if err := s.x.Set(ctx, notifyChannel, dndProp, system.XfBool(saved == "true")); err != nil {
		return fmt.Errorf("restore do-not-disturb: %w", err)
	}
	return nil
}

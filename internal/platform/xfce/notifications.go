package xfce

import (
	"context"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

type notifications struct{ r system.Runner }

// NewNotifications enables do-not-disturb while presenting.
func NewNotifications(r system.Runner) core.Switch { return notifications{r} }

func (notifications) ID() string { return core.IDNotifications }

func (s notifications) set(ctx context.Context, v string) error {
	_, err := s.r.Run(ctx, "xfconf-query", "-c", "xfce4-notifyd", "-p", "/do-not-disturb", "-n", "-t", "bool", "-s", v)
	return err
}

func (s notifications) Enable(ctx context.Context) (string, error) {
	saved, err := s.r.Run(ctx, "xfconf-query", "-c", "xfce4-notifyd", "-p", "/do-not-disturb")
	if err != nil {
		saved = "false"
	}
	return saved, s.set(ctx, "true")
}

func (s notifications) Disable(ctx context.Context, saved string) error {
	return s.set(ctx, saved)
}

// Package xfce implements presentation-mode switches for the XFCE desktop.
package xfce

import (
	"context"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

type icons struct{ r system.Runner }

// NewIcons hides desktop icons while presenting.
func NewIcons(r system.Runner) core.Switch { return icons{r} }

func (icons) ID() string { return core.IDIcons }

func (s icons) Enable(ctx context.Context) (string, error) {
	saved, err := s.r.Run(ctx, "xfconf-query", "-c", "xfce4-desktop", "-p", "/desktop-icons/style")
	if err != nil {
		saved = "" // property unset
	}
	_, err = s.r.Run(ctx, "xfconf-query", "-c", "xfce4-desktop", "-p", "/desktop-icons/style", "-n", "-t", "int", "-s", "0")
	return saved, err
}

func (s icons) Disable(ctx context.Context, saved string) error {
	if saved == "" {
		_, err := s.r.Run(ctx, "xfconf-query", "-c", "xfce4-desktop", "-p", "/desktop-icons/style", "-r")
		return err
	}
	_, err := s.r.Run(ctx, "xfconf-query", "-c", "xfce4-desktop", "-p", "/desktop-icons/style", "-n", "-t", "int", "-s", saved)
	return err
}

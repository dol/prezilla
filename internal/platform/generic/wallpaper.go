// Package generic implements switches that work on any desktop the
// cross-platform libraries support. A desktop that needs more than they
// offer gets its own package, as xfce does.
package generic

import (
	"context"
	"errors"
	"fmt"
	"os"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

type wallpaper struct {
	w     system.Wallpaper
	image string
}

// NewWallpaper swaps the wallpaper for image while presenting and restores
// the previous one afterwards.
func NewWallpaper(w system.Wallpaper, image string) core.Switch { return wallpaper{w, image} }

func (wallpaper) ID() string { return core.IDWallpaper }

func (s wallpaper) Enable(ctx context.Context) (string, error) {
	if s.image == "" {
		return "", errors.New(`wallpaper: no image configured (set "wallpaper" in config)`)
	}
	fi, err := os.Stat(s.image)
	if err != nil || fi.IsDir() {
		return "", fmt.Errorf("wallpaper: image not found: %s", s.image)
	}
	// Without the original there is nothing to restore, so do not touch it.
	orig, err := s.w.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("read wallpaper: %w", err)
	}
	if orig == "" {
		return "", errors.New("wallpaper: current wallpaper unknown, cannot restore it")
	}
	if err := s.w.Set(ctx, s.image); err != nil {
		// The set may have been partially applied.
		// Rolling back must work even when ctx is what failed the set.
		_ = s.w.Set(context.WithoutCancel(ctx), orig)
		return "", fmt.Errorf("set wallpaper: %w", err)
	}
	return orig, nil
}

func (s wallpaper) Disable(ctx context.Context, saved string) error {
	if saved == "" {
		return errors.New("wallpaper: invalid saved state")
	}
	if err := s.w.Set(ctx, saved); err != nil {
		return fmt.Errorf("restore wallpaper: %w", err)
	}
	return nil
}

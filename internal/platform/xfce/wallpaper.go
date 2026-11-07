package xfce

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

type wallpaper struct {
	r     system.Runner
	image string
}

// NewWallpaper swaps every monitor/workspace wallpaper for image while presenting.
func NewWallpaper(r system.Runner, image string) core.Switch { return wallpaper{r, image} }

func (wallpaper) ID() string { return core.IDWallpaper }

func (s wallpaper) set(ctx context.Context, prop, value string) error {
	_, err := s.r.Run(ctx, "xfconf-query", "-c", "xfce4-desktop", "-p", prop, "-s", value)
	return err
}

func (s wallpaper) Enable(ctx context.Context) (string, error) {
	out, err := s.r.Run(ctx, "xfconf-query", "-c", "xfce4-desktop", "-l")
	if err != nil {
		return "", fmt.Errorf("xfconf-query -l: %w", err)
	}
	var props []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); strings.HasSuffix(line, "/last-image") {
			props = append(props, line)
		}
	}
	if len(props) == 0 {
		return "", errors.New("no wallpaper properties found")
	}
	sort.Strings(props)
	orig := make(map[string]string, len(props))
	for _, p := range props {
		v, err := s.r.Run(ctx, "xfconf-query", "-c", "xfce4-desktop", "-p", p)
		if err != nil {
			return "", fmt.Errorf("xfconf-query %s: %w", p, err)
		}
		orig[p] = v
	}
	data, err := json.Marshal(orig)
	if err != nil {
		return "", err
	}
	for i, p := range props {
		if err := s.set(ctx, p, s.image); err != nil {
			// Best-effort rollback of the properties already changed.
			for _, q := range props[:i] {
				_ = s.set(ctx, q, orig[q])
			}
			return "", fmt.Errorf("xfconf-query %s: %w", p, err)
		}
	}
	return string(data), nil
}

func (s wallpaper) Disable(ctx context.Context, saved string) error {
	var orig map[string]string
	if err := json.Unmarshal([]byte(saved), &orig); err != nil {
		return fmt.Errorf("wallpaper: invalid saved state: %w", err)
	}
	props := make([]string, 0, len(orig))
	for p := range orig {
		props = append(props, p)
	}
	sort.Strings(props)
	var errs []error
	for _, p := range props {
		if err := s.set(ctx, p, orig[p]); err != nil {
			errs = append(errs, fmt.Errorf("xfconf-query %s: %w", p, err))
		}
	}
	return errors.Join(errs...)
}

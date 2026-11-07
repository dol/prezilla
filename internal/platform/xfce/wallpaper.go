package xfce

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

const backdropPrefix = "/backdrop"

type wallpaper struct {
	x     system.Xfconf
	image string
}

// NewWallpaper swaps every monitor/workspace wallpaper for image while presenting.
func NewWallpaper(x system.Xfconf, image string) core.Switch { return wallpaper{x, image} }

func (wallpaper) ID() string { return core.IDWallpaper }

func (s wallpaper) set(ctx context.Context, prop, value string) error {
	return s.x.Set(ctx, desktopChannel, prop, system.XfString(value))
}

func (s wallpaper) Enable(ctx context.Context) (string, error) {
	if s.image == "" {
		return "", errors.New(`wallpaper: no image configured (set "wallpaper" in config)`)
	}
	fi, err := os.Stat(s.image)
	if err == nil && fi.IsDir() {
		return "", fmt.Errorf("wallpaper: image not found: %s", s.image)
	}
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("wallpaper: image not found: %s", s.image)
		}
		return "", fmt.Errorf("wallpaper: image %s: %w", s.image, err)
	}
	all, err := s.x.List(ctx, desktopChannel, backdropPrefix)
	if err != nil {
		return "", fmt.Errorf("list wallpapers: %w", err)
	}
	var props []string
	orig := map[string]string{}
	for p, v := range all {
		// An empty original cannot be restored reliably; leave it untouched.
		if strings.HasSuffix(p, "/last-image") && v.Kind == system.XfconfString && v.Str != "" {
			props = append(props, p)
			orig[p] = v.Str
		}
	}
	if len(props) == 0 {
		return "", errors.New("no wallpaper properties found")
	}
	sort.Strings(props)
	data, err := json.Marshal(orig)
	if err != nil {
		return "", err
	}
	for i, p := range props {
		if err := s.set(ctx, p, s.image); err != nil {
			// Best-effort rollback (uses ctx) of the properties already changed,
			// including the failing one, which may have been partially applied.
			for _, q := range props[:i+1] {
				_ = s.set(ctx, q, orig[q])
			}
			return "", fmt.Errorf("set %s: %w", p, err)
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
			errs = append(errs, fmt.Errorf("restore %s: %w", p, err))
		}
	}
	return errors.Join(errs...)
}

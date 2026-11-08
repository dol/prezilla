package generic

import (
	"prezilla/internal/core"
	"prezilla/internal/system"
)

// Switches builds the switches of cfg.Switches that work on any desktop, in
// config order. Others, and unknown IDs, are ignored.
func Switches(b system.Backends, cfg core.Config) []core.Switch {
	var out []core.Switch
	for _, id := range cfg.Switches {
		if sw, ok := New(b, id, cfg.Wallpaper); ok {
			out = append(out, sw)
		}
	}
	return out
}

// All builds every generic switch; the wallpaper's Disable restores from the
// saved value and does not need the image.
func All(b system.Backends, cfg core.Config) []core.Switch {
	return []core.Switch{NewWallpaper(b.Wallpaper, cfg.Wallpaper)}
}

// New builds the single generic switch with the given ID, or false when the
// ID is unknown or has no generic implementation.
func New(b system.Backends, id, image string) (core.Switch, bool) {
	if id == core.IDWallpaper {
		return NewWallpaper(b.Wallpaper, image), true
	}
	return nil, false
}

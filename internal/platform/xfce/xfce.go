// Package xfce implements the presentation-mode switches for XFCE on X11.
package xfce

import (
	"prezilla/internal/core"
	"prezilla/internal/system"
)

// Switches builds the switches listed in cfg.Switches, in config order.
// Unknown IDs are ignored.
func Switches(r system.Runner, cfg core.Config) []core.Switch {
	var out []core.Switch
	for _, id := range cfg.Switches {
		switch id {
		case core.IDIcons:
			out = append(out, NewIcons(r))
		case core.IDNotifications:
			out = append(out, NewNotifications(r))
		case core.IDMute:
			out = append(out, NewMute(r))
		case core.IDMinimize:
			out = append(out, NewMinimize(r))
		case core.IDWallpaper:
			out = append(out, NewWallpaper(r, cfg.Wallpaper))
		}
	}
	return out
}

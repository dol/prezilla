// Package xfce implements the presentation-mode switches for XFCE on X11.
package xfce

import (
	"prezilla/internal/core"
	"prezilla/internal/system"
)

// Switches builds the switches listed in cfg.Switches, in config order.
// Unknown IDs are ignored.
func Switches(b system.Backends, cfg core.Config) []core.Switch {
	var out []core.Switch
	for _, id := range cfg.Switches {
		switch id {
		case core.IDIcons:
			out = append(out, NewIcons(b.Runner))
		case core.IDNotifications:
			out = append(out, NewNotifications(b.Runner))
		case core.IDMute:
			out = append(out, NewMute(b.Audio))
		case core.IDMinimize:
			out = append(out, NewMinimize(b.Windows))
		case core.IDWallpaper:
			out = append(out, NewWallpaper(b.Runner, cfg.Wallpaper))
		}
	}
	return out
}

// All builds every known switch in a fixed order (icons, notifications, mute,
// minimize, wallpaper). The wallpaper switch uses cfg.Wallpaper; its Disable
// restores from the saved value and does not need the image.
func All(b system.Backends, cfg core.Config) []core.Switch {
	return []core.Switch{
		NewIcons(b.Runner),
		NewNotifications(b.Runner),
		NewMute(b.Audio),
		NewMinimize(b.Windows),
		NewWallpaper(b.Runner, cfg.Wallpaper),
	}
}

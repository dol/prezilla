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
			out = append(out, NewIcons(b.Xfconf))
		case core.IDNotifications:
			out = append(out, NewNotifications(b.Xfconf))
		case core.IDPresentation:
			out = append(out, NewPresentation(b.Xfconf))
		case core.IDMute:
			out = append(out, NewMute(b.Audio))
		case core.IDMinimize:
			out = append(out, NewMinimize(b.Windows))
		case core.IDWallpaper:
			out = append(out, NewWallpaper(b.Xfconf, cfg.Wallpaper))
		}
	}
	return out
}

// All builds every known switch in a fixed order (icons, notifications, presentation, mute,
// minimize, wallpaper). The wallpaper switch uses cfg.Wallpaper; its Disable
// restores from the saved value and does not need the image.
func All(b system.Backends, cfg core.Config) []core.Switch {
	return []core.Switch{
		NewIcons(b.Xfconf),
		NewNotifications(b.Xfconf),
		NewPresentation(b.Xfconf),
		NewMute(b.Audio),
		NewMinimize(b.Windows),
		NewWallpaper(b.Xfconf, cfg.Wallpaper),
	}
}

// New builds the single switch with the given ID, or false for an unknown ID.
// It uses the same constructors as Switches and All.
func New(b system.Backends, id, image string) (core.Switch, bool) {
	switch id {
	case core.IDIcons:
		return NewIcons(b.Xfconf), true
	case core.IDNotifications:
		return NewNotifications(b.Xfconf), true
	case core.IDPresentation:
		return NewPresentation(b.Xfconf), true
	case core.IDMute:
		return NewMute(b.Audio), true
	case core.IDMinimize:
		return NewMinimize(b.Windows), true
	case core.IDWallpaper:
		return NewWallpaper(b.Xfconf, image), true
	}
	return nil, false
}

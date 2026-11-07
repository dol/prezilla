// Package core holds the presentation-mode engine and its foundations.
package core

import "context"

// Switch IDs.
const (
	IDIcons         = "icons"
	IDNotifications = "notifications"
	IDPresentation  = "presentation"
	IDMute          = "mute"
	IDMinimize      = "minimize"
	IDWallpaper     = "wallpaper"
)

// SwitchOrder is the canonical order of switches in menus and config files.
var SwitchOrder = []string{IDIcons, IDNotifications, IDPresentation, IDMute, IDMinimize, IDWallpaper}

// Switch is one reversible presentation-mode toggle.
type Switch interface {
	ID() string
	// Enable applies the switch and returns the opaque original value to restore later.
	Enable(ctx context.Context) (saved string, err error)
	// Disable restores the state captured by Enable.
	Disable(ctx context.Context, saved string) error
}

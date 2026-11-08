//go:build !darwin && !windows

package system

// DefaultWallpaper is the Wallpaper of this operating system.
func DefaultWallpaper() Wallpaper { return DesktopWallpaper{Runner: ExecRunner{}} }

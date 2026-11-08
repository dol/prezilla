package system

// DefaultWallpaper is the Wallpaper of this operating system.
func DefaultWallpaper() Wallpaper { return OsascriptWallpaper{Runner: ExecRunner{}} }

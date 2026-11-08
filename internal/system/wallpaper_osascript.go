package system

import (
	"context"
	"strings"
)

// OsascriptWallpaper reads and sets the macOS wallpaper with AppleScript.
type OsascriptWallpaper struct {
	Runner Runner
}

// Get implements Wallpaper.
func (o OsascriptWallpaper) Get(ctx context.Context) (string, error) {
	return o.Runner.Run(ctx, "osascript", "-e",
		`tell application "Finder" to get POSIX path of (get desktop picture as alias)`)
}

// Set implements Wallpaper. It sets the picture on every desktop.
func (o OsascriptWallpaper) Set(ctx context.Context, path string) error {
	_, err := o.Runner.Run(ctx, "osascript", "-e",
		`tell application "System Events" to tell every desktop to set picture to `+appleQuote(path))
	return err
}

// appleQuote renders s as an AppleScript string literal.
func appleQuote(s string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

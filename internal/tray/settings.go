package tray

import (
	"context"
	"errors"
	"fmt"
	"os"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

// settingsEngine is the part of core.Engine the switch tick boxes use.
type settingsEngine interface {
	Status() (core.State, error)
	EnableOne(context.Context, core.Switch) (bool, error)
	DisableOne(context.Context, string) (bool, error)
}

// tickItem is one switch tick box; ticked means the switch is part of
// presentation mode.
type tickItem struct{ ID, Title, Tooltip string }

// tickItems are the switch tick boxes in menu order (core.SwitchOrder).
var tickItems = []tickItem{
	{ID: core.IDIcons, Title: "Hide desktop icons", Tooltip: "Presentation mode hides the desktop icons."},
	{ID: core.IDNotifications, Title: "Disable notifications", Tooltip: "Presentation mode turns notifications off."},
	{ID: core.IDPresentation, Title: "Keep screen awake", Tooltip: "Presentation mode keeps the screen from blanking or locking."},
	{ID: core.IDMute, Title: "Mute sound", Tooltip: "Presentation mode mutes the sound."},
	{ID: core.IDMinimize, Title: "Minimize windows", Tooltip: "Presentation mode minimizes all windows."},
	{ID: core.IDWallpaper, Title: "Change wallpaper", Tooltip: "Presentation mode shows the chosen wallpaper image."},
}

// chooseWallpaperTitle is the entry that opens the image chooser.
const chooseWallpaperTitle = "Choose wallpaper…"

// The notification sent when Choose wallpaper… cannot open a file chooser.
const (
	noZenitySummary = "Choose wallpaper"
	noZenityBody    = `Prezilla needs zenity to browse for an image. Install zenity, or set "wallpaper" in config.json.`
)

// settings holds the switch tick and wallpaper logic behind the tray menu.
// Each method must be called from one goroutine at a time; the tray
// serialises them.
type settings struct {
	ConfigPath string
	Engine     settingsEngine
	// SwitchFor builds the switch id; image is the wallpaper path and is
	// ignored by the other switches.
	SwitchFor func(id, image string) (core.Switch, error)
	Chooser   system.FileChooser
	Notify    func(ctx context.Context, summary, body string) error
	// StartDir is where the file chooser opens.
	StartDir string
}

// Ticks reports for every switch in core.SwitchOrder whether it is ticked:
// while the mode is active, whether it has a saved original (what is really
// applied); otherwise whether config.json enables it. An unparsable config
// is returned as an error in both cases, since a tick change would have to
// write it.
func (s *settings) Ticks() (map[string]bool, error) {
	cfg, err := core.LoadConfig(s.ConfigPath)
	if err != nil {
		return nil, err
	}
	st, err := s.Engine.Status()
	if err != nil {
		return nil, err
	}
	ticks := make(map[string]bool, len(core.SwitchOrder))
	if st.Active {
		for _, id := range core.SwitchOrder {
			_, ticks[id] = st.Saved[id]
		}
		return ticks, nil
	}
	for _, id := range core.SwitchOrder {
		ticks[id] = cfg.Has(id)
	}
	return ticks, nil
}

// SetTick ticks or unticks switch id. While the mode is active the engine
// applies or restores the switch first; then config.json is updated. A failed
// engine call changes nothing; a failed config write after a successful
// engine call is returned and the applied state stays (no rollback).
// Ticking the wallpaper switch without an image opens the chooser instead,
// and a configured image that is gone is reported without changing anything.
func (s *settings) SetTick(ctx context.Context, id string, on bool) error {
	cfg, err := core.LoadConfig(s.ConfigPath)
	if err != nil {
		return err
	}
	if on && id == core.IDWallpaper {
		if cfg.Wallpaper == "" {
			return s.ChooseWallpaper(ctx)
		}
		if err := imageExists(cfg.Wallpaper); err != nil {
			return err
		}
	}
	if on {
		sw, err := s.SwitchFor(id, cfg.Wallpaper)
		if err != nil {
			return err
		}
		if _, err := s.Engine.EnableOne(ctx, sw); err != nil {
			return err
		}
	} else if _, err := s.Engine.DisableOne(ctx, id); err != nil {
		return err
	}
	// Active or not (the mode may have ended meanwhile), the choice is saved.
	_, err = core.UpdateConfig(s.ConfigPath, func(c *core.Config) { *c = c.With(id, on) })
	return err
}

// ChooseWallpaper asks for an image and, when one is chosen, saves it with
// the wallpaper switch ticked. While the mode is active the new image is
// applied at once: the saved original is restored first, then the switch is
// applied with the new image, so the original stays the one saved. A
// cancelled chooser changes nothing; a missing zenity sends one notification
// instead of an error.
func (s *settings) ChooseWallpaper(ctx context.Context) error {
	if _, err := core.LoadConfig(s.ConfigPath); err != nil {
		return err
	}
	path, err := s.Chooser.ChooseImage(ctx, s.StartDir)
	switch {
	case errors.Is(err, system.ErrCancelled):
		return nil
	case errors.Is(err, system.ErrNoZenity):
		if s.Notify == nil {
			return err
		}
		return s.Notify(ctx, noZenitySummary, noZenityBody)
	case err != nil:
		return err
	}
	if err := system.ValidateImage(path); err != nil {
		return err
	}
	active, err := s.Engine.DisableOne(ctx, core.IDWallpaper)
	if err != nil {
		return err
	}
	if active {
		sw, err := s.SwitchFor(core.IDWallpaper, path)
		if err != nil {
			return err
		}
		if _, err := s.Engine.EnableOne(ctx, sw); err != nil {
			return err
		}
	}
	_, err = core.UpdateConfig(s.ConfigPath, func(c *core.Config) {
		*c = c.With(core.IDWallpaper, true)
		c.Wallpaper = path
	})
	return err
}

// imageExists reports a missing image or a directory with the wallpaper
// switch's own error texts, so the stale-image sentence applies.
func imageExists(path string) error {
	fi, err := os.Stat(path)
	switch {
	case errors.Is(err, os.ErrNotExist), err == nil && fi.IsDir():
		return fmt.Errorf("wallpaper: image not found: %s", path)
	case err != nil:
		return fmt.Errorf("wallpaper: image %s: %w", path, err)
	}
	return nil
}

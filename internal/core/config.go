package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"time"
)

// Config is the user configuration.
type Config struct {
	Switches       []string `json:"switches"`
	Wallpaper      string   `json:"wallpaper"`
	TimeoutMinutes int      `json:"timeout_minutes"`
}

// DefaultConfig enables every switch except wallpaper, with a 30 minute timeout.
func DefaultConfig() Config {
	return Config{
		Switches:       []string{IDIcons, IDNotifications, IDMute, IDMinimize},
		TimeoutMinutes: 30,
	}
}

// Timeout returns the auto-off duration.
func (c Config) Timeout() time.Duration {
	return time.Duration(c.TimeoutMinutes) * time.Minute
}

// LoadConfig reads path onto the defaults. A missing file yields the defaults.
func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	if cfg.TimeoutMinutes < 0 {
		return Config{}, fmt.Errorf("config %s: timeout_minutes must not be negative", path)
	}
	if cfg.Wallpaper != "" && !slices.Contains(cfg.Switches, IDWallpaper) {
		cfg.Switches = append(cfg.Switches, IDWallpaper)
	}
	return cfg, nil
}

// DefaultConfigPath is os.UserConfigDir()/prezilla/config.json.
func DefaultConfigPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "prezilla", "config.json")
}

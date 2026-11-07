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
		Switches:       []string{IDIcons, IDNotifications, IDPresentation, IDMute, IDMinimize},
		TimeoutMinutes: 30,
	}
}

// Timeout returns the auto-off duration.
func (c Config) Timeout() time.Duration {
	return time.Duration(c.TimeoutMinutes) * time.Minute
}

// Has reports whether switch id is enabled in the config.
func (c Config) Has(id string) bool { return slices.Contains(c.Switches, id) }

// With returns a copy with switch id enabled or disabled. Enabling inserts at
// the SwitchOrder position; unknown IDs go to the end. c is not modified.
func (c Config) With(id string, on bool) Config {
	out := c
	out.Switches = slices.DeleteFunc(slices.Clone(c.Switches), func(s string) bool { return s == id })
	if on {
		rank := func(s string) int {
			if i := slices.Index(SwitchOrder, s); i >= 0 {
				return i
			}
			return len(SwitchOrder)
		}
		at := len(out.Switches)
		if r := rank(id); r < len(SwitchOrder) {
			for i, s := range out.Switches {
				if rank(s) > r {
					at = i
					break
				}
			}
		}
		out.Switches = slices.Insert(out.Switches, at, id)
	}
	return out
}

// LoadConfig reads path onto the defaults. A missing file yields the defaults.
func LoadConfig(path string) (Config, error) {
	cfg, _, err := readConfig(path)
	return cfg, err
}

// readConfig returns the effective config and the file's top-level keys
// (nil for a missing file). The wallpaper switch is added automatically only
// when the file has no switches key at all.
func readConfig(path string) (Config, map[string]json.RawMessage, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil, nil
	}
	if err != nil {
		return Config{}, nil, fmt.Errorf("read config %s: %w", path, err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return Config{}, nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	if cfg.TimeoutMinutes < 0 {
		return Config{}, nil, fmt.Errorf("config %s: timeout_minutes must not be negative", path)
	}
	if _, hasSwitches := raw["switches"]; !hasSwitches && cfg.Wallpaper != "" && !cfg.Has(IDWallpaper) {
		cfg.Switches = append(cfg.Switches, IDWallpaper)
	}
	return cfg, raw, nil
}

// UpdateConfig reads the config at path, applies fn and writes the whole file
// back atomically, keeping unknown top-level keys. It always writes switches
// and timeout_minutes, and wallpaper only when set. An unparsable file is
// left untouched and reported.
func UpdateConfig(path string, fn func(*Config)) (Config, error) {
	cfg, raw, err := readConfig(path)
	if err != nil {
		return Config{}, err
	}
	fn(&cfg)
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}
	if cfg.Switches == nil {
		cfg.Switches = []string{}
	}
	set := func(key string, v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		raw[key] = b
		return nil
	}
	if err := set("switches", cfg.Switches); err != nil {
		return Config{}, err
	}
	if err := set("timeout_minutes", cfg.TimeoutMinutes); err != nil {
		return Config{}, err
	}
	if cfg.Wallpaper != "" {
		if err := set("wallpaper", cfg.Wallpaper); err != nil {
			return Config{}, err
		}
	} else {
		delete(raw, "wallpaper")
	}
	data, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return Config{}, err
	}
	if err := writeFileAtomic(path, append(data, '\n'), 0o755, 0o644, "config"); err != nil {
		return Config{}, err
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

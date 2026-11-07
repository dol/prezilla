// Package platform selects the desktop-specific switch implementations.
package platform

import (
	"fmt"
	"strings"

	"prezilla/internal/core"
	"prezilla/internal/platform/xfce"
	"prezilla/internal/system"
)

// Detect returns the supported desktop name for XDG_CURRENT_DESKTOP.
func Detect(getenv func(string) string) (string, error) {
	v := getenv("XDG_CURRENT_DESKTOP")
	for _, part := range strings.Split(v, ":") {
		if strings.EqualFold(part, "xfce") {
			return "xfce", nil
		}
	}
	return "", fmt.Errorf("unsupported desktop %q", v)
}

// Switches builds the switches for desktop as configured in cfg.
func Switches(desktop string, b system.Backends, cfg core.Config) ([]core.Switch, error) {
	switch desktop {
	case "xfce":
		return xfce.Switches(b, cfg), nil
	}
	return nil, fmt.Errorf("unsupported desktop %q", desktop)
}

// AllSwitches builds every known switch for desktop, used to restore saved
// entries whose switch is no longer in the config.
func AllSwitches(desktop string, b system.Backends, cfg core.Config) ([]core.Switch, error) {
	switch desktop {
	case "xfce":
		return xfce.All(b, cfg), nil
	}
	return nil, fmt.Errorf("unsupported desktop %q", desktop)
}

// Switch builds the single switch id for desktop. An unsupported desktop or an
// unknown id is an error.
func Switch(desktop string, b system.Backends, id, image string) (core.Switch, error) {
	switch desktop {
	case "xfce":
		if sw, ok := xfce.New(b, id, image); ok {
			return sw, nil
		}
		return nil, fmt.Errorf("unknown switch %q", id)
	}
	return nil, fmt.Errorf("unsupported desktop %q", desktop)
}

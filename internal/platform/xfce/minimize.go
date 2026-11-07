package xfce

import (
	"context"
	"fmt"
	"strings"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

type minimize struct{ r system.Runner }

// NewMinimize shows the desktop (minimizes all windows) while presenting.
func NewMinimize(r system.Runner) core.Switch { return minimize{r} }

func (minimize) ID() string { return core.IDMinimize }

func (s minimize) Enable(ctx context.Context) (string, error) {
	out, err := s.r.Run(ctx, "env", "LC_ALL=C", "wmctrl", "-m")
	if err != nil {
		return "", fmt.Errorf("wmctrl -m: %w", err)
	}
	saved, ok := parseShowingDesktop(out)
	if !ok {
		return "", fmt.Errorf("wmctrl -m: unexpected output %q", out)
	}
	if _, err := s.r.Run(ctx, "wmctrl", "-k", "on"); err != nil {
		return "", fmt.Errorf("wmctrl -k on: %w", err)
	}
	return saved, nil
}

func (s minimize) Disable(ctx context.Context, saved string) error {
	if saved != "off" {
		return nil
	}
	if _, err := s.r.Run(ctx, "wmctrl", "-k", "off"); err != nil {
		return fmt.Errorf("wmctrl -k off: %w", err)
	}
	return nil
}

// parseShowingDesktop finds the "Showing the desktop: ON|OFF" line.
func parseShowingDesktop(out string) (string, bool) {
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(strings.ToLower(line), "showing the desktop") {
			continue
		}
		i := strings.LastIndex(line, ":")
		if i < 0 {
			return "", false
		}
		switch strings.ToLower(strings.TrimSpace(line[i+1:])) {
		case "on":
			return "on", true
		case "off":
			return "off", true
		}
		return "", false
	}
	return "", false
}

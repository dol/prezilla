package xfce

import (
	"context"
	"fmt"
	"strings"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

type mute struct{ r system.Runner }

// NewMute mutes the default audio sink while presenting.
func NewMute(r system.Runner) core.Switch { return mute{r} }

func (mute) ID() string { return core.IDMute }

func (s mute) Enable(ctx context.Context) (string, error) {
	out, err := s.r.Run(ctx, "env", "LC_ALL=C", "pactl", "get-sink-mute", "@DEFAULT_SINK@")
	if err != nil {
		return "", fmt.Errorf("pactl get-sink-mute: %w", err)
	}
	var saved string
	switch strings.TrimSpace(out) {
	case "Mute: yes":
		saved = "yes"
	case "Mute: no":
		saved = "no"
	default:
		return "", fmt.Errorf("pactl get-sink-mute: unexpected output %q", out)
	}
	if _, err := s.r.Run(ctx, "pactl", "set-sink-mute", "@DEFAULT_SINK@", "1"); err != nil {
		return "", fmt.Errorf("pactl set-sink-mute: %w", err)
	}
	return saved, nil
}

func (s mute) Disable(ctx context.Context, saved string) error {
	if saved != "no" {
		return nil
	}
	if _, err := s.r.Run(ctx, "pactl", "set-sink-mute", "@DEFAULT_SINK@", "0"); err != nil {
		return fmt.Errorf("pactl set-sink-mute: %w", err)
	}
	return nil
}

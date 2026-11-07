package xfce

import (
	"context"
	"fmt"
	"strings"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

// defaultSinkAlias is pactl's alias for whatever the default sink is now.
const defaultSinkAlias = "@DEFAULT_SINK@"

type mute struct{ r system.Runner }

// NewMute mutes the default audio sink while presenting and later unmutes
// that same sink, even if the default output has changed in between.
func NewMute(r system.Runner) core.Switch { return mute{r} }

func (mute) ID() string { return core.IDMute }

// Enable saves "<sink name>\t<yes|no>".
func (s mute) Enable(ctx context.Context) (string, error) {
	sink := defaultSinkAlias
	if out, err := s.r.Run(ctx, "env", "LC_ALL=C", "pactl", "get-default-sink"); err == nil {
		if name := strings.TrimSpace(out); name != "" {
			sink = name
		}
	}
	out, err := s.r.Run(ctx, "env", "LC_ALL=C", "pactl", "get-sink-mute", sink)
	if err != nil {
		return "", fmt.Errorf("pactl get-sink-mute: %w", err)
	}
	var was string
	switch strings.TrimSpace(out) {
	case "Mute: yes":
		was = "yes"
	case "Mute: no":
		was = "no"
	default:
		return "", fmt.Errorf("pactl get-sink-mute: unexpected output %q", out)
	}
	if _, err := s.r.Run(ctx, "pactl", "set-sink-mute", sink, "1"); err != nil {
		return "", fmt.Errorf("pactl set-sink-mute: %w", err)
	}
	return sink + "\t" + was, nil
}

// Disable unmutes the saved sink if it was unmuted before. Legacy values
// "yes"/"no" (no sink name) refer to the current default sink. A named sink
// that no longer exists has nothing to restore.
func (s mute) Disable(ctx context.Context, saved string) error {
	sink, was, ok := strings.Cut(saved, "\t")
	if !ok {
		sink, was = defaultSinkAlias, saved
	}
	if was != "no" {
		return nil
	}
	if sink != defaultSinkAlias && !s.sinkExists(ctx, sink) {
		return nil
	}
	if _, err := s.r.Run(ctx, "pactl", "set-sink-mute", sink, "0"); err != nil {
		return fmt.Errorf("pactl set-sink-mute: %w", err)
	}
	return nil
}

// sinkExists reports whether name is a whole tab-separated field of a
// "pactl list short sinks" line. If the list cannot be read it assumes the
// sink exists, so the unmute is still attempted and any failure reported.
func (s mute) sinkExists(ctx context.Context, name string) bool {
	out, err := s.r.Run(ctx, "pactl", "list", "short", "sinks")
	if err != nil {
		return true
	}
	for _, line := range strings.Split(out, "\n") {
		for _, field := range strings.Split(line, "\t") {
			if strings.TrimSpace(field) == name {
				return true
			}
		}
	}
	return false
}

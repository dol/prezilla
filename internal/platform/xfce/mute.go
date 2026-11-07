package xfce

import (
	"context"
	"fmt"
	"strings"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

// defaultSinkAlias is the audio server's alias for whatever the default sink
// is now. It is also the literal name stored in legacy and fallback state.
const defaultSinkAlias = "@DEFAULT_SINK@"

type mute struct{ a system.Audio }

// NewMute mutes the default audio sink while presenting and later unmutes
// that same sink, even if the default output has changed in between.
func NewMute(a system.Audio) core.Switch { return mute{a} }

func (mute) ID() string { return core.IDMute }

// Enable saves "<sink name>\t<yes|no>".
func (s mute) Enable(ctx context.Context) (string, error) {
	sink := defaultSinkAlias
	if name, err := s.a.DefaultSinkName(ctx); err == nil {
		if name = strings.TrimSpace(name); name != "" {
			sink = name
		}
	}
	muted, err := s.a.SinkMuted(ctx, sink)
	if err != nil {
		return "", fmt.Errorf("read sink mute: %w", err)
	}
	if err := s.a.SetSinkMute(ctx, sink, true); err != nil {
		if !muted {
			// The request may still have landed (a library call cannot be
			// cancelled) while nothing is saved, so undo it best effort.
			fix, cancel := context.WithTimeout(context.WithoutCancel(ctx), core.CorrectiveTimeout)
			_ = s.a.SetSinkMute(fix, sink, false)
			cancel()
		}
		return "", fmt.Errorf("mute sink: %w", err)
	}
	was := "no"
	if muted {
		was = "yes"
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
	if was != "no" || sink == "" {
		return nil
	}
	// If the sink list cannot be read, assume the sink exists so the unmute
	// is still attempted and any failure reported.
	if sink != defaultSinkAlias {
		if exists, err := s.a.SinkExists(ctx, sink); err == nil && !exists {
			return nil
		}
	}
	if err := s.a.SetSinkMute(ctx, sink, false); err != nil {
		return fmt.Errorf("unmute sink: %w", err)
	}
	return nil
}

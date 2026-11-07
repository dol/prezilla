package system

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jfreymuth/pulse"
	"github.com/jfreymuth/pulse/proto"
)

// PulseAudio talks to a PulseAudio or PipeWire-pulse server natively. Each
// call opens its own connection and closes it again. The zero value is valid.
type PulseAudio struct {
	// Timeout bounds each call; zero means DefaultTimeout.
	Timeout time.Duration
}

func (p PulseAudio) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return DefaultTimeout
}

// withClient runs fn with a fresh client, bounded by ctx and the timeout. The
// library is not context-aware, so fn runs in a goroutine; if it outlives the
// bound it finishes (and closes its client) in the background.
func withClient[T any](parent context.Context, p PulseAudio, fn func(*pulse.Client) (T, error)) (T, error) {
	var zero T
	timeout := p.timeout()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	type result struct {
		v   T
		err error
	}
	ch := make(chan result, 1)
	go func() {
		c, err := pulse.NewClient(pulse.ClientApplicationName("prezilla"))
		if err != nil {
			ch <- result{err: fmt.Errorf("pulse: connect: %w", err)}
			return
		}
		defer c.Close()
		v, err := fn(c)
		ch <- result{v, err}
	}()
	select {
	case r := <-ch:
		return r.v, r.err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && parent.Err() == nil {
			return zero, fmt.Errorf("pulse: timed out after %s: %w", timeout, context.DeadlineExceeded)
		}
		return zero, fmt.Errorf("pulse: %w", parent.Err())
	}
}

// DefaultSinkName implements Audio.
func (p PulseAudio) DefaultSinkName(ctx context.Context) (string, error) {
	return withClient(ctx, p, func(c *pulse.Client) (string, error) {
		s, err := c.DefaultSink()
		if err != nil {
			return "", fmt.Errorf("pulse: default sink: %w", err)
		}
		return s.ID(), nil
	})
}

// SinkMuted implements Audio. The sink is looked up by name.
func (p PulseAudio) SinkMuted(ctx context.Context, name string) (bool, error) {
	return withClient(ctx, p, func(c *pulse.Client) (bool, error) {
		var rep proto.GetSinkInfoReply
		err := c.RawRequest(&proto.GetSinkInfo{SinkIndex: proto.Undefined, SinkName: name}, &rep)
		if err != nil {
			return false, fmt.Errorf("pulse: get sink %q: %w", name, err)
		}
		return rep.Mute, nil
	})
}

// SetSinkMute implements Audio. The sink is addressed by name, not index.
func (p PulseAudio) SetSinkMute(ctx context.Context, name string, mute bool) error {
	_, err := withClient(ctx, p, func(c *pulse.Client) (struct{}, error) {
		err := c.RawRequest(&proto.SetSinkMute{SinkIndex: proto.Undefined, SinkName: name, Mute: mute}, nil)
		if err != nil {
			return struct{}{}, fmt.Errorf("pulse: set sink mute %q: %w", name, err)
		}
		return struct{}{}, nil
	})
	return err
}

// SinkExists implements Audio using an exact name match.
func (p PulseAudio) SinkExists(ctx context.Context, name string) (bool, error) {
	return withClient(ctx, p, func(c *pulse.Client) (bool, error) {
		sinks, err := c.ListSinks()
		if err != nil {
			return false, fmt.Errorf("pulse: list sinks: %w", err)
		}
		for _, s := range sinks {
			if s.ID() == name {
				return true, nil
			}
		}
		return false, nil
	})
}

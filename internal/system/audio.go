package system

import (
	"context"
	"fmt"
	"slices"
	"sync"
)

// Audio reads and changes the mute state of audio output sinks.
type Audio interface {
	// DefaultSinkName returns the name of the current default sink.
	DefaultSinkName(ctx context.Context) (string, error)
	// SinkMuted reports whether the named sink is muted.
	SinkMuted(ctx context.Context, name string) (bool, error)
	// SetSinkMute mutes or unmutes the named sink.
	SetSinkMute(ctx context.Context, name string, mute bool) error
	// SinkExists reports whether a sink with exactly this name exists.
	SinkExists(ctx context.Context, name string) (bool, error)
}

// Backends bundles the system interfaces the platform switches use.
type Backends struct {
	Runner  Runner
	Audio   Audio
	Windows Windows
	Xfconf  Xfconf
	// Wallpaper serves desktops without their own wallpaper switch.
	Wallpaper Wallpaper
}

// FakeAudio is an in-memory Audio for tests. Set the exported fields before
// use; Calls records every call as a string such as "SinkMuted <name>" or
// "SetSinkMute <name> true".
type FakeAudio struct {
	mu sync.Mutex

	Calls []string

	Default    string          // returned by DefaultSinkName
	DefaultErr error           // returned by DefaultSinkName when set
	Muted      map[string]bool // mute state per sink name; unknown names error in SinkMuted
	MutedErr   error           // returned by SinkMuted when set
	SetErr     error           // returned by SetSinkMute when set
	SetErrOnce error           // returned by the first SetSinkMute only, then cleared
	Sinks      []string        // sinks SinkExists knows about
	SinksErr   error           // returned by SinkExists when set
}

func (f *FakeAudio) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, s)
}

// DefaultSinkName implements Audio.
func (f *FakeAudio) DefaultSinkName(context.Context) (string, error) {
	f.record("DefaultSinkName")
	return f.Default, f.DefaultErr
}

// SinkMuted implements Audio.
func (f *FakeAudio) SinkMuted(_ context.Context, name string) (bool, error) {
	f.record("SinkMuted " + name)
	if f.MutedErr != nil {
		return false, f.MutedErr
	}
	m, ok := f.Muted[name]
	if !ok {
		return false, fmt.Errorf("fake audio: no such sink %q", name)
	}
	return m, nil
}

// SetSinkMute implements Audio. It also updates Muted.
func (f *FakeAudio) SetSinkMute(_ context.Context, name string, mute bool) error {
	f.record(fmt.Sprintf("SetSinkMute %s %t", name, mute))
	if f.SetErrOnce != nil {
		err := f.SetErrOnce
		f.SetErrOnce = nil
		return err
	}
	if f.SetErr != nil {
		return f.SetErr
	}
	if f.Muted == nil {
		f.Muted = map[string]bool{}
	}
	f.Muted[name] = mute
	return nil
}

// SinkExists implements Audio.
func (f *FakeAudio) SinkExists(_ context.Context, name string) (bool, error) {
	f.record("SinkExists " + name)
	if f.SinksErr != nil {
		return false, f.SinksErr
	}
	return slices.Contains(f.Sinks, name), nil
}

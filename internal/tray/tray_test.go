package tray

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"prezilla/internal/core"
	"prezilla/internal/tray/icons"
)

func TestLabelsActiveAndInactive(t *testing.T) {
	title, tip := Labels(true)
	if title != "Presenting" || tip != "prezilla: presentation mode on" {
		t.Errorf("active: %q, %q", title, tip)
	}
	title, tip = Labels(false)
	if title != "Not presenting" || tip != "prezilla: presentation mode off" {
		t.Errorf("inactive: %q, %q", title, tip)
	}
}

func TestIconReturnsEmbeddedPNGPerState(t *testing.T) {
	if !bytes.Equal(Icon(true), icons.Active) {
		t.Error("Icon(true) != icons.Active")
	}
	if !bytes.Equal(Icon(false), icons.Idle) {
		t.Error("Icon(false) != icons.Idle")
	}
	if bytes.Equal(Icon(true), Icon(false)) {
		t.Error("icons should differ")
	}
}

func TestRemainingText(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		expires time.Time
		want    string
	}{
		{time.Time{}, ""},
		{now.Add(-time.Minute), ""},
		{now, ""},
		{now.Add(30 * time.Second), "1 min left"},
		{now.Add(time.Minute), "1 min left"},
		{now.Add(90 * time.Second), "2 min left"},
		{now.Add(3 * time.Minute), "3 min left"},
	}
	for _, c := range cases {
		if got := remainingText(now, c.expires); got != c.want {
			t.Errorf("expires %v: got %q want %q", c.expires.Sub(now), got, c.want)
		}
	}
}

func TestTooltip(t *testing.T) {
	cases := []struct {
		active    bool
		remaining string
		toggleErr string
		expiryErr string
		want      string
	}{
		{false, "", "", "", "prezilla: presentation mode off"},
		{true, "", "", "", "prezilla: presentation mode on"},
		{true, "3 min left", "", "", "prezilla: presentation mode on, 3 min left"},
		{false, "", "boom", "", "prezilla: presentation mode off (error: boom)"},
		{true, "1 min left", "boom", "", "prezilla: presentation mode on, 1 min left (error: boom)"},
		{true, "", "", "expired", "prezilla: presentation mode on (error: expired)"},
		{true, "", "boom", "expired", "prezilla: presentation mode on (error: boom; expired)"},
	}
	for _, c := range cases {
		if got := tooltip(c.active, c.remaining, c.toggleErr, c.expiryErr); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
}

func TestTogglerGuardsDetachesAndWaits(t *testing.T) {
	var tg toggler
	parent, cancel := context.WithCancel(context.Background())
	started, release, got := make(chan struct{}), make(chan struct{}), make(chan context.Context, 1)
	if !tg.start(parent, func(ctx context.Context) {
		got <- ctx
		close(started)
		<-release
	}) {
		t.Fatal("first start refused")
	}
	<-started
	if tg.start(parent, func(context.Context) { t.Error("overlapping toggle ran") }) {
		t.Error("overlapping start accepted")
	}
	cancel()
	if err := (<-got).Err(); err != nil {
		t.Errorf("toggle ctx cancelled with parent: %v", err)
	}
	done := make(chan struct{})
	go func() { tg.wait(); close(done) }()
	select {
	case <-done:
		t.Fatal("wait returned while toggle running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("wait did not return after toggle finished")
	}
	tg.wait() // idle: must not block
}

func TestSpawnIfDeadline(t *testing.T) {
	deadline := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		st    core.State
		stErr error
		want  int
	}{
		{"active with deadline", core.State{Active: true, ExpiresAt: deadline}, nil, 1},
		{"active without deadline", core.State{Active: true}, nil, 0},
		{"inactive", core.State{ExpiresAt: deadline}, nil, 0},
		{"status error", core.State{}, errors.New("read"), 0},
	}
	for _, c := range cases {
		n := 0
		err := spawnIfDeadline(func() (core.State, error) { return c.st, c.stErr }, func() error { n++; return nil })
		if n != c.want || err != nil {
			t.Errorf("%s: spawned %d err %v, want %d", c.name, n, err, c.want)
		}
	}
	err := spawnIfDeadline(func() (core.State, error) { return core.State{Active: true, ExpiresAt: deadline}, nil },
		func() error { return errors.New("exec failed") })
	if err == nil || !strings.Contains(err.Error(), "start watcher: exec failed") {
		t.Errorf("spawn error = %v", err)
	}
	if err := spawnIfDeadline(func() (core.State, error) { return core.State{Active: true, ExpiresAt: deadline}, nil }, nil); err != nil {
		t.Errorf("nil spawn: %v", err)
	}
}

package tray

import (
	"bytes"
	"context"
	"testing"
	"time"

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
		errText   string
		want      string
	}{
		{false, "", "", "prezilla: presentation mode off"},
		{true, "", "", "prezilla: presentation mode on"},
		{true, "3 min left", "", "prezilla: presentation mode on, 3 min left"},
		{false, "", "boom", "prezilla: presentation mode off (error: boom)"},
		{true, "1 min left", "boom", "prezilla: presentation mode on, 1 min left (error: boom)"},
	}
	for _, c := range cases {
		if got := tooltip(c.active, c.remaining, c.errText); got != c.want {
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

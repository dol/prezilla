package tray

import (
	"bytes"
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

package tray

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"prezilla/internal/system"
)

func TestSlotTitleShortensLongTitles(t *testing.T) {
	if got := slotTitle(system.Window{Title: "Slides"}); got != "Slides" {
		t.Errorf("short: %q", got)
	}
	long := strings.Repeat("ä", maxWindowTitle+10)
	got := slotTitle(system.Window{Title: long})
	if r := []rune(got); len(r) != maxWindowTitle || r[len(r)-1] != '…' {
		t.Errorf("long: %q (%d runes)", got, len(r))
	}
}

func TestWindowListRefreshAndFocusBySlot(t *testing.T) {
	f := &system.FakeWindowFocus{Windows: []system.Window{{ID: "7", Title: "Slides"}, {ID: "9", Title: "Terminal"}}}
	l := &windowList{src: f}
	titles := l.refresh(context.Background())
	if len(titles) != 2 || titles[0] != "Slides" || titles[1] != "Terminal" {
		t.Fatalf("titles %v", titles)
	}
	if err := l.focus(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	if got := f.FocusCalls("Focus"); len(got) != 1 || got[0] != "Focus 9" {
		t.Errorf("focus calls %v", got)
	}
	if err := l.focus(context.Background(), 5); !errors.Is(err, system.ErrNoWindow) {
		t.Errorf("slot out of range: %v", err)
	}
}

func TestWindowListCapsTheSlots(t *testing.T) {
	var ws []system.Window
	for i := 0; i < maxWindowSlots+5; i++ {
		ws = append(ws, system.Window{ID: fmt.Sprint(i), Title: fmt.Sprint("w", i)})
	}
	l := &windowList{src: &system.FakeWindowFocus{Windows: ws}}
	if got := l.refresh(context.Background()); len(got) != maxWindowSlots {
		t.Errorf("got %d titles", len(got))
	}
}

func TestWindowListGivesUpOnAnUnsupportedSession(t *testing.T) {
	f := &system.FakeWindowFocus{ListErr: fmt.Errorf("wayland: %w", system.ErrNoWindowFocus)}
	l := &windowList{src: f}
	if got := l.refresh(context.Background()); got != nil {
		t.Errorf("got %v", got)
	}
	l.refresh(context.Background())
	if n := len(f.FocusCalls("List")); n != 1 {
		t.Errorf("List called %d times, want once", n)
	}
	if l.supported() {
		t.Error("still supported")
	}
}

func TestWindowListKeepsTryingAfterATransientError(t *testing.T) {
	f := &system.FakeWindowFocus{ListErr: errors.New("x11: timed out")}
	l := &windowList{src: f}
	l.refresh(context.Background())
	f.ListErr = nil
	f.Windows = []system.Window{{ID: "1", Title: "A"}}
	if got := l.refresh(context.Background()); len(got) != 1 {
		t.Errorf("got %v", got)
	}
}

func TestNilWindowListIsUnsupported(t *testing.T) {
	var l *windowList
	if l.supported() || (&windowList{}).supported() {
		t.Error("nil source must be unsupported")
	}
	if got := (&windowList{}).refresh(context.Background()); got != nil {
		t.Errorf("got %v", got)
	}
}

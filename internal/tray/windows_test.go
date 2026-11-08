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

type fakePicker struct {
	id   string
	err  error
	seen []system.Window
}

func (p *fakePicker) Pick(_ context.Context, ws []system.Window) (string, error) {
	p.seen = ws
	return p.id, p.err
}

func TestPickFocusesTheChosenWindow(t *testing.T) {
	f := &system.FakeWindowFocus{Windows: []system.Window{{ID: "7", Title: "Slides"}, {ID: "9", Title: "Terminal"}}}
	p := &fakePicker{id: "9"}
	if err := (&windowList{src: f}).pick(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(p.seen) != 2 {
		t.Errorf("picker saw %v", p.seen)
	}
	if got := f.FocusCalls("Focus"); len(got) != 1 || got[0] != "Focus 9" {
		t.Errorf("focus calls %v", got)
	}
}

func TestPickCancelledIsNotAnError(t *testing.T) {
	f := &system.FakeWindowFocus{Windows: []system.Window{{ID: "7", Title: "Slides"}}}
	if err := (&windowList{src: f}).pick(context.Background(), &fakePicker{err: system.ErrCancelled}); err != nil {
		t.Errorf("err %v", err)
	}
	if got := f.FocusCalls("Focus"); len(got) != 0 {
		t.Errorf("focus calls %v", got)
	}
}

func TestPickReportsListAndPickerErrors(t *testing.T) {
	boom := errors.New("boom")
	if err := (&windowList{src: &system.FakeWindowFocus{ListErr: boom}}).pick(context.Background(), &fakePicker{}); !errors.Is(err, boom) {
		t.Errorf("list: %v", err)
	}
	f := &system.FakeWindowFocus{Windows: []system.Window{{ID: "7"}}}
	if err := (&windowList{src: f}).pick(context.Background(), &fakePicker{err: boom}); !errors.Is(err, boom) {
		t.Errorf("picker: %v", err)
	}
}

func TestChooseFirstReturnsTheChosenWindowOrNothing(t *testing.T) {
	ws := []system.Window{{ID: "7", Title: "Slides"}}
	cases := []struct {
		name   string
		f      *system.FakeWindowFocus
		p      *fakePicker
		want   string
		picked bool
	}{
		{"chosen", &system.FakeWindowFocus{Windows: ws}, &fakePicker{id: "7"}, "7", true},
		{"closed", &system.FakeWindowFocus{Windows: ws}, &fakePicker{err: system.ErrCancelled}, "", true},
		{"picker fails", &system.FakeWindowFocus{Windows: ws}, &fakePicker{err: errors.New("boom")}, "", true},
		{"no windows", &system.FakeWindowFocus{}, &fakePicker{id: "7"}, "", false},
		{"list fails", &system.FakeWindowFocus{ListErr: errors.New("boom")}, &fakePicker{id: "7"}, "", false},
	}
	for _, c := range cases {
		got := (&windowList{src: c.f}).chooseFirst(context.Background(), c.p)
		if got != c.want || (c.p.seen != nil) != c.picked {
			t.Errorf("%s: got %q, picker shown %v", c.name, got, c.p.seen != nil)
		}
	}
}

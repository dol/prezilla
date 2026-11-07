package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExtendInactiveIsNoOp(t *testing.T) {
	a := &fakeSwitch{id: "a", savedVal: "1"}
	e, store, _ := newTestEngine(t, a)
	ok, err := e.Extend(context.Background(), 30*time.Minute)
	if ok || err != nil {
		t.Fatalf("ok %v err %v", ok, err)
	}
	if a.enabled != 0 || a.disabled != 0 {
		t.Errorf("switch called: enabled %d disabled %d", a.enabled, a.disabled)
	}
	if _, err := os.Stat(store.path); !os.IsNotExist(err) {
		t.Errorf("state file written: %v", err)
	}
	st, _ := e.Status()
	if st.Active || !st.ExpiresAt.IsZero() {
		t.Errorf("state %+v", st)
	}
}

func TestExtendInactiveWithStaleDeadlineDoesNotSave(t *testing.T) {
	a := &fakeSwitch{id: "a", savedVal: "1"}
	e, store, c := newTestEngine(t, a)
	if err := store.Save(State{ExpiresAt: c.t.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(store.path)
	if ok, err := e.Extend(context.Background(), 30*time.Minute); ok || err != nil {
		t.Fatalf("ok %v err %v", ok, err)
	}
	after, _ := os.ReadFile(store.path)
	if string(before) != string(after) || a.enabled != 0 {
		t.Errorf("changed: %s -> %s, enabled %d", before, after, a.enabled)
	}
}

// liveEngine returns an engine whose session is active with deadline at.
func liveEngine(t *testing.T, at time.Duration) (*Engine, *Store, *clock, *fakeSwitch) {
	t.Helper()
	a := &fakeSwitch{id: "a", savedVal: "1"}
	e, store, c := newTestEngine(t, a)
	if err := e.Enable(context.Background(), at); err != nil {
		t.Fatal(err)
	}
	return e, store, c, a
}

func TestExtendAfterDeadlineIsNoOp(t *testing.T) {
	for _, past := range []time.Duration{0, time.Second, time.Hour} {
		e, store, c, a := liveEngine(t, 10*time.Minute)
		c.t = c.t.Add(10*time.Minute + past)
		before, _ := os.ReadFile(store.path)
		if ok, err := e.Extend(context.Background(), 30*time.Minute); ok || err != nil {
			t.Fatalf("past %v: ok %v err %v", past, ok, err)
		}
		after, _ := os.ReadFile(store.path)
		if string(before) != string(after) || a.enabled != 1 || a.disabled != 0 {
			t.Errorf("past %v: state changed or switches called", past)
		}
	}
}

func TestExtendWithoutDeadlineIsNoOp(t *testing.T) {
	e, _, _, _ := liveEngine(t, 0)
	if ok, err := e.Extend(context.Background(), 30*time.Minute); ok || err != nil {
		t.Fatalf("ok %v err %v", ok, err)
	}
	if st, _ := e.Status(); !st.Active || !st.ExpiresAt.IsZero() {
		t.Errorf("state %+v", st)
	}
}

func TestExtendMovesDeadlineLater(t *testing.T) {
	e, _, c, a := liveEngine(t, 30*time.Minute)
	c.t = c.t.Add(20 * time.Minute) // 10 min left
	ok, err := e.Extend(context.Background(), 30*time.Minute)
	if !ok || err != nil {
		t.Fatalf("ok %v err %v", ok, err)
	}
	st, _ := e.Status()
	if !st.Active || !st.ExpiresAt.Equal(c.t.Add(30*time.Minute)) {
		t.Errorf("state %+v want deadline %v", st, c.t.Add(30*time.Minute))
	}
	if a.enabled != 1 || a.disabled != 0 || st.Saved["a"] != "1" {
		t.Errorf("switches touched: enabled %d disabled %d saved %v", a.enabled, a.disabled, st.Saved)
	}
}

func TestExtendNeverShortens(t *testing.T) {
	e, _, c, _ := liveEngine(t, 2*time.Hour) // on --for 2h
	want := c.t.Add(2 * time.Hour)
	c.t = c.t.Add(20 * time.Minute)
	// The session is live, so Extend succeeds, but the later deadline stays.
	if ok, err := e.Extend(context.Background(), 30*time.Minute); !ok || err != nil {
		t.Fatalf("ok %v err %v", ok, err)
	}
	st, _ := e.Status()
	if !st.Active || !st.ExpiresAt.Equal(want) {
		t.Errorf("deadline %v want %v", st.ExpiresAt, want)
	}
}

func TestExtendNonPositiveTimeoutIsNoOp(t *testing.T) {
	for _, d := range []time.Duration{0, -time.Minute} {
		e, _, c, _ := liveEngine(t, 10*time.Minute)
		want := c.t.Add(10 * time.Minute)
		if ok, err := e.Extend(context.Background(), d); ok || err != nil {
			t.Fatalf("timeout %v: ok %v err %v", d, ok, err)
		}
		st, _ := e.Status()
		if !st.Active || !st.ExpiresAt.Equal(want) {
			t.Errorf("timeout %v: state %+v", d, st)
		}
	}
}

func TestExtendSerializesWithDisable(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	c := &clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	if err := store.Save(State{Active: true, Saved: map[string]string{"a": "orig"}, ExpiresAt: c.t.Add(10 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	a := newBlocking("a", "")
	b := &fakeSwitch{id: "a", savedVal: "new"}
	ea := NewEngine([]Switch{a}, store, c.now)
	eb := NewEngine([]Switch{b}, store, c.now)

	doneA := make(chan error, 1)
	go func() { doneA <- ea.Disable(context.Background()) }()
	<-a.entered
	// Status is lock-free and still reports the session as live here.
	if st, _ := eb.Status(); !st.Active {
		t.Fatal("precondition: state should still read active during Disable")
	}

	doneB := make(chan error, 1)
	var extended bool
	go func() { var err error; extended, err = eb.Extend(context.Background(), 30*time.Minute); doneB <- err }()
	mustBlock(t, doneB)

	close(a.release)
	if err := waitDone(t, doneA); err != nil {
		t.Fatal(err)
	}
	if err := waitDone(t, doneB); err != nil {
		t.Fatal(err)
	}
	if extended || b.enabled != 0 {
		t.Fatalf("extended %v enabled %d", extended, b.enabled)
	}
	st, _ := store.Load()
	if st.Active || !st.ExpiresAt.IsZero() {
		t.Fatalf("session resurrected: %+v", st)
	}
}

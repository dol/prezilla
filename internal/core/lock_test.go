//go:build unix

package core

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// blockingSwitch signals entered when Enable/Disable starts and waits for release.
type blockingSwitch struct {
	id       string
	entered  chan struct{}
	release  chan struct{}
	savedVal string
}

func (b *blockingSwitch) ID() string { return b.id }
func (b *blockingSwitch) Enable(context.Context) (string, error) {
	close(b.entered)
	<-b.release
	return b.savedVal, nil
}
func (b *blockingSwitch) Disable(context.Context, string) error {
	close(b.entered)
	<-b.release
	return nil
}

func newBlocking(id, saved string) *blockingSwitch {
	return &blockingSwitch{id: id, savedVal: saved, entered: make(chan struct{}), release: make(chan struct{})}
}

// mustBlock asserts done does not fire for a short while.
func mustBlock(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("second operation did not wait for the lock (err=%v)", err)
	case <-time.After(150 * time.Millisecond):
	}
}

func waitDone(t *testing.T, done <-chan error) error {
	t.Helper()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("second operation never finished")
		return nil
	}
}

func TestConcurrentEnableSerializesAndKeepsOriginals(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	a := newBlocking("a", "orig")
	b := &fakeSwitch{id: "a", savedVal: "presentation-value"}
	ea := NewEngine([]Switch{a}, store, time.Now)
	eb := NewEngine([]Switch{b}, store, time.Now)

	doneA := make(chan error, 1)
	go func() { doneA <- ea.Enable(context.Background(), 0) }()
	<-a.entered

	doneB := make(chan error, 1)
	go func() { doneB <- eb.Enable(context.Background(), 0) }()
	mustBlock(t, doneB)

	close(a.release)
	if err := waitDone(t, doneA); err != nil {
		t.Fatal(err)
	}
	if err := waitDone(t, doneB); err != nil {
		t.Fatal(err)
	}
	if b.enabled != 0 {
		t.Errorf("second Enable called switch %d times", b.enabled)
	}
	st, _ := store.Load()
	if !st.Active || st.Saved["a"] != "orig" {
		t.Fatalf("state = %+v", st)
	}
}

func TestConcurrentToggleSerializes(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	a := newBlocking("a", "orig")
	b := &fakeSwitch{id: "a", savedVal: "presentation-value"}
	ea := NewEngine([]Switch{a}, store, time.Now)
	eb := NewEngine([]Switch{b}, store, time.Now)

	doneA := make(chan error, 1)
	go func() { _, err := ea.Toggle(context.Background(), 0); doneA <- err }()
	<-a.entered

	doneB := make(chan error, 1)
	var onB bool
	go func() { var err error; onB, err = eb.Toggle(context.Background(), 0); doneB <- err }()
	mustBlock(t, doneB)

	close(a.release)
	if err := waitDone(t, doneA); err != nil {
		t.Fatal(err)
	}
	if err := waitDone(t, doneB); err != nil {
		t.Fatal(err)
	}
	// B saw A's result (active) and toggled off, restoring A's original.
	if onB || b.enabled != 0 || b.disabled != 1 || b.gotSaved != "orig" {
		t.Fatalf("onB=%v enabled=%d disabled=%d got=%q", onB, b.enabled, b.disabled, b.gotSaved)
	}
}

func TestConcurrentDisableIfExpiredSerializes(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	c := &clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	if err := store.Save(State{Active: true, Saved: map[string]string{"a": "orig"}, ExpiresAt: c.t.Add(-time.Minute)}); err != nil {
		t.Fatal(err)
	}
	a := newBlocking("a", "")
	b := &fakeSwitch{id: "a"}
	ea := NewEngine([]Switch{a}, store, c.now)
	eb := NewEngine([]Switch{b}, store, c.now)

	doneA := make(chan error, 1)
	go func() { _, err := ea.DisableIfExpired(context.Background()); doneA <- err }()
	<-a.entered

	doneB := make(chan error, 1)
	var firedB bool
	go func() { var err error; firedB, err = eb.DisableIfExpired(context.Background()); doneB <- err }()
	mustBlock(t, doneB)

	close(a.release)
	if err := waitDone(t, doneA); err != nil {
		t.Fatal(err)
	}
	if err := waitDone(t, doneB); err != nil {
		t.Fatal(err)
	}
	if firedB || b.disabled != 0 {
		t.Fatalf("second DisableIfExpired fired=%v disabled=%d", firedB, b.disabled)
	}
	st, _ := store.Load()
	if st.Active {
		t.Fatalf("state = %+v", st)
	}
}

func TestLockTimeoutErrorNamesLockPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "state.json")
	store := NewStore(p)
	store.lockTimeout = 100 * time.Millisecond
	unlock, err := store.Lock()
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	a := &fakeSwitch{id: "a"}
	e := NewEngine([]Switch{a}, store, time.Now)
	err = e.Enable(context.Background(), 0)
	if err == nil || !strings.Contains(err.Error(), p+".lock") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "waiting for another Prezilla process") {
		t.Fatalf("err = %v, want the display name Prezilla", err)
	}
	if a.enabled != 0 {
		t.Fatal("switch called without the lock")
	}
	if _, err := e.Toggle(context.Background(), 0); err == nil {
		t.Fatal("Toggle: want lock error")
	}
	if err := e.Disable(context.Background()); err == nil {
		t.Fatal("Disable: want lock error")
	}
	if _, err := e.DisableIfExpired(context.Background()); err == nil {
		t.Fatal("DisableIfExpired: want lock error")
	}
}

func TestLockReleasedAfterUnlock(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	store.lockTimeout = 100 * time.Millisecond
	unlock, err := store.Lock()
	if err != nil {
		t.Fatal(err)
	}
	unlock()
	unlock2, err := store.Lock()
	if err != nil {
		t.Fatalf("relock: %v", err)
	}
	unlock2()
}

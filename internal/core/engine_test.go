package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeSwitch struct {
	id                    string
	enableErr, disableErr error
	enabled, disabled     int
	savedVal              string
	gotSaved              string
	log                   *[]string
}

func (f *fakeSwitch) ID() string { return f.id }
func (f *fakeSwitch) Enable(ctx context.Context) (string, error) {
	f.enabled++
	if f.enableErr != nil {
		return "", f.enableErr
	}
	return f.savedVal, nil
}
func (f *fakeSwitch) Disable(ctx context.Context, saved string) error {
	f.disabled++
	f.gotSaved = saved
	if f.log != nil {
		*f.log = append(*f.log, f.id)
	}
	return f.disableErr
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newTestEngine(t *testing.T, sw ...Switch) (*Engine, *Store, *clock) {
	t.Helper()
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	c := &clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	return NewEngine(sw, store, c.now), store, c
}

func TestEnableSavesEachSwitchValueAndMarksActive(t *testing.T) {
	a := &fakeSwitch{id: "a", savedVal: "1"}
	b := &fakeSwitch{id: "b", savedVal: "2"}
	e, _, _ := newTestEngine(t, a, b)
	if err := e.Enable(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	st, _ := e.Status()
	if !st.Active || st.Saved["a"] != "1" || st.Saved["b"] != "2" {
		t.Fatalf("state = %+v", st)
	}
}

func TestEnableTwiceDoesNotCallSwitchesAgainOrOverwriteSaved(t *testing.T) {
	a := &fakeSwitch{id: "a", savedVal: "1"}
	e, _, _ := newTestEngine(t, a)
	e.Enable(context.Background(), 0)
	a.savedVal = "changed"
	if err := e.Enable(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	st, _ := e.Status()
	if a.enabled != 1 || st.Saved["a"] != "1" {
		t.Fatalf("enabled=%d saved=%v", a.enabled, st.Saved)
	}
}

func TestEnableContinuesAfterFailureAndReportsSwitchID(t *testing.T) {
	a := &fakeSwitch{id: "a", savedVal: "1"}
	m := &fakeSwitch{id: "mute", enableErr: errors.New("boom")}
	c := &fakeSwitch{id: "c", savedVal: "3"}
	e, _, _ := newTestEngine(t, a, m, c)
	err := e.Enable(context.Background(), 0)
	if err == nil || !strings.Contains(err.Error(), "mute:") {
		t.Fatalf("err = %v", err)
	}
	st, _ := e.Status()
	if !st.Active || st.Saved["a"] != "1" || st.Saved["c"] != "3" {
		t.Fatalf("state = %+v", st)
	}
	if _, ok := st.Saved["mute"]; ok {
		t.Fatal("failed switch saved")
	}
}

func TestEnableAllFailLeavesInactive(t *testing.T) {
	a := &fakeSwitch{id: "a", enableErr: errors.New("x")}
	e, _, _ := newTestEngine(t, a)
	if err := e.Enable(context.Background(), time.Minute); err == nil {
		t.Fatal("want error")
	}
	st, _ := e.Status()
	if st.Active {
		t.Fatalf("state = %+v", st)
	}
}

func TestDisablePassesSavedValueBackInReverseOrder(t *testing.T) {
	var log []string
	a := &fakeSwitch{id: "a", savedVal: "1", log: &log}
	b := &fakeSwitch{id: "b", savedVal: "2", log: &log}
	e, _, _ := newTestEngine(t, a, b)
	e.Enable(context.Background(), 0)
	if err := e.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Join(log, ",") != "b,a" || a.gotSaved != "1" || b.gotSaved != "2" {
		t.Fatalf("log=%v a=%q b=%q", log, a.gotSaved, b.gotSaved)
	}
	st, _ := e.Status()
	if st.Active || len(st.Saved) != 0 {
		t.Fatalf("state = %+v", st)
	}
}

func TestDisableFailureKeepsEntryAndStaysActive(t *testing.T) {
	a := &fakeSwitch{id: "a", savedVal: "1"}
	b := &fakeSwitch{id: "b", savedVal: "2", disableErr: errors.New("nope")}
	e, _, _ := newTestEngine(t, a, b)
	e.Enable(context.Background(), 0)
	err := e.Disable(context.Background())
	if err == nil || !strings.Contains(err.Error(), "b:") {
		t.Fatalf("err = %v", err)
	}
	st, _ := e.Status()
	if !st.Active || st.Saved["b"] != "2" {
		t.Fatalf("state = %+v", st)
	}
	if _, ok := st.Saved["a"]; ok {
		t.Fatal("a should be restored")
	}
	b.disableErr = nil
	if err := e.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	st, _ = e.Status()
	if st.Active || len(st.Saved) != 0 {
		t.Fatalf("state = %+v", st)
	}
}

func TestDisableUnknownSwitchIDIsDroppedAndReported(t *testing.T) {
	a := &fakeSwitch{id: "a", savedVal: "1"}
	e, store, _ := newTestEngine(t, a)
	store.Save(State{Active: true, Saved: map[string]string{"a": "1", "gone": "x"}})
	err := e.Disable(context.Background())
	if err == nil || !strings.Contains(err.Error(), "gone: no configured switch to restore") {
		t.Fatalf("err = %v", err)
	}
	st, _ := e.Status()
	if _, ok := st.Saved["gone"]; ok || st.Active || len(st.Saved) != 0 {
		t.Fatalf("state = %+v", st)
	}
	if err := e.Disable(context.Background()); err != nil {
		t.Fatalf("second Disable = %v", err)
	}
}

func TestEnableSaveFailureRollsBackSwitches(t *testing.T) {
	dir := t.TempDir()
	ro := filepath.Join(dir, "ro")
	if err := os.Mkdir(ro, 0o700); err != nil {
		t.Fatal(err)
	}
	// The lock file must exist beforehand so only Save fails in the read-only dir.
	if err := os.WriteFile(filepath.Join(ro, "state.json.lock"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(ro, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(ro, 0o700) })
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	a := &fakeSwitch{id: "a", savedVal: "1"}
	b := &fakeSwitch{id: "b", savedVal: "2"}
	bad := NewStore(filepath.Join(ro, "state.json"))
	err := NewEngine([]Switch{a, b}, bad, time.Now).Enable(context.Background(), 0)
	if err == nil || !strings.Contains(err.Error(), "save state") {
		t.Fatalf("err = %v", err)
	}
	if a.disabled != 1 || a.gotSaved != "1" || b.disabled != 1 || b.gotSaved != "2" {
		t.Fatalf("a=%d/%q b=%d/%q", a.disabled, a.gotSaved, b.disabled, b.gotSaved)
	}
	good := NewStore(filepath.Join(dir, "state.json"))
	st, _ := NewEngine([]Switch{a, b}, good, time.Now).Status()
	if st.Active {
		t.Fatalf("state = %+v", st)
	}
}

func TestEnableWithNullStateFileDoesNotPanic(t *testing.T) {
	a := &fakeSwitch{id: "a", savedVal: "1"}
	e, store, _ := newTestEngine(t, a)
	store.Save(State{})
	if err := e.Enable(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
}

func TestDisableWhenInactiveIsNoop(t *testing.T) {
	a := &fakeSwitch{id: "a"}
	e, _, _ := newTestEngine(t, a)
	if err := e.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.disabled != 0 {
		t.Fatal("switch called")
	}
}

func TestToggleReturnsNewState(t *testing.T) {
	a := &fakeSwitch{id: "a"}
	e, _, _ := newTestEngine(t, a)
	on, err := e.Toggle(context.Background(), 0)
	if err != nil || !on {
		t.Fatalf("on=%v err=%v", on, err)
	}
	on, err = e.Toggle(context.Background(), 0)
	if err != nil || on {
		t.Fatalf("on=%v err=%v", on, err)
	}
}

func TestStateSurvivesNewEngineSameStore(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	a1 := &fakeSwitch{id: "a", savedVal: "orig"}
	NewEngine([]Switch{a1}, store, time.Now).Enable(context.Background(), 0)
	a2 := &fakeSwitch{id: "a"}
	if err := NewEngine([]Switch{a2}, store, time.Now).Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a2.disabled != 1 || a2.gotSaved != "orig" {
		t.Fatalf("disabled=%d got=%q", a2.disabled, a2.gotSaved)
	}
}

func TestEnableSetsExpiresAtFromClock(t *testing.T) {
	e, _, c := newTestEngine(t, &fakeSwitch{id: "a"})
	e.Enable(context.Background(), time.Hour)
	st, _ := e.Status()
	if !st.ExpiresAt.Equal(c.t.Add(time.Hour)) {
		t.Fatalf("ExpiresAt = %v", st.ExpiresAt)
	}
}

func TestEnableZeroTimeoutHasNoDeadline(t *testing.T) {
	e, _, _ := newTestEngine(t, &fakeSwitch{id: "a"})
	e.Enable(context.Background(), 0)
	st, _ := e.Status()
	if !st.ExpiresAt.IsZero() {
		t.Fatalf("ExpiresAt = %v", st.ExpiresAt)
	}
}

func TestEnableWhileActiveOnlyMovesDeadline(t *testing.T) {
	a := &fakeSwitch{id: "a", savedVal: "1"}
	e, _, c := newTestEngine(t, a)
	e.Enable(context.Background(), time.Hour)
	c.t = c.t.Add(10 * time.Minute)
	e.Enable(context.Background(), 2*time.Hour)
	st, _ := e.Status()
	if a.enabled != 1 || st.Saved["a"] != "1" || !st.ExpiresAt.Equal(c.t.Add(2*time.Hour)) {
		t.Fatalf("enabled=%d state=%+v", a.enabled, st)
	}
}

func TestDisableIfExpiredBeforeDeadlineDoesNothing(t *testing.T) {
	a := &fakeSwitch{id: "a"}
	e, _, c := newTestEngine(t, a)
	e.Enable(context.Background(), time.Hour)
	c.t = c.t.Add(59 * time.Minute)
	d, err := e.DisableIfExpired(context.Background())
	if err != nil || d || a.disabled != 0 {
		t.Fatalf("d=%v err=%v", d, err)
	}
}

func TestDisableIfExpiredAfterJumpPastDeadlineRestores(t *testing.T) {
	a := &fakeSwitch{id: "a", savedVal: "1"}
	e, _, c := newTestEngine(t, a)
	e.Enable(context.Background(), time.Hour)
	c.t = c.t.Add(5 * time.Hour)
	d, err := e.DisableIfExpired(context.Background())
	if err != nil || !d || a.disabled != 1 {
		t.Fatalf("d=%v err=%v", d, err)
	}
	st, _ := e.Status()
	if st.Active {
		t.Fatal("still active")
	}
}

func TestDisableIfExpiredWithoutDeadlineNeverFires(t *testing.T) {
	a := &fakeSwitch{id: "a"}
	e, _, c := newTestEngine(t, a)
	e.Enable(context.Background(), 0)
	c.t = c.t.Add(1000 * time.Hour)
	d, err := e.DisableIfExpired(context.Background())
	if err != nil || d || a.disabled != 0 {
		t.Fatalf("d=%v err=%v", d, err)
	}
}

func TestDisableClearsExpiresAt(t *testing.T) {
	e, _, _ := newTestEngine(t, &fakeSwitch{id: "a"})
	e.Enable(context.Background(), time.Hour)
	e.Disable(context.Background())
	st, _ := e.Status()
	if !st.ExpiresAt.IsZero() {
		t.Fatalf("ExpiresAt = %v", st.ExpiresAt)
	}
}

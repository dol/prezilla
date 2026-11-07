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

func TestDisableUsesRestorerForSavedSwitchNoLongerConfigured(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	a1 := &fakeSwitch{id: "a", savedVal: "A"}
	b1 := &fakeSwitch{id: "b", savedVal: "B"}
	if err := NewEngine([]Switch{a1, b1}, store, time.Now).Enable(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Load()
	st.Saved["gone"] = "x"
	store.Save(st)

	var log []string
	a2 := &fakeSwitch{id: "a", log: &log}
	b2 := &fakeSwitch{id: "b", log: &log}
	unusedA := &fakeSwitch{id: "a", log: &log}
	e := NewEngine([]Switch{a2}, store, time.Now)
	e.SetRestorers([]Switch{unusedA, b2})
	err := e.Disable(context.Background())
	if err == nil || !strings.Contains(err.Error(), "gone: no configured switch to restore") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), "b:") {
		t.Fatalf("b reported as orphan: %v", err)
	}
	if a2.gotSaved != "A" || b2.disabled != 1 || b2.gotSaved != "B" || unusedA.disabled != 0 {
		t.Fatalf("a2=%q b2=%d/%q unusedA=%d", a2.gotSaved, b2.disabled, b2.gotSaved, unusedA.disabled)
	}
	st, _ = store.Load()
	if st.Active || len(st.Saved) != 0 {
		t.Fatalf("state = %+v", st)
	}
}

func TestRestorerFailureKeepsEntry(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "state.json"))
	store.Save(State{Active: true, Saved: map[string]string{"b": "B"}})
	b := &fakeSwitch{id: "b", disableErr: errors.New("nope")}
	e := NewEngine(nil, store, time.Now)
	e.SetRestorers([]Switch{b})
	if err := e.Disable(context.Background()); err == nil || !strings.Contains(err.Error(), "b: nope") {
		t.Fatalf("err = %v", err)
	}
	st, _ := store.Load()
	if !st.Active || st.Saved["b"] != "B" {
		t.Fatalf("state = %+v", st)
	}
}

// activate puts the mode on with the given saved values and deadline.
func activate(t *testing.T, store *Store, saved map[string]string, exp time.Time) {
	t.Helper()
	if err := store.Save(State{Active: true, Saved: saved, ExpiresAt: exp}); err != nil {
		t.Fatal(err)
	}
}

func TestEnableOneAppliesAndSavesWhenActive(t *testing.T) {
	a := &fakeSwitch{id: "a", savedVal: "1"}
	e, store, _ := newTestEngine(t, a)
	activate(t, store, map[string]string{"x": "0"}, time.Time{})
	ok, err := e.EnableOne(context.Background(), a)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	st, _ := e.Status()
	if !st.Active || st.Saved["a"] != "1" || st.Saved["x"] != "0" {
		t.Fatalf("state = %+v", st)
	}
}

func TestEnableOneInactiveDoesNothing(t *testing.T) {
	a := &fakeSwitch{id: "a", savedVal: "1"}
	e, _, _ := newTestEngine(t, a)
	ok, err := e.EnableOne(context.Background(), a)
	if err != nil || ok || a.enabled != 0 {
		t.Fatalf("ok=%v err=%v enabled=%d", ok, err, a.enabled)
	}
	if st, _ := e.Status(); st.Active || len(st.Saved) != 0 {
		t.Fatalf("state = %+v", st)
	}
}

func TestEnableOneAlreadySavedIsNoop(t *testing.T) {
	a := &fakeSwitch{id: "a", savedVal: "new"}
	e, store, _ := newTestEngine(t, a)
	activate(t, store, map[string]string{"a": "orig"}, time.Time{})
	ok, err := e.EnableOne(context.Background(), a)
	if err != nil || !ok || a.enabled != 0 {
		t.Fatalf("ok=%v err=%v enabled=%d", ok, err, a.enabled)
	}
	if st, _ := e.Status(); st.Saved["a"] != "orig" {
		t.Fatalf("state = %+v", st)
	}
}

func TestEnableOneFailureSavesNothing(t *testing.T) {
	a := &fakeSwitch{id: "a", enableErr: errors.New("boom")}
	e, store, _ := newTestEngine(t, a)
	activate(t, store, map[string]string{"x": "0"}, time.Time{})
	ok, err := e.EnableOne(context.Background(), a)
	if err == nil || !strings.Contains(err.Error(), "a:") || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	st, _ := e.Status()
	if _, has := st.Saved["a"]; has || !st.Active || len(st.Saved) != 1 {
		t.Fatalf("state = %+v", st)
	}
}

func TestEnableOneSaveFailureRollsBack(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	activate(t, NewStore(path), map[string]string{"x": "0"}, time.Time{})
	// The lock file must exist beforehand so only Save fails in the read-only dir.
	if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o700) })
	a := &fakeSwitch{id: "a", savedVal: "1"}
	ok, err := NewEngine([]Switch{a}, NewStore(path), time.Now).EnableOne(context.Background(), a)
	if err == nil || !strings.Contains(err.Error(), "save state") || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if a.disabled != 1 || a.gotSaved != "1" {
		t.Fatalf("disabled=%d saved=%q", a.disabled, a.gotSaved)
	}
}

func TestEnableOneKeepsDeadline(t *testing.T) {
	a := &fakeSwitch{id: "a", savedVal: "1"}
	e, store, c := newTestEngine(t, a)
	exp := c.t.Add(time.Hour)
	activate(t, store, map[string]string{"x": "0"}, exp)
	if _, err := e.EnableOne(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.Status(); !st.ExpiresAt.Equal(exp) {
		t.Fatalf("ExpiresAt = %v, want %v", st.ExpiresAt, exp)
	}
}

func TestDisableOneRestoresAndRemovesSaved(t *testing.T) {
	a := &fakeSwitch{id: "a"}
	b := &fakeSwitch{id: "b"}
	e, store, _ := newTestEngine(t, a, b)
	activate(t, store, map[string]string{"a": "1", "b": "2"}, time.Time{})
	ok, err := e.DisableOne(context.Background(), "a")
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	st, _ := e.Status()
	if a.disabled != 1 || a.gotSaved != "1" || b.disabled != 0 || !st.Active || st.Saved["b"] != "2" {
		t.Fatalf("a=%d/%q b=%d state=%+v", a.disabled, a.gotSaved, b.disabled, st)
	}
	if _, has := st.Saved["a"]; has {
		t.Fatalf("a still saved: %+v", st)
	}
}

func TestDisableOneFallsBackToRestorers(t *testing.T) {
	r := &fakeSwitch{id: "r"}
	e, store, _ := newTestEngine(t)
	e.SetRestorers([]Switch{r})
	activate(t, store, map[string]string{"r": "9"}, time.Time{})
	ok, err := e.DisableOne(context.Background(), "r")
	if err != nil || !ok || r.gotSaved != "9" {
		t.Fatalf("ok=%v err=%v saved=%q", ok, err, r.gotSaved)
	}
}

func TestDisableOneFailureKeepsSaved(t *testing.T) {
	a := &fakeSwitch{id: "a", disableErr: errors.New("boom")}
	e, store, _ := newTestEngine(t, a)
	activate(t, store, map[string]string{"a": "1"}, time.Time{})
	ok, err := e.DisableOne(context.Background(), "a")
	if err == nil || !strings.Contains(err.Error(), "a:") || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if st, _ := e.Status(); st.Saved["a"] != "1" || !st.Active {
		t.Fatalf("state = %+v", st)
	}
}

func TestDisableOneLastSwitchKeepsModeActive(t *testing.T) {
	a := &fakeSwitch{id: "a"}
	e, store, c := newTestEngine(t, a)
	exp := c.t.Add(time.Hour)
	activate(t, store, map[string]string{"a": "1"}, exp)
	if _, err := e.DisableOne(context.Background(), "a"); err != nil {
		t.Fatal(err)
	}
	st, _ := e.Status()
	if !st.Active || len(st.Saved) != 0 || !st.ExpiresAt.Equal(exp) {
		t.Fatalf("state = %+v", st)
	}
	if err := e.Disable(context.Background()); err != nil {
		t.Fatal(err)
	}
	if st, _ := e.Status(); st.Active {
		t.Fatalf("state = %+v", st)
	}
}

func TestDisableOneUnknownIDIsNoop(t *testing.T) {
	a := &fakeSwitch{id: "a"}
	e, store, _ := newTestEngine(t, a)
	activate(t, store, map[string]string{"a": "1"}, time.Time{})
	ok, err := e.DisableOne(context.Background(), "zzz")
	if err != nil || !ok || a.disabled != 0 {
		t.Fatalf("ok=%v err=%v disabled=%d", ok, err, a.disabled)
	}
	if st, _ := e.Status(); st.Saved["a"] != "1" {
		t.Fatalf("state = %+v", st)
	}
}

func TestDisableOneSavedWithoutSwitchReportsError(t *testing.T) {
	e, store, _ := newTestEngine(t)
	activate(t, store, map[string]string{"gone": "1"}, time.Time{})
	_, err := e.DisableOne(context.Background(), "gone")
	if err == nil || !strings.Contains(err.Error(), "gone") {
		t.Fatalf("err = %v", err)
	}
}

func TestDisableOneInactiveDoesNothing(t *testing.T) {
	a := &fakeSwitch{id: "a"}
	e, _, _ := newTestEngine(t, a)
	ok, err := e.DisableOne(context.Background(), "a")
	if err != nil || ok || a.disabled != 0 {
		t.Fatalf("ok=%v err=%v disabled=%d", ok, err, a.disabled)
	}
}

func TestSetSwitchesChangesNextEnable(t *testing.T) {
	a := &fakeSwitch{id: "a", savedVal: "1"}
	b := &fakeSwitch{id: "b", savedVal: "2"}
	e, _, _ := newTestEngine(t, a)
	e.SetSwitches([]Switch{b})
	if err := e.Enable(context.Background(), 0); err != nil {
		t.Fatal(err)
	}
	if a.enabled != 0 || b.enabled != 1 {
		t.Fatalf("a=%d b=%d", a.enabled, b.enabled)
	}
}

func TestEnableWithoutSwitchesReturnsErrNoSwitchesAndSavesNothing(t *testing.T) {
	e, store, _ := newTestEngine(t)
	if err := e.Enable(context.Background(), time.Hour); !errors.Is(err, ErrNoSwitches) {
		t.Fatalf("err = %v, want ErrNoSwitches", err)
	}
	if _, err := os.Stat(store.path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("state file written (stat err %v)", err)
	}
	if _, err := e.Toggle(context.Background(), time.Hour); !errors.Is(err, ErrNoSwitches) {
		t.Fatalf("Toggle err = %v, want ErrNoSwitches", err)
	}
}

func TestEnableWithoutSwitchesWhileActiveOnlyMovesDeadline(t *testing.T) {
	e, store, c := newTestEngine(t)
	activate(t, store, map[string]string{"a": "1"}, time.Time{})
	if err := e.Enable(context.Background(), time.Hour); err != nil {
		t.Fatal(err)
	}
	st, _ := store.Load()
	if !st.Active || !st.ExpiresAt.Equal(c.now().Add(time.Hour)) {
		t.Fatalf("state = %+v", st)
	}
}

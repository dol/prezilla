package cli

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"prezilla/internal/core"
)

type fakeSwitch struct {
	id      string
	enabled bool
	err     error
}

func (f *fakeSwitch) ID() string { return f.id }
func (f *fakeSwitch) Enable(context.Context) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	f.enabled = true
	return "orig", nil
}
func (f *fakeSwitch) Disable(context.Context, string) error { f.enabled = false; return nil }

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

type harness struct {
	sw      *fakeSwitch
	clk     *clock
	d       Deps
	spawned int
	tray    int
	out     bytes.Buffer
	errOut  bytes.Buffer
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{sw: &fakeSwitch{id: "icons"}, clk: &clock{t: time.Date(2026, 1, 1, 10, 0, 0, 0, time.Local)}}
	store := core.NewStore(filepath.Join(t.TempDir(), "state.json"))
	h.d = Deps{
		Engine:         core.NewEngine([]core.Switch{h.sw}, store, h.clk.now),
		Tray:           func(context.Context) error { h.tray++; return nil },
		SpawnWatcher:   func() error { h.spawned++; return nil },
		DefaultTimeout: 30 * time.Minute,
		PollInterval:   time.Millisecond,
		Version:        "1.2.3",
	}
	return h
}

func (h *harness) run(args ...string) int {
	h.out.Reset()
	h.errOut.Reset()
	return Run(context.Background(), args, &h.out, &h.errOut, h.d)
}

func (h *harness) state(t *testing.T) core.State {
	t.Helper()
	st, err := h.d.Engine.Status()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestRunOnEnablesAndExitsZero(t *testing.T) {
	h := newHarness(t)
	if c := h.run("on"); c != 0 {
		t.Fatalf("code %d: %s", c, h.errOut.String())
	}
	if !h.sw.enabled || !h.state(t).Active {
		t.Error("not enabled")
	}
}

func TestRunOffRestores(t *testing.T) {
	h := newHarness(t)
	h.run("on")
	if c := h.run("off"); c != 0 {
		t.Fatalf("code %d", c)
	}
	if h.sw.enabled || h.state(t).Active {
		t.Error("not restored")
	}
}

func TestRunToggle(t *testing.T) {
	h := newHarness(t)
	h.run("toggle")
	if !h.sw.enabled {
		t.Fatal("toggle did not enable")
	}
	h.run("toggle")
	if h.sw.enabled {
		t.Fatal("toggle did not disable")
	}
}

func TestRunStatusPrintsActiveOrInactive(t *testing.T) {
	h := newHarness(t)
	h.run("status")
	if h.out.String() != "inactive\n" {
		t.Errorf("got %q", h.out.String())
	}
	h.run("on", "--for", "0")
	h.run("status")
	if h.out.String() != "active\n" {
		t.Errorf("got %q", h.out.String())
	}
}

func TestRunUnknownCommandExits2WithUsage(t *testing.T) {
	h := newHarness(t)
	if c := h.run("bogus"); c != 2 {
		t.Errorf("code %d", c)
	}
	if !strings.Contains(h.errOut.String(), "usage") {
		t.Errorf("stderr %q", h.errOut.String())
	}
}

func TestRunNoArgsExits2(t *testing.T) {
	h := newHarness(t)
	if c := h.run(); c != 2 {
		t.Errorf("code %d", c)
	}
}

func TestRunVersionPrintsVersion(t *testing.T) {
	h := newHarness(t)
	if c := h.run("version"); c != 0 || h.out.String() != "1.2.3\n" {
		t.Errorf("code %d out %q", c, h.out.String())
	}
}

func TestRunEngineErrorExits1AndPrintsToStderr(t *testing.T) {
	h := newHarness(t)
	h.sw.err = errors.New("boom")
	if c := h.run("on"); c != 1 {
		t.Errorf("code %d", c)
	}
	// An unmapped error keeps its original text.
	if got := h.errOut.String(); got != "icons: boom\n" {
		t.Errorf("stderr %q", got)
	}
	if h.out.Len() != 0 {
		t.Errorf("stdout %q", h.out.String())
	}
}

func TestRunMappedEngineErrorPrintsExplainedText(t *testing.T) {
	h := newHarness(t)
	h.sw.err = errors.New("xfconf: connect: dial unix /run/user/1000/bus: no such file")
	if c := h.run("on"); c != 1 {
		t.Errorf("code %d", c)
	}
	want := `Couldn't change desktop icons: XFCE's settings service (xfconfd) isn't reachable. Run Prezilla inside an XFCE session, or remove "icons" from "switches" in config.json.` +
		" (details: icons: xfconf: connect: dial unix /run/user/1000/bus: no such file)\n"
	if got := h.errOut.String(); got != want {
		t.Errorf("stderr %q\nwant %q", got, want)
	}
	if h.out.Len() != 0 {
		t.Errorf("stdout %q", h.out.String())
	}
}

func TestRunOnWithoutSwitchesExplainsAndExits1(t *testing.T) {
	h := newHarness(t)
	h.d.Engine = core.NewEngine(nil, core.NewStore(filepath.Join(t.TempDir(), "state.json")), h.clk.now)
	if c := h.run("on"); c != 1 {
		t.Errorf("code %d", c)
	}
	want := `No switch is ticked, so presentation mode would change nothing. Tick at least one switch in the tray menu, or add one to "switches" in config.json.` +
		" (details: no switches configured)\n"
	if got := h.errOut.String(); got != want {
		t.Errorf("stderr %q\nwant %q", got, want)
	}
	if h.spawned != 0 {
		t.Errorf("watcher spawned %d times", h.spawned)
	}
}

func TestRunTrayCallsDepsTray(t *testing.T) {
	h := newHarness(t)
	if c := h.run("tray"); c != 0 || h.tray != 1 {
		t.Errorf("code %d tray %d", c, h.tray)
	}
	h.d.Tray = func(context.Context) error { return errors.New("no host") }
	if c := h.run("tray"); c != 1 || !strings.Contains(h.errOut.String(), "no host") {
		t.Errorf("code %d stderr %q", c, h.errOut.String())
	}
}

func TestRunOnUsesDefaultTimeout(t *testing.T) {
	h := newHarness(t)
	h.run("on")
	if got, want := h.state(t).ExpiresAt, h.clk.now().Add(30*time.Minute); !got.Equal(want) {
		t.Errorf("expires %v want %v", got, want)
	}
}

func TestRunOnForOverridesTimeout(t *testing.T) {
	h := newHarness(t)
	h.run("on", "--for", "45m")
	if got, want := h.state(t).ExpiresAt, h.clk.now().Add(45*time.Minute); !got.Equal(want) {
		t.Errorf("expires %v want %v", got, want)
	}
}

func TestRunOnForZeroMeansNoDeadline(t *testing.T) {
	h := newHarness(t)
	h.run("on", "--for", "0")
	if !h.state(t).ExpiresAt.IsZero() {
		t.Error("deadline set")
	}
}

func TestRunOnForInvalidDurationExits2(t *testing.T) {
	h := newHarness(t)
	if c := h.run("on", "--for", "soon"); c != 2 {
		t.Errorf("code %d", c)
	}
	if h.errOut.Len() == 0 || h.sw.enabled {
		t.Error("expected usage message and no enable")
	}
}

func TestRunOnSpawnsWatcherOnlyWhenDeadlineSet(t *testing.T) {
	h := newHarness(t)
	h.run("on", "--for", "0")
	if h.spawned != 0 {
		t.Errorf("spawned %d without deadline", h.spawned)
	}
	h.run("on", "--for", "5m")
	if h.spawned != 1 {
		t.Errorf("spawned %d with deadline", h.spawned)
	}
}

func TestRunStatusShowsUntilTime(t *testing.T) {
	h := newHarness(t)
	h.run("on", "--for", "45m")
	h.run("status")
	if h.out.String() != "active until 10:45\n" {
		t.Errorf("got %q", h.out.String())
	}
}

func TestRunStatusExpiredRestoresAndPrintsInactive(t *testing.T) {
	h := newHarness(t)
	h.run("on", "--for", "5m")
	h.clk.advance(time.Hour)
	h.run("status")
	if h.out.String() != "inactive\n" || h.sw.enabled {
		t.Errorf("out %q enabled %v", h.out.String(), h.sw.enabled)
	}
}

func TestRunToggleAfterExpiryTurnsOn(t *testing.T) {
	h := newHarness(t)
	h.run("on", "--for", "5m")
	h.clk.advance(time.Hour)
	h.run("toggle")
	if !h.sw.enabled || !h.state(t).Active {
		t.Error("expected on after expired toggle")
	}
}

func TestRunWatchDisablesAfterDeadlineWithFakeClock(t *testing.T) {
	h := newHarness(t)
	h.run("on", "--for", "5m")
	go func() {
		time.Sleep(20 * time.Millisecond)
		h.clk.advance(time.Hour)
	}()
	done := make(chan int, 1)
	go func() { done <- Run(context.Background(), []string{"watch"}, &bytes.Buffer{}, &bytes.Buffer{}, h.d) }()
	select {
	case c := <-done:
		if c != 0 {
			t.Errorf("code %d", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not finish")
	}
	if h.state(t).Active {
		t.Error("still active")
	}
}

func TestRunWatchExitsWhenAlreadyInactive(t *testing.T) {
	h := newHarness(t)
	if c := h.run("watch"); c != 0 {
		t.Errorf("code %d", c)
	}
}

type flakyDisable struct {
	fakeSwitch
	failures int
	attempts int
}

func (f *flakyDisable) Disable(ctx context.Context, saved string) error {
	f.attempts++
	if f.attempts <= f.failures {
		return errors.New("restore failed")
	}
	return f.fakeSwitch.Disable(ctx, saved)
}

func TestRunWatchRetriesAfterPartialDisableFailure(t *testing.T) {
	h := newHarness(t)
	sw := &flakyDisable{fakeSwitch: fakeSwitch{id: "icons"}, failures: 2}
	store := core.NewStore(filepath.Join(t.TempDir(), "state.json"))
	h.d.Engine = core.NewEngine([]core.Switch{sw}, store, h.clk.now)
	h.run("on", "--for", "5m")
	h.clk.advance(time.Hour)
	done := make(chan int, 1)
	go func() { done <- Run(context.Background(), []string{"watch"}, &bytes.Buffer{}, &bytes.Buffer{}, h.d) }()
	select {
	case c := <-done:
		if c != 0 {
			t.Errorf("code %d", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch did not finish")
	}
	if h.state(t).Active {
		t.Error("still active")
	}
	if sw.attempts != 3 {
		t.Errorf("attempts %d, want 3", sw.attempts)
	}
}

func TestRunTrayRestoresExpiredStateBeforeTrayEvenIfTrayFails(t *testing.T) {
	h := newHarness(t)
	h.run("on", "--for", "5m")
	h.spawned = 0
	h.clk.advance(time.Hour)
	var activeAtTray, enabledAtTray bool
	h.d.Tray = func(context.Context) error {
		st, _ := h.d.Engine.Status()
		activeAtTray, enabledAtTray = st.Active, h.sw.enabled
		return errors.New("no host")
	}
	if c := h.run("tray"); c != 1 {
		t.Errorf("code %d", c)
	}
	if activeAtTray || enabledAtTray {
		t.Errorf("not restored before tray: active=%v enabled=%v", activeAtTray, enabledAtTray)
	}
	if h.spawned != 0 {
		t.Errorf("spawned %d after expiry restore", h.spawned)
	}
}

func TestRunTraySpawnsWatcherWhenActiveWithDeadline(t *testing.T) {
	h := newHarness(t)
	h.run("on", "--for", "5m")
	h.spawned = 0
	h.d.Tray = func(context.Context) error {
		if h.spawned != 1 {
			t.Errorf("watcher not spawned before tray (spawned=%d)", h.spawned)
		}
		return errors.New("no host")
	}
	h.run("tray")
	if h.spawned != 1 {
		t.Errorf("spawned %d, want 1", h.spawned)
	}
}

func TestRunTraySpawnsNothingWhenInactiveOrNoDeadline(t *testing.T) {
	h := newHarness(t)
	h.run("tray")
	h.run("on", "--for", "0")
	h.run("tray")
	if h.spawned != 0 {
		t.Errorf("spawned %d", h.spawned)
	}
}

func TestRunTrayExpiryErrorIsPrintedAndTrayStillRuns(t *testing.T) {
	h := newHarness(t)
	sw := &flakyDisable{fakeSwitch: fakeSwitch{id: "icons"}, failures: 1}
	h.d.Engine = core.NewEngine([]core.Switch{sw}, core.NewStore(filepath.Join(t.TempDir(), "state.json")), h.clk.now)
	h.run("on", "--for", "5m")
	h.clk.advance(time.Hour)
	if c := h.run("tray"); c != 0 || h.tray != 1 {
		t.Errorf("code %d tray %d", c, h.tray)
	}
	if !strings.Contains(h.errOut.String(), "restore failed") {
		t.Errorf("stderr %q", h.errOut.String())
	}
}

type ctxSwitch struct {
	enableErr, disableErr []error
	enableDeadline        []time.Time
}

func (c *ctxSwitch) ID() string { return "icons" }
func (c *ctxSwitch) Enable(ctx context.Context) (string, error) {
	c.enableErr = append(c.enableErr, ctx.Err())
	dl, _ := ctx.Deadline()
	c.enableDeadline = append(c.enableDeadline, dl)
	return "orig", nil
}
func (c *ctxSwitch) Disable(ctx context.Context, _ string) error {
	c.disableErr = append(c.disableErr, ctx.Err())
	return nil
}

func TestRunEngineCommandsIgnoreParentCancellation(t *testing.T) {
	h := newHarness(t)
	sw := &ctxSwitch{}
	h.d.Engine = core.NewEngine([]core.Switch{sw}, core.NewStore(filepath.Join(t.TempDir(), "state.json")), h.clk.now)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, args := range [][]string{{"on", "--for", "0"}, {"off"}, {"toggle", "--for", "0"}, {"toggle"}} {
		if c := Run(cancelled, args, &h.out, &h.errOut, h.d); c != 0 {
			t.Fatalf("%v: code %d: %s", args, c, h.errOut.String())
		}
	}
	if len(sw.enableErr) != 2 || len(sw.disableErr) != 2 {
		t.Fatalf("enable %v disable %v", sw.enableErr, sw.disableErr)
	}
	for _, err := range append(sw.enableErr, sw.disableErr...) {
		if err != nil {
			t.Errorf("switch saw cancelled ctx: %v", err)
		}
	}
}

func TestRunOnBoundsEngineContext(t *testing.T) {
	h := newHarness(t)
	sw := &ctxSwitch{}
	h.d.Engine = core.NewEngine([]core.Switch{sw}, core.NewStore(filepath.Join(t.TempDir(), "state.json")), h.clk.now)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	before := time.Now()
	if c := Run(cancelled, []string{"on", "--for", "0"}, &h.out, &h.errOut, h.d); c != 0 {
		t.Fatalf("code %d: %s", c, h.errOut.String())
	}
	if len(sw.enableDeadline) != 1 || sw.enableErr[0] != nil {
		t.Fatalf("deadlines %v errs %v", sw.enableDeadline, sw.enableErr)
	}
	dl := sw.enableDeadline[0]
	if dl.IsZero() || !dl.After(before) || dl.After(time.Now().Add(core.OperationTimeout)) {
		t.Errorf("deadline %v not within (now, now+%s]", dl, core.OperationTimeout)
	}
}

func TestRunAboutPrintsTextAndExitsZero(t *testing.T) {
	var out, errOut bytes.Buffer
	d := Deps{Version: "1.2.3"} // no Engine: about must not touch it
	if c := Run(context.Background(), []string{"about", "extra"}, &out, &errOut, d); c != 0 {
		t.Fatalf("exit %d, stderr %q", c, errOut.String())
	}
	want := "Prezilla 1.2.3\nPresentation mode switcher\nLicense: Apache-2.0\nWebsite: https://github.com/dol/prezilla\n"
	if out.String() != want || errOut.Len() != 0 {
		t.Fatalf("out %q err %q", out.String(), errOut.String())
	}
}

func TestUsageKeepsLowercaseCommandName(t *testing.T) {
	if !strings.HasPrefix(usage, "usage: prezilla <on ") {
		t.Fatalf("usage = %q", usage)
	}
	h := newHarness(t)
	if c := h.run("bogus"); c != 2 {
		t.Fatalf("code %d", c)
	}
	if !strings.Contains(h.errOut.String(), "usage: prezilla <") {
		t.Errorf("stderr %q", h.errOut.String())
	}
}

func TestRunOnWatcherFailurePrintsSentenceAndDetailsExit1(t *testing.T) {
	h := newHarness(t)
	h.d.SpawnWatcher = func() error { return errors.New("fork/exec /x: resource temporarily unavailable") }
	if c := h.run("on"); c != 1 {
		t.Errorf("code %d", c)
	}
	want := `Couldn't start the background timer, so presentation mode might not end by itself. Run "prezilla off" when you are done.` +
		" (details: start watcher: fork/exec /x: resource temporarily unavailable)\n"
	if got := h.errOut.String(); got != want {
		t.Errorf("stderr %q\nwant %q", got, want)
	}
}

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
	if !strings.Contains(h.errOut.String(), "icons") || !strings.Contains(h.errOut.String(), "boom") {
		t.Errorf("stderr %q", h.errOut.String())
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

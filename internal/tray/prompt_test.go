package tray

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"prezilla/internal/about"
	"prezilla/internal/core"
	"prezilla/internal/system"
)

func TestShouldPromptTable(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	live := core.State{Active: true, ExpiresAt: now.Add(10 * time.Minute)}
	cases := []struct {
		name   string
		active bool
		st     core.State
		want   bool
	}{
		{"screensaver ended", false, live, false},
		{"mode inactive", true, core.State{ExpiresAt: now.Add(time.Minute)}, false},
		{"no deadline", true, core.State{Active: true}, false},
		{"deadline passed", true, core.State{Active: true, ExpiresAt: now.Add(-time.Second)}, false},
		{"exactly at deadline", true, core.State{Active: true, ExpiresAt: now}, false},
		{"valid", true, live, true},
	}
	for _, c := range cases {
		if got := shouldPrompt(c.active, c.st, now); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

func TestTimerLabelTable(t *testing.T) {
	zone := time.FixedZone("X", 3*3600)
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, zone)
	cases := []struct {
		name    string
		expires time.Time
		want    string
	}{
		{"zero", time.Time{}, ""},
		{"past", now.Add(-time.Minute), ""},
		{"now", now, ""},
		{"12 min", now.Add(12 * time.Minute), "Ends 12:12 · 12 min left"},
		{"under a minute", now.Add(20 * time.Second), "Ends 12:00 · 1 min left"},
		{"local zone formatting", time.Date(2026, 1, 1, 9, 45, 0, 0, time.UTC), "Ends 09:45 · 45 min left"},
	}
	for _, c := range cases {
		if got := timerLabel(now, c.expires); got != c.want {
			t.Errorf("%s: got %q want %q", c.name, got, c.want)
		}
	}
	// The label uses the deadline's own zone: callers pass it in local time.
	if got := timerLabel(now, time.Date(2026, 1, 1, 14, 5, 0, 0, zone)); got != "Ends 14:05 · 125 min left" {
		t.Errorf("fixed zone: %q", got)
	}
}

func TestPromptRequestStrings(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	p := promptRequest(about.New("1"), core.State{Active: true, ExpiresAt: now.Add(11*time.Minute + time.Second)}, now)
	want := system.Prompt{
		Summary:  "Presentation still running",
		Body:     "Screensaver started with 12 min left. Continue presenting?",
		IconName: "prezilla",
		Actions:  []system.PromptAction{{Key: "continue", Label: "Continue presenting"}, {Key: "end", Label: "End presentation"}},
	}
	if !reflect.DeepEqual(p, want) {
		t.Errorf("got %+v\nwant %+v", p, want)
	}
}

// fakeEngine is the coordinator's view of core.Engine.
type fakeEngine struct {
	mu       sync.Mutex
	st       core.State
	stErr    error
	enables  []time.Duration
	disables int
	opErr    error
	ctxs     []context.Context
	gate     chan struct{} // when set, engine calls wait for it after recording
	// endsFirst simulates a Disable that holds the lock while Status still
	// reads active: Extend then finds the session ended.
	endsFirst bool
	spawns    int
}

// record notes one engine call and waits for gate, if any.
func (f *fakeEngine) record(ctx context.Context, enable bool) {
	f.mu.Lock()
	if enable {
		f.enables = append(f.enables, testTimeout)
	} else {
		f.disables++
	}
	f.ctxs = append(f.ctxs, ctx)
	gate := f.gate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
}

func (f *fakeEngine) status() (core.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st, f.stErr
}

func (f *fakeEngine) set(st core.State) {
	f.mu.Lock()
	f.st = st
	f.mu.Unlock()
}

func (f *fakeEngine) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.enables), f.disables
}

const testTimeout = 30 * time.Minute

type coordFixture struct {
	c       *promptCoordinator
	eng     *fakeEngine
	p       *system.FakePrompter
	ss      *system.FakeScreenSaver
	now     time.Time
	changed chan struct{}
	cancel  context.CancelFunc
	ctx     context.Context
	done    chan error
}

func newFixture(t *testing.T, p *system.FakePrompter) *coordFixture {
	t.Helper()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	eng := &fakeEngine{st: core.State{Active: true, ExpiresAt: now.Add(10 * time.Minute)}}
	f := &coordFixture{eng: eng, p: p, ss: &system.FakeScreenSaver{}, now: now, changed: make(chan struct{}, 64)}
	var prompter system.Prompter
	if p != nil {
		prompter = p
	}
	f.c = newPromptCoordinator(promptDeps{
		prompter: prompter,
		info:     about.New("1"),
		status:   eng.status,
		extend: func(ctx context.Context) (bool, error) {
			eng.record(ctx, true)
			eng.mu.Lock()
			defer eng.mu.Unlock()
			if eng.endsFirst {
				eng.st = core.State{}
			}
			if eng.opErr != nil {
				return false, eng.opErr
			}
			// Like Engine.Extend: never turns a session on.
			if !eng.st.Active || eng.st.ExpiresAt.IsZero() || !now.Before(eng.st.ExpiresAt) {
				return false, nil
			}
			if next := now.Add(testTimeout); next.After(eng.st.ExpiresAt) {
				eng.st.ExpiresAt = next
			}
			return true, nil
		},
		disable: func(ctx context.Context) error {
			eng.record(ctx, false)
			eng.mu.Lock()
			defer eng.mu.Unlock()
			if eng.opErr == nil {
				eng.st = core.State{}
			}
			return eng.opErr
		},
		now:     func() time.Time { return now },
		changed: func() { f.changed <- struct{}{} },
	})
	f.ctx, f.cancel = context.WithCancel(context.Background())
	f.done = make(chan error, 1)
	go func() { f.done <- f.c.watch(f.ctx, f.ss) }()
	t.Cleanup(func() {
		f.cancel()
		select {
		case <-f.done:
		case <-time.After(2 * time.Second):
			t.Error("watch did not stop")
		}
		f.c.wait()
	})
	return f
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func blockingPrompter() *system.FakePrompter {
	return &system.FakePrompter{Answer: make(chan string)}
}

func TestCoordinatorOnePromptPerActivation(t *testing.T) {
	p := blockingPrompter()
	f := newFixture(t, p)
	f.ss.Emit(true)
	eventually(t, "prompt", func() bool { return len(p.Prompts()) == 1 && f.c.pending() })
	f.ss.Emit(true)
	f.ss.Emit(false) // screensaver ended: the prompt stays
	f.ss.Emit(true)
	time.Sleep(50 * time.Millisecond)
	if n := len(p.Prompts()); n != 1 || !f.c.pending() {
		t.Fatalf("prompts %d pending %v", n, f.c.pending())
	}
	if got := p.Prompts()[0].Summary; got != "Presentation still running" {
		t.Errorf("summary %q", got)
	}
	p.Answer <- ""
	eventually(t, "resolved", func() bool { return !f.c.pending() })
	f.ss.Emit(false)
	f.ss.Emit(true)
	eventually(t, "second prompt", func() bool { return len(p.Prompts()) == 2 })
}

func TestCoordinatorNoPromptWithoutLiveDeadline(t *testing.T) {
	p := blockingPrompter()
	f := newFixture(t, p)
	f.eng.set(core.State{Active: true})
	f.ss.Emit(true)
	f.eng.set(core.State{})
	f.ss.Emit(false)
	f.ss.Emit(true)
	time.Sleep(50 * time.Millisecond)
	if n := len(p.Prompts()); n != 0 || f.c.pending() {
		t.Errorf("prompts %d pending %v", n, f.c.pending())
	}
}

func TestCoordinatorContinueMovesDeadline(t *testing.T) {
	p := blockingPrompter()
	f := newFixture(t, p)
	gate := make(chan struct{})
	f.eng.gate = gate
	f.ss.Emit(true)
	eventually(t, "prompt", f.c.pending)
	p.Answer <- "continue"
	eventually(t, "enable", func() bool { e, _ := f.eng.counts(); return e == 1 })
	defer close(gate)
	if _, d := f.eng.counts(); d != 0 {
		t.Errorf("disables %d", d)
	}
	f.eng.mu.Lock()
	ctx := f.eng.ctxs[0]
	f.eng.mu.Unlock()
	if dl, ok := ctx.Deadline(); !ok || time.Until(dl) > core.OperationTimeout {
		t.Errorf("engine ctx deadline %v %v", dl, ok)
	}
	f.cancel() // Quit must not cut a running engine call
	if ctx.Err() != nil {
		t.Errorf("engine ctx cancelled with tray ctx")
	}
	if f.c.pending() {
		t.Error("still pending")
	}
}

func TestCoordinatorEndDisables(t *testing.T) {
	p := blockingPrompter()
	f := newFixture(t, p)
	f.ss.Emit(true)
	eventually(t, "prompt", f.c.pending)
	p.Answer <- "end"
	eventually(t, "disable", func() bool { _, d := f.eng.counts(); return d == 1 })
	if e, _ := f.eng.counts(); e != 0 {
		t.Errorf("enables %d", e)
	}
}

func TestCoordinatorDismissedChangesNothing(t *testing.T) {
	p := blockingPrompter()
	f := newFixture(t, p)
	f.ss.Emit(true)
	eventually(t, "prompt", f.c.pending)
	p.Answer <- ""
	eventually(t, "resolved", func() bool { return !f.c.pending() })
	time.Sleep(20 * time.Millisecond)
	if e, d := f.eng.counts(); e != 0 || d != 0 {
		t.Errorf("enables %d disables %d", e, d)
	}
}

func TestCoordinatorUnknownKeyChangesNothing(t *testing.T) {
	p := blockingPrompter()
	f := newFixture(t, p)
	f.ss.Emit(true)
	eventually(t, "prompt", f.c.pending)
	p.Answer <- "default"
	eventually(t, "resolved", func() bool { return !f.c.pending() })
	time.Sleep(20 * time.Millisecond)
	if e, d := f.eng.counts(); e != 0 || d != 0 {
		t.Errorf("enables %d disables %d", e, d)
	}
}

func TestCoordinatorAnswerAfterSessionEndedDoesNothing(t *testing.T) {
	p := blockingPrompter()
	f := newFixture(t, p)
	f.ss.Emit(true)
	eventually(t, "prompt", f.c.pending)
	f.eng.set(core.State{}) // ended elsewhere (CLI, watcher), poll not run yet
	p.Answer <- "continue"
	eventually(t, "resolved", func() bool { return !f.c.pending() })
	time.Sleep(20 * time.Millisecond)
	if e, d := f.eng.counts(); e != 0 || d != 0 {
		t.Errorf("resurrected: enables %d disables %d", e, d)
	}
}

func TestCoordinatorMenuAnswerCancelsPrompt(t *testing.T) {
	p := blockingPrompter()
	f := newFixture(t, p)
	f.ss.Emit(true)
	eventually(t, "prompt", f.c.pending)
	f.c.menuAnswer(f.ctx, "end")
	eventually(t, "prompt cancelled", func() bool { return p.Cancelled() == 1 })
	eventually(t, "disable", func() bool { _, d := f.eng.counts(); return d == 1 })
	if f.c.pending() || f.c.errText() != "" {
		t.Errorf("pending %v err %q", f.c.pending(), f.c.errText())
	}
	if e, _ := f.eng.counts(); e != 0 {
		t.Errorf("enables %d", e)
	}
}

func TestCoordinatorNotificationAnswerWinsOverMenu(t *testing.T) {
	p := blockingPrompter()
	f := newFixture(t, p)
	f.ss.Emit(true)
	eventually(t, "prompt", f.c.pending)
	p.Answer <- "continue"
	eventually(t, "enable", func() bool { e, _ := f.eng.counts(); return e == 1 })
	f.c.menuAnswer(f.ctx, "end") // late menu click: ignored
	time.Sleep(20 * time.Millisecond)
	if e, d := f.eng.counts(); e != 1 || d != 0 {
		t.Errorf("enables %d disables %d", e, d)
	}
}

func TestCoordinatorMenuAnswerWithoutPromptIgnored(t *testing.T) {
	f := newFixture(t, blockingPrompter())
	f.c.menuAnswer(f.ctx, "end")
	time.Sleep(20 * time.Millisecond)
	if e, d := f.eng.counts(); e != 0 || d != 0 {
		t.Errorf("enables %d disables %d", e, d)
	}
}

func TestCoordinatorExpiryCancelsPrompt(t *testing.T) {
	p := blockingPrompter()
	f := newFixture(t, p)
	f.ss.Emit(true)
	eventually(t, "prompt", f.c.pending)
	f.c.check(core.State{Active: true, ExpiresAt: f.now.Add(time.Minute)}, f.now)
	if !f.c.pending() {
		t.Fatal("live session cancelled the prompt")
	}
	f.c.check(core.State{Active: true, ExpiresAt: f.now}, f.now)
	if f.c.pending() {
		t.Error("expired session kept the prompt")
	}
	eventually(t, "prompt cancelled", func() bool { return p.Cancelled() == 1 })
	if f.c.errText() != "" {
		t.Errorf("cancellation surfaced as error %q", f.c.errText())
	}
	if e, d := f.eng.counts(); e != 0 || d != 0 {
		t.Errorf("enables %d disables %d", e, d)
	}
}

func TestCoordinatorModeEndedCancelsPrompt(t *testing.T) {
	p := blockingPrompter()
	f := newFixture(t, p)
	f.ss.Emit(true)
	eventually(t, "prompt", f.c.pending)
	f.c.check(core.State{}, f.now)
	eventually(t, "prompt cancelled", func() bool { return p.Cancelled() == 1 && !f.c.pending() })
}

func TestCoordinatorPrompterErrorKeepsMenuFallback(t *testing.T) {
	p := &system.FakePrompter{Err: errors.New("notify: server does not support actions")}
	f := newFixture(t, p)
	f.ss.Emit(true)
	eventually(t, "error", func() bool { return f.c.errText() != "" })
	if got := f.c.errText(); got != "notify: server does not support actions" {
		t.Errorf("err %q", got)
	}
	if !f.c.pending() {
		t.Fatal("menu fallback gone after prompter error")
	}
	select {
	case err := <-f.done:
		t.Fatalf("watch exited: %v", err)
	default:
	}
	f.c.menuAnswer(f.ctx, "continue")
	eventually(t, "enable", func() bool { e, _ := f.eng.counts(); return e == 1 })
	if f.c.errText() != "" {
		t.Errorf("successful answer kept error %q", f.c.errText())
	}
}

func TestCoordinatorEngineErrorSurfaced(t *testing.T) {
	p := blockingPrompter()
	f := newFixture(t, p)
	f.eng.opErr = errors.New("mute: boom")
	f.ss.Emit(true)
	eventually(t, "prompt", f.c.pending)
	p.Answer <- "end"
	eventually(t, "error", func() bool { return strings.Contains(f.c.errText(), "mute: boom") })
}

func TestCoordinatorNilPrompterUsesMenuOnly(t *testing.T) {
	f := newFixture(t, nil)
	f.ss.Emit(true)
	eventually(t, "pending", f.c.pending)
	if f.c.errText() != "" {
		t.Errorf("err %q", f.c.errText())
	}
	f.c.menuAnswer(f.ctx, "end")
	eventually(t, "disable", func() bool { _, d := f.eng.counts(); return d == 1 })
}

func TestCoordinatorQuitCancelsPrompt(t *testing.T) {
	p := blockingPrompter()
	f := newFixture(t, p)
	f.ss.Emit(true)
	eventually(t, "prompt", f.c.pending)
	f.cancel()
	eventually(t, "prompt cancelled", func() bool { return p.Cancelled() == 1 })
	f.c.wait()
	if f.c.errText() != "" {
		t.Errorf("quit surfaced error %q", f.c.errText())
	}
	if e, d := f.eng.counts(); e != 0 || d != 0 {
		t.Errorf("enables %d disables %d", e, d)
	}
}

func TestCoordinatorWatchErrorReturned(t *testing.T) {
	c := newPromptCoordinator(promptDeps{})
	ss := &system.FakeScreenSaver{Err: errors.New("screensaver: connect: nope")}
	if err := c.watch(context.Background(), ss); err == nil || !strings.HasPrefix(err.Error(), "screensaver:") {
		t.Errorf("err %v", err)
	}
	if err := c.watch(context.Background(), nil); err != nil {
		t.Errorf("nil screensaver: %v", err)
	}
}

func TestCoordinatorChangedCalled(t *testing.T) {
	p := blockingPrompter()
	f := newFixture(t, p)
	f.ss.Emit(true)
	select {
	case <-f.changed:
	case <-time.After(2 * time.Second):
		t.Fatal("changed not called on prompt")
	}
	p.Answer <- ""
	select {
	case <-f.changed:
	case <-time.After(2 * time.Second):
		t.Fatal("changed not called on answer")
	}
}

func TestCoordinatorContinueAfterConcurrentEndDoesNotResurrect(t *testing.T) {
	p := blockingPrompter()
	f := newFixture(t, p)
	f.eng.endsFirst = true // Status still says active; Extend sees it ended
	f.ss.Emit(true)
	eventually(t, "prompt", f.c.pending)
	p.Answer <- "continue"
	eventually(t, "extend", func() bool { e, _ := f.eng.counts(); return e == 1 })
	time.Sleep(20 * time.Millisecond)
	st, _ := f.eng.status()
	if st.Active || !st.ExpiresAt.IsZero() {
		t.Errorf("session resurrected: %+v", st)
	}
	f.eng.mu.Lock()
	spawns := f.eng.spawns
	f.eng.mu.Unlock()
	if _, d := f.eng.counts(); d != 0 || spawns != 0 {
		t.Errorf("disables %d spawns %d", d, spawns)
	}
	if f.c.errText() != "" {
		t.Errorf("err %q", f.c.errText())
	}
}

func TestContinueStep(t *testing.T) {
	cases := []struct{ cfg, want time.Duration }{
		{0, 30 * time.Minute},
		{-time.Minute, 30 * time.Minute},
		{45 * time.Minute, 45 * time.Minute},
		{2 * time.Hour, 2 * time.Hour},
	}
	for _, c := range cases {
		if got := continueStep(c.cfg); got != c.want {
			t.Errorf("continueStep(%v) = %v want %v", c.cfg, got, c.want)
		}
	}
}

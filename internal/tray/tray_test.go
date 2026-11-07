package tray

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"prezilla/internal/about"
	"prezilla/internal/core"
	"prezilla/internal/system"
	"prezilla/internal/tray/icons"
	"prezilla/internal/usererr"
)

func TestLabelsActiveAndInactive(t *testing.T) {
	if tip := Labels(true); tip != "Prezilla: presentation mode on" {
		t.Errorf("active: %q", tip)
	}
	if tip := Labels(false); tip != "Prezilla: presentation mode off" {
		t.Errorf("inactive: %q", tip)
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
		{now.Add(30 * time.Second), "1\u00a0min left"},
		{now.Add(time.Minute), "1\u00a0min left"},
		{now.Add(90 * time.Second), "2\u00a0min left"},
		{now.Add(3 * time.Minute), "3\u00a0min left"},
	}
	for _, c := range cases {
		if got := remainingText(now, c.expires); got != c.want {
			t.Errorf("expires %v: got %q want %q", c.expires.Sub(now), got, c.want)
		}
	}
}

func TestTooltip(t *testing.T) {
	cases := []struct {
		active    bool
		remaining string
		toggleErr string
		expiryErr string
		prompting bool
		want      string
	}{
		{false, "", "", "", false, "Prezilla: presentation mode off"},
		{true, "", "", "", false, "Prezilla: presentation mode on"},
		{true, "3 min left", "", "", false, "Prezilla: presentation mode on, 3 min left"},
		{false, "", "boom", "", false, "Prezilla: presentation mode off (error: boom)"},
		{true, "1 min left", "boom", "", false, "Prezilla: presentation mode on, 1 min left (error: boom)"},
		{true, "", "", "expired", false, "Prezilla: presentation mode on (error: expired)"},
		{true, "", "boom", "expired", false, "Prezilla: presentation mode on (error: boom; expired)"},
		{true, "3 min left", "", "", true, "Prezilla: presentation mode on, 3 min left\nScreensaver started. Keep presenting?"},
		{true, "3 min left", "boom", "", true, "Prezilla: presentation mode on, 3 min left (error: boom)\nScreensaver started. Keep presenting?"},
	}
	for _, c := range cases {
		if got := tooltip(c.active, c.remaining, c.toggleErr, c.expiryErr, c.prompting); got != c.want {
			t.Errorf("got %q want %q", got, c.want)
		}
	}
}

func TestTogglerGuardsDetachesAndWaits(t *testing.T) {
	var tg toggler
	parent, cancel := context.WithCancel(context.Background())
	started, release, got := make(chan struct{}), make(chan struct{}), make(chan context.Context, 1)
	if !tg.start(parent, func(ctx context.Context) {
		got <- ctx
		close(started)
		<-release
	}) {
		t.Fatal("first start refused")
	}
	<-started
	if tg.start(parent, func(context.Context) { t.Error("overlapping toggle ran") }) {
		t.Error("overlapping start accepted")
	}
	cancel()
	tctx := <-got
	if err := tctx.Err(); err != nil {
		t.Errorf("toggle ctx cancelled with parent: %v", err)
	}
	if dl, ok := tctx.Deadline(); !ok || time.Until(dl) <= 0 || time.Until(dl) > core.OperationTimeout {
		t.Errorf("toggle ctx deadline = %v, %v", dl, ok)
	}
	done := make(chan struct{})
	go func() { tg.wait(); close(done) }()
	select {
	case <-done:
		t.Fatal("wait returned while toggle running")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("wait did not return after toggle finished")
	}
	tg.wait() // idle: must not block
}

func TestSpawnIfDeadline(t *testing.T) {
	deadline := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name  string
		st    core.State
		stErr error
		want  int
	}{
		{"active with deadline", core.State{Active: true, ExpiresAt: deadline}, nil, 1},
		{"active without deadline", core.State{Active: true}, nil, 0},
		{"inactive", core.State{ExpiresAt: deadline}, nil, 0},
		{"status error", core.State{}, errors.New("read"), 0},
	}
	for _, c := range cases {
		n := 0
		err := spawnIfDeadline(func() (core.State, error) { return c.st, c.stErr }, func() error { n++; return nil })
		if n != c.want || err != nil {
			t.Errorf("%s: spawned %d err %v, want %d", c.name, n, err, c.want)
		}
	}
	err := spawnIfDeadline(func() (core.State, error) { return core.State{Active: true, ExpiresAt: deadline}, nil },
		func() error { return errors.New("exec failed") })
	if err == nil || !strings.Contains(err.Error(), "start watcher: exec failed") {
		t.Errorf("spawn error = %v", err)
	}
	if err := spawnIfDeadline(func() (core.State, error) { return core.State{Active: true, ExpiresAt: deadline}, nil }, nil); err != nil {
		t.Errorf("nil spawn: %v", err)
	}
}

func TestAboutMessage(t *testing.T) {
	info := about.New("1.2.3")
	s, b := aboutMessage(info)
	if s != "Prezilla 1.2.3" {
		t.Errorf("summary %q", s)
	}
	want := "Presentation mode switcher\nLicense: Apache-2.0\nhttps://github.com/dol/prezilla"
	if b != want {
		t.Errorf("body %q want %q", b, want)
	}
}

func TestAboutAction(t *testing.T) {
	info := about.New("1.2.3")
	n := &system.FakeNotifier{}
	var a actionState
	refreshed := 0
	done := func() { refreshed++ }
	a.run(context.Background(), aboutAction(n, info), done)
	if len(n.Calls) != 1 || n.Calls[0].Summary != "Prezilla 1.2.3" || n.Calls[0].Icon != "prezilla" {
		t.Errorf("notify calls %v", n.Calls)
	}
	if a.text() != "" || refreshed != 1 {
		t.Errorf("text %q refreshed %d", a.text(), refreshed)
	}

	n.Err = errors.New("no bus")
	a.run(context.Background(), aboutAction(n, info), done)
	if a.text() != "no bus" {
		t.Errorf("text %q", a.text())
	}
	if tip := tooltip(false, "", a.text(), "", false); tip != "Prezilla: presentation mode off (error: no bus)" {
		t.Errorf("tooltip %q", tip)
	}
	n.Err = nil
	a.run(context.Background(), aboutAction(n, info), done)
	if a.text() != "" {
		t.Errorf("success should clear, got %q", a.text())
	}
}

func TestJoinErrText(t *testing.T) {
	cases := [][3]string{{"", "", ""}, {"a", "", "a"}, {"", "b", "b"}, {"a", "b", "a; b"}}
	for _, c := range cases {
		if got := joinErrText(c[0], c[1]); got != c[2] {
			t.Errorf("%q+%q = %q", c[0], c[1], got)
		}
	}
}

func TestNilDependenciesYieldErrors(t *testing.T) {
	info := about.New("1")
	cases := []struct {
		fn   func(context.Context) error
		want string
	}{
		{aboutAction(nil, info), "about: no notifier configured"},
	}
	for _, c := range cases {
		var a actionState
		a.run(context.Background(), c.fn, func() {})
		if a.text() != c.want {
			t.Errorf("text %q want %q", a.text(), c.want)
		}
		if tip := tooltip(false, "", a.text(), "", false); !strings.Contains(tip, "(error: "+c.want+")") {
			t.Errorf("tooltip %q", tip)
		}
	}
}

// visibleTitles lists the titles of the visible conditional items and the
// checkbox in menu order.
func visibleTitles(m menuModel) []string {
	var out []string
	for _, it := range m.ordered() {
		if it.Visible {
			out = append(out, it.Title)
		}
	}
	return out
}

func TestMenuStateTable(t *testing.T) {
	const timer = "Ends 12:12 · 12\u00a0min left"
	const left = 12 * time.Minute
	cases := []struct {
		name        string
		active      bool
		timer       string
		remaining   time.Duration
		pending     bool
		step        time.Duration
		wantVisible []string
		wantChecked bool
	}{
		{"inactive", false, "", 0, false, testTimeout, []string{"Presentation mode"}, false},
		{"active without deadline", true, "", 0, false, testTimeout, []string{"Presentation mode"}, true},
		{"active with deadline", true, timer, left, false, testTimeout,
			[]string{"Presentation mode", timer, "Reset timer to 30 min"}, true},
		{"pending question hides reset", true, timer, left, true, testTimeout,
			[]string{"Keep presenting for 30 min", "Stop presenting", "Presentation mode", timer}, true},
		{"step 45", true, timer, left, true, 45 * time.Minute,
			[]string{"Keep presenting for 45 min", "Stop presenting", "Presentation mode", timer}, true},
		{"step 45 reset", true, timer, left, false, 45 * time.Minute,
			[]string{"Presentation mode", timer, "Reset timer to 45 min"}, true},
	}
	for _, c := range cases {
		m := menuState(c.active, c.timer, c.remaining, c.pending, c.step)
		if got := visibleTitles(m); !reflect.DeepEqual(got, c.wantVisible) {
			t.Errorf("%s: visible %q want %q", c.name, got, c.wantVisible)
		}
		if m.Mode.Checked != c.wantChecked {
			t.Errorf("%s: checked %v want %v", c.name, m.Mode.Checked, c.wantChecked)
		}
		if m.Mode.Title != "Presentation mode" || !m.Mode.Visible || !m.Mode.Enabled {
			t.Errorf("%s: checkbox %+v", c.name, m.Mode)
		}
		if m.Timer.Enabled {
			t.Errorf("%s: timer line enabled", c.name)
		}
		for _, it := range []itemState{m.Keep, m.Stop, m.Reset} {
			if it.Visible && !it.Enabled {
				t.Errorf("%s: %q visible but disabled", c.name, it.Title)
			}
		}
		for _, it := range []itemState{m.Keep, m.Stop, m.Mode, m.Reset} {
			if it.Checked && it.Title != "Presentation mode" {
				t.Errorf("%s: %q checked", c.name, it.Title)
			}
		}
	}
}

// The reset item is shown only when a click would change something: a
// deadline exists, less than step is left, and no question is open.
func TestMenuStateResetVisibility(t *testing.T) {
	const timer = "Ends 12:30 · 30\u00a0min left"
	cases := []struct {
		name      string
		timer     string
		remaining time.Duration
		pending   bool
		want      bool
	}{
		{"remaining < step", timer, testTimeout - time.Second, false, true},
		{"remaining == step", timer, testTimeout, false, false},
		{"remaining > step", timer, testTimeout + time.Minute, false, false},
		{"question pending", timer, time.Minute, true, false},
		{"no deadline", "", 0, false, false},
	}
	for _, c := range cases {
		if got := menuState(true, c.timer, c.remaining, c.pending, testTimeout).Reset.Visible; got != c.want {
			t.Errorf("%s: reset visible %v want %v", c.name, got, c.want)
		}
	}
}

func TestMenuStateCheckboxTitleNeverChanges(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, timer := range []string{"", "Ends 12:12 · 12\u00a0min left"} {
			for _, pending := range []bool{false, true} {
				if got := menuState(active, timer, time.Minute, pending, testTimeout).Mode.Title; got != modeTitle {
					t.Errorf("active %v timer %q pending %v: title %q", active, timer, pending, got)
				}
			}
		}
	}
	if modeTitle != "Presentation mode" || modeTooltip != "Turn presentation mode on or off" {
		t.Errorf("checkbox %q / %q", modeTitle, modeTooltip)
	}
}

func TestMenuStateTitlesFromStep(t *testing.T) {
	for _, c := range []struct {
		cfg                        time.Duration
		keep, reset, keepTip, rTip string
	}{
		{0, "Keep presenting for 30 min", "Reset timer to 30 min",
			"Keeps presentation mode on until 30 minutes from now.", "Sets the end time to 30 minutes from now."},
		{45 * time.Minute, "Keep presenting for 45 min", "Reset timer to 45 min",
			"Keeps presentation mode on until 45 minutes from now.", "Sets the end time to 45 minutes from now."},
	} {
		m := menuState(true, "x", time.Minute, true, continueStep(c.cfg))
		if m.Keep.Title != c.keep || m.Reset.Title != c.reset || m.Keep.Tooltip != c.keepTip || m.Reset.Tooltip != c.rTip {
			t.Errorf("cfg %v: keep %q/%q reset %q/%q", c.cfg, m.Keep.Title, m.Keep.Tooltip, m.Reset.Title, m.Reset.Tooltip)
		}
		if m.Stop.Title != "Stop presenting" {
			t.Errorf("stop %q", m.Stop.Title)
		}
		for _, it := range m.ordered() {
			if it.Tooltip == "" || !strings.HasSuffix(it.Tooltip, ".") && it.Title != modeTitle {
				t.Errorf("%q tooltip %q", it.Title, it.Tooltip)
			}
			if strings.Contains(it.Title, "+") || strings.Contains(it.Title, "Extend") || strings.Contains(it.Title, " by ") {
				t.Errorf("%q promises an addition", it.Title)
			}
		}
	}
}

// fakeClock is a settable clock for the toggle cooldown tests.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *fakeClock) add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func TestToggleCooldownDropsFastSecondClick(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	g := &clickGate{now: clk.now}
	var tg toggler
	runs := 0
	var mu sync.Mutex
	fn := func(context.Context) { mu.Lock(); runs++; mu.Unlock() }
	if !tryToggle(context.Background(), g, &tg, fn) {
		t.Fatal("first click refused")
	}
	tg.wait()
	clk.add(100 * time.Millisecond)
	if tryToggle(context.Background(), g, &tg, fn) {
		t.Error("click at +100ms accepted")
	}
	req := make(chan struct{}, 1)
	requestToggle(g, req)
	if len(req) != 0 {
		t.Error("click during cooldown was queued")
	}
	clk.add(500 * time.Millisecond) // +600ms
	requestToggle(g, req)
	if len(req) != 1 {
		t.Error("click after cooldown not queued")
	}
	if !tryToggle(context.Background(), g, &tg, fn) {
		t.Error("click at +600ms refused")
	}
	tg.wait()
	mu.Lock()
	defer mu.Unlock()
	if runs != 2 {
		t.Errorf("toggle ran %d times, want 2", runs)
	}
	if toggleCooldown != 500*time.Millisecond {
		t.Errorf("cooldown %v", toggleCooldown)
	}
}

func TestToggleCooldownStartsEvenWhenToggleFails(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	g := &clickGate{now: clk.now}
	var tg toggler
	var a actionState
	failing := func(ctx context.Context) {
		a.run(ctx, func(context.Context) error { return errors.New("mute: boom") }, func() {})
	}
	if !tryToggle(context.Background(), g, &tg, failing) {
		t.Fatal("first click refused")
	}
	tg.wait()
	if a.text() != "mute: boom" {
		t.Fatalf("toggle did not run: %q", a.text())
	}
	clk.add(100 * time.Millisecond)
	if tryToggle(context.Background(), g, &tg, failing) {
		t.Error("click after a failed toggle accepted within the cooldown")
	}
}

func TestToggleWhileRunningDroppedAndDoesNotRestartCooldown(t *testing.T) {
	clk := &fakeClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	g := &clickGate{now: clk.now}
	var tg toggler
	release := make(chan struct{})
	if !tryToggle(context.Background(), g, &tg, func(context.Context) { <-release }) {
		t.Fatal("first click refused")
	}
	clk.add(600 * time.Millisecond) // cooldown over, toggle still running
	if tryToggle(context.Background(), g, &tg, func(context.Context) { t.Error("overlapping toggle ran") }) {
		t.Error("click while running accepted")
	}
	close(release)
	tg.wait()
	clk.add(10 * time.Millisecond)
	ran := make(chan struct{})
	if !tryToggle(context.Background(), g, &tg, func(context.Context) { close(ran) }) {
		t.Error("dropped click restarted the cooldown")
	}
	<-ran
	tg.wait()
}

func TestStepMinutes(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want int
	}{{30 * time.Minute, 30}, {45 * time.Minute, 45}, {2 * time.Hour, 120}} {
		if got := stepMinutes(c.d); got != c.want {
			t.Errorf("stepMinutes(%v) = %d want %d", c.d, got, c.want)
		}
	}
}

func TestStartExtend(t *testing.T) {
	type call struct {
		ctx  context.Context
		step time.Duration
	}
	cases := []struct {
		name    string
		ok      bool
		err     error
		wantErr string
	}{
		{"extended", true, nil, ""},
		{"session ended: no-op", false, nil, ""},
		{"error", false, errors.New("state: locked"), "state: locked"},
	}
	for _, c := range cases {
		var xt toggler
		var a actionState
		calls := make(chan call, 4)
		done := make(chan struct{}, 4)
		parent, cancel := context.WithCancel(context.Background())
		extend := func(ctx context.Context, step time.Duration) (bool, error) {
			calls <- call{ctx, step}
			return c.ok, c.err
		}
		if !startExtend(parent, &xt, &a, extend, 45*time.Minute, func() { done <- struct{}{} }) {
			t.Fatalf("%s: not started", c.name)
		}
		got := <-calls
		cancel()
		<-done
		xt.wait()
		if got.step != 45*time.Minute {
			t.Errorf("%s: step %v", c.name, got.step)
		}
		if dl, ok := got.ctx.Deadline(); !ok || time.Until(dl) > core.OperationTimeout {
			t.Errorf("%s: ctx deadline %v %v", c.name, dl, ok)
		}
		if a.text() != c.wantErr {
			t.Errorf("%s: err text %q want %q", c.name, a.text(), c.wantErr)
		}
		if c.wantErr != "" {
			if tip := tooltip(true, "", a.text(), "", false); !strings.Contains(tip, "(error: "+c.wantErr+")") {
				t.Errorf("%s: tooltip %q", c.name, tip)
			}
		}
	}
}

func TestStartExtendIgnoresOverlappingClicks(t *testing.T) {
	var xt toggler
	var a actionState
	release := make(chan struct{})
	n := 0
	var mu sync.Mutex
	extend := func(ctx context.Context, _ time.Duration) (bool, error) {
		mu.Lock()
		n++
		mu.Unlock()
		<-release
		if ctx.Err() != nil {
			t.Errorf("extend ctx cancelled: %v", ctx.Err())
		}
		return true, nil
	}
	parent, cancel := context.WithCancel(context.Background())
	if !startExtend(parent, &xt, &a, extend, testTimeout, func() {}) {
		t.Fatal("first click refused")
	}
	if startExtend(parent, &xt, &a, extend, testTimeout, func() {}) {
		t.Error("overlapping click accepted")
	}
	cancel() // Quit must not cut the running call
	close(release)
	xt.wait()
	mu.Lock()
	defer mu.Unlock()
	if n != 1 {
		t.Errorf("extend ran %d times", n)
	}
	if !startExtend(context.Background(), &xt, &a, func(context.Context, time.Duration) (bool, error) { return true, nil }, testTimeout, func() {}) {
		t.Error("click after completion refused")
	}
	xt.wait()
}

// fakeWindow is a scripted system.AboutWindow; block, when set, is waited on
// (or ctx) before Show returns.
type fakeWindow struct {
	mu    sync.Mutex
	calls int
	err   error
	block chan struct{}
}

func (w *fakeWindow) Show(ctx context.Context, _ about.Info) error {
	w.mu.Lock()
	w.calls++
	block, err := w.block, w.err
	w.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (w *fakeWindow) n() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls
}

func newShower(w system.AboutWindow, n system.Notifier) (*aboutShower, *bytes.Buffer) {
	var log bytes.Buffer
	return &aboutShower{window: w, notifier: n, info: about.New("1.2.3"), stderr: &log}, &log
}

func TestAboutShowerWindowSuccessSendsNoNotification(t *testing.T) {
	w, n := &fakeWindow{}, &system.FakeNotifier{}
	s, log := newShower(w, n)
	if err := s.show(context.Background()); err != nil {
		t.Fatal(err)
	}
	if w.n() != 1 || len(n.Calls) != 0 || log.Len() != 0 {
		t.Errorf("window %d notify %v log %q", w.n(), n.Calls, log.String())
	}
}

func TestAboutShowerNoZenityFallsBackAndWarnsOnce(t *testing.T) {
	w := &fakeWindow{err: fmt.Errorf("%w: exec: not found", system.ErrNoZenity)}
	n := &system.FakeNotifier{}
	s, log := newShower(w, n)
	for i := 1; i <= 2; i++ {
		if err := s.show(context.Background()); err != nil {
			t.Fatal(err)
		}
		if len(n.Calls) != i || n.Calls[i-1].Summary != "Prezilla 1.2.3" ||
			!strings.Contains(n.Calls[i-1].Body, about.Website) {
			t.Fatalf("click %d: notify %v", i, n.Calls)
		}
	}
	if log.String() != "about: zenity not found; showing a notification instead\n" {
		t.Errorf("log %q", log.String())
	}
}

func TestAboutShowerRealFailureFallsBack(t *testing.T) {
	w := &fakeWindow{err: errors.New("about: zenity: exit status 2")}
	n := &system.FakeNotifier{}
	s, log := newShower(w, n)
	if err := s.show(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(n.Calls) != 1 || !strings.Contains(log.String(), "exit status 2") {
		t.Errorf("notify %v log %q", n.Calls, log.String())
	}
}

func TestAboutShowerNilWindowUsesNotification(t *testing.T) {
	n := &system.FakeNotifier{}
	s, log := newShower(nil, n)
	if err := s.show(context.Background()); err != nil || len(n.Calls) != 1 || log.Len() != 0 {
		t.Errorf("err %v notify %v log %q", err, n.Calls, log.String())
	}
}

func TestAboutShowerBothFailShowsError(t *testing.T) {
	w := &fakeWindow{err: fmt.Errorf("%w: exec: not found", system.ErrNoZenity)}
	n := &system.FakeNotifier{Err: errors.New("no bus")}
	s, _ := newShower(w, n)
	var a actionState
	a.run(context.Background(), s.show, func() {})
	if !strings.Contains(a.text(), "no bus") {
		t.Errorf("text %q", a.text())
	}
	s, _ = newShower(nil, nil)
	if err := s.show(context.Background()); err == nil || !strings.Contains(err.Error(), "no notifier configured") {
		t.Errorf("nil everything: %v", err)
	}
}

func TestAboutShowerCancelledIsSilent(t *testing.T) {
	w := &fakeWindow{block: make(chan struct{})}
	n := &system.FakeNotifier{}
	s, log := newShower(w, n)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.show(ctx); err != nil || len(n.Calls) != 0 || log.Len() != 0 {
		t.Errorf("err %v notify %v log %q", err, n.Calls, log.String())
	}
}

func TestAboutShowerSecondClickWhileOpenIgnored(t *testing.T) {
	w := &fakeWindow{block: make(chan struct{})}
	n := &system.FakeNotifier{}
	s, _ := newShower(w, n)
	var a actionState
	done := make(chan struct{}, 2)
	if !s.start(context.Background(), &a, func() { done <- struct{}{} }) {
		t.Fatal("first click not started")
	}
	if s.start(context.Background(), &a, func() { done <- struct{}{} }) {
		t.Error("second click started while the window is open")
	}
	close(w.block)
	<-done
	s.wait()
	if w.n() != 1 || len(n.Calls) != 0 {
		t.Errorf("window %d notify %v", w.n(), n.Calls)
	}
	if !s.start(context.Background(), &a, func() { done <- struct{}{} }) {
		t.Error("click after close not started")
	}
	<-done
	s.wait()
}

func TestAboutShowerQuitClosesOpenWindow(t *testing.T) {
	w := &fakeWindow{block: make(chan struct{})}
	s, _ := newShower(w, &system.FakeNotifier{})
	ctx, cancel := context.WithCancel(context.Background())
	var a actionState
	s.start(ctx, &a, func() {})
	cancel()
	s.wait() // returns only because the cancelled ctx closed the window
	if a.text() != "" {
		t.Errorf("text %q", a.text())
	}
}

func TestNoHostErrorKeepsLowercaseCommand(t *testing.T) {
	want := `use "prezilla toggle" from the command line`
	if !strings.Contains(errNoHost.Error(), want) {
		t.Fatalf("errNoHost = %q, want it to contain %q", errNoHost, want)
	}
}

func TestTooltipUsesShortSentencesWithoutDetails(t *testing.T) {
	err := errors.Join(
		fmt.Errorf("mute: %w", fmt.Errorf("read sink mute: %w", errors.New("pulse: connect: refused"))),
		errors.New("boom"),
	)
	got := tooltip(true, "", usererr.Short(err), "", false)
	want := "Prezilla: presentation mode on (error: " +
		`Couldn't change the sound: no PulseAudio or PipeWire server is running. Start your sound server, or remove "mute" from "switches" in config.json. boom)`
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
	if strings.Contains(got, "details:") || strings.Contains(got, "pulse: connect") {
		t.Errorf("tooltip leaks technical text: %q", got)
	}
}

func TestActionStateKeepsShortTextForTooltip(t *testing.T) {
	var a actionState
	a.run(context.Background(), func(context.Context) error {
		return fmt.Errorf("minimize: %w", fmt.Errorf("show desktop: %w", errors.New("x11: connect: no display")))
	}, func() {})
	if got := a.text(); strings.Contains(got, "x11: connect") || !strings.HasPrefix(got, "Couldn't show the desktop:") {
		t.Errorf("text %q", got)
	}
}

func TestExpiryNoteDeduplicatesOnExplainedText(t *testing.T) {
	err := fmt.Errorf("icons: %w", errors.New("boom"))
	n := noteExpiry("", err)
	if !n.print || n.explained != "icons: boom" || n.short != "icons: boom" {
		t.Fatalf("first: %+v", n)
	}
	if n2 := noteExpiry(n.explained, err); n2.print {
		t.Errorf("same error printed twice: %+v", n2)
	}
	mapped := fmt.Errorf("mute: %w", fmt.Errorf("read sink mute: %w", errors.New("pulse: connect: refused")))
	m := noteExpiry(n.explained, mapped)
	if !m.print || m.explained != usererr.Explain(mapped) || strings.Contains(m.short, "details:") {
		t.Errorf("mapped: %+v", m)
	}
	if c := noteExpiry(m.explained, nil); c.print || c.explained != "" || c.short != "" {
		t.Errorf("cleared: %+v", c)
	}
}

// systray exports no hover text on Linux, so the title itself says that
// quitting leaves presentation mode on.
func TestMenuStateQuitTitle(t *testing.T) {
	for _, c := range []struct {
		active bool
		want   string
	}{{false, "Quit"}, {true, "Quit (presentation stays on)"}} {
		q := menuState(c.active, "", 0, false, testTimeout).Quit
		if q.Title != c.want || !q.Visible || !q.Enabled || q.Tooltip != quitTooltip {
			t.Errorf("active %v: %+v", c.active, q)
		}
	}
}

package tray

import (
	"bytes"
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
	"prezilla/internal/tray/icons"
)

func TestLabelsActiveAndInactive(t *testing.T) {
	if tip := Labels(true); tip != "prezilla: presentation mode on" {
		t.Errorf("active: %q", tip)
	}
	if tip := Labels(false); tip != "prezilla: presentation mode off" {
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
		{false, "", "", "", false, "prezilla: presentation mode off"},
		{true, "", "", "", false, "prezilla: presentation mode on"},
		{true, "3 min left", "", "", false, "prezilla: presentation mode on, 3 min left"},
		{false, "", "boom", "", false, "prezilla: presentation mode off (error: boom)"},
		{true, "1 min left", "boom", "", false, "prezilla: presentation mode on, 1 min left (error: boom)"},
		{true, "", "", "expired", false, "prezilla: presentation mode on (error: expired)"},
		{true, "", "boom", "expired", false, "prezilla: presentation mode on (error: boom; expired)"},
		{true, "3 min left", "", "", true, "prezilla: presentation mode on, 3 min left\nScreensaver started. Keep presenting?"},
		{true, "3 min left", "boom", "", true, "prezilla: presentation mode on, 3 min left (error: boom)\nScreensaver started. Keep presenting?"},
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
	if s != "prezilla 1.2.3" {
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
	if len(n.Calls) != 1 || n.Calls[0].Summary != "prezilla 1.2.3" || n.Calls[0].Icon != "prezilla" {
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
	if tip := tooltip(false, "", a.text(), "", false); tip != "prezilla: presentation mode off (error: no bus)" {
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
	const timer = "Ends 12:12 · 12 min left"
	cases := []struct {
		name        string
		active      bool
		timer       string
		pending     bool
		step        time.Duration
		wantVisible []string
		wantChecked bool
	}{
		{"inactive", false, "", false, testTimeout, []string{"Presentation mode"}, false},
		{"active without deadline", true, "", false, testTimeout, []string{"Presentation mode"}, true},
		{"active with deadline", true, timer, false, testTimeout,
			[]string{"Presentation mode", timer, "Extend by 30 min"}, true},
		{"pending question", true, timer, true, testTimeout,
			[]string{"Keep presenting (+30 min)", "Stop presenting", "Presentation mode", timer, "Extend by 30 min"}, true},
		{"step 45", true, timer, true, 45 * time.Minute,
			[]string{"Keep presenting (+45 min)", "Stop presenting", "Presentation mode", timer, "Extend by 45 min"}, true},
	}
	for _, c := range cases {
		m := menuState(c.active, c.timer, c.pending, c.step)
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
		for _, it := range []itemState{m.Keep, m.Stop, m.Extend} {
			if it.Visible && !it.Enabled {
				t.Errorf("%s: %q visible but disabled", c.name, it.Title)
			}
		}
		for _, it := range []itemState{m.Keep, m.Stop, m.Mode, m.Extend} {
			if it.Checked && it.Title != "Presentation mode" {
				t.Errorf("%s: %q checked", c.name, it.Title)
			}
		}
	}
}

func TestMenuStateCheckboxTitleNeverChanges(t *testing.T) {
	for _, active := range []bool{false, true} {
		for _, timer := range []string{"", "Ends 12:12 · 12 min left"} {
			for _, pending := range []bool{false, true} {
				if got := menuState(active, timer, pending, testTimeout).Mode.Title; got != modeTitle {
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
		cfg              time.Duration
		keep, extend, ks string
	}{
		{0, "Keep presenting (+30 min)", "Extend by 30 min", "Keeps presentation mode on until 30 minutes from now."},
		{45 * time.Minute, "Keep presenting (+45 min)", "Extend by 45 min", "Keeps presentation mode on until 45 minutes from now."},
	} {
		m := menuState(true, "x", true, continueStep(c.cfg))
		if m.Keep.Title != c.keep || m.Extend.Title != c.extend || m.Keep.Tooltip != c.ks {
			t.Errorf("cfg %v: keep %q extend %q tip %q", c.cfg, m.Keep.Title, m.Extend.Title, m.Keep.Tooltip)
		}
		if m.Stop.Title != "Stop presenting" {
			t.Errorf("stop %q", m.Stop.Title)
		}
		for _, it := range m.ordered() {
			if it.Tooltip == "" || !strings.HasSuffix(it.Tooltip, ".") && it.Title != modeTitle {
				t.Errorf("%q tooltip %q", it.Title, it.Tooltip)
			}
		}
	}
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

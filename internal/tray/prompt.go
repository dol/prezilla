package tray

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"prezilla/internal/about"
	"prezilla/internal/core"
	"prezilla/internal/system"
)

const (
	keyContinue = "continue"
	keyEnd      = "end"
	// promptLine is the tooltip line shown while a prompt is pending.
	promptLine = "Screensaver started. Keep presenting?"
	// stopTitle answers the question by turning the mode off.
	stopTitle = "Stop presenting"
)

// shouldPrompt reports whether a screensaver change to active should ask
// about the running session: only while the mode is on with a live deadline.
func shouldPrompt(active bool, st core.State, now time.Time) bool {
	return active && st.Active && !st.ExpiresAt.IsZero() && now.Before(st.ExpiresAt)
}

// timerLabel is the timer menu item text, e.g. "Ends 15:04 · 12 min left",
// with the clock time in expires' own location; empty without a live deadline.
func timerLabel(now, expires time.Time) string {
	rem := remainingText(now, expires)
	if rem == "" {
		return ""
	}
	return "Ends " + expires.Format("15:04") + " · " + rem
}

// keepTitle answers the question by setting the end time to step from now
// (a later end time is kept); the notification action and the menu item
// share it.
func keepTitle(step time.Duration) string {
	return fmt.Sprintf("Keep presenting for %d min", stepMinutes(step))
}

// stepMinutes is step in whole minutes, rounded to the nearest minute.
func stepMinutes(step time.Duration) int {
	return int((step + time.Minute/2) / time.Minute)
}

// promptRequest builds the "keep presenting?" question for st; keeping sets
// the end time to step from now.
func promptRequest(info about.Info, st core.State, now time.Time, step time.Duration) system.Prompt {
	return system.Prompt{
		Summary:  "Keep presenting?",
		Body:     "The screensaver started with " + remainingText(now, st.ExpiresAt) + ".",
		IconName: info.IconName,
		Actions: []system.PromptAction{
			{Key: keyContinue, Label: keepTitle(step)},
			{Key: keyEnd, Label: stopTitle},
		},
	}
}

// continueStep is the time from now that "Keep presenting" and "Reset
// timer" set the end time to (a later end time is kept): the configured
// timeout, or the default 30 minutes when none is configured (a deadline can
// still exist through "on --for").
func continueStep(cfgTimeout time.Duration) time.Duration {
	if cfgTimeout > 0 {
		return cfgTimeout
	}
	return core.DefaultConfig().Timeout()
}

// promptDeps is what the coordinator needs; tests inject fakes.
type promptDeps struct {
	// prompter shows the notification; nil leaves only the menu items.
	prompter system.Prompter
	info     about.Info
	// step is the time from now that "keep presenting" sets the end
	// time to (label only).
	step   time.Duration
	status func() (core.State, error)
	// extend moves the deadline of a live session (never shortens or clears
	// it) and reports false, changing nothing, once the session has ended.
	extend  func(ctx context.Context) (bool, error)
	disable func(ctx context.Context) error
	now     func() time.Time
	// changed is called (never with locks held) when the pending state or
	// the error text changed, so the tray can redraw.
	changed func()
}

// promptCoordinator asks at most one "keep presenting?" question at a
// time. The notification answer and the two fallback menu items race; the
// first answer wins and cancels the other.
type promptCoordinator struct {
	d  promptDeps
	wg sync.WaitGroup

	mu      sync.Mutex
	waiting bool               // a question is pending
	gen     uint64             // identifies the pending question
	cancel  context.CancelFunc // cancels the pending notification
	err     string             // last prompter or engine error
	closed  bool               // wait was called; start nothing new
}

func newPromptCoordinator(d promptDeps) *promptCoordinator {
	if d.now == nil {
		d.now = time.Now
	}
	if d.changed == nil {
		d.changed = func() {}
	}
	return &promptCoordinator{d: d}
}

// watch forwards screensaver changes from ss until ctx ends. A nil ss
// disables the feature. Subscription errors are returned.
func (c *promptCoordinator) watch(ctx context.Context, ss system.ScreenSaver) error {
	if ss == nil {
		return nil
	}
	events := make(chan bool, 16)
	errc := make(chan error, 1)
	go func() {
		errc <- ss.Watch(ctx, func(active bool) {
			select { // never block the signal goroutine
			case events <- active:
			default:
			}
		})
	}()
	for {
		select {
		case active := <-events:
			c.screensaver(ctx, active)
		case err := <-errc:
			return err
		}
	}
}

// screensaver handles one screensaver change. Only a change to active with a
// live deadline asks, and only when no question is pending; a change to
// inactive leaves a pending question alone.
func (c *promptCoordinator) screensaver(ctx context.Context, active bool) {
	if !active || c.d.status == nil || c.pending() {
		return
	}
	st, err := c.d.status()
	if err != nil || !shouldPrompt(active, st, c.d.now()) {
		return // a status error is reported by the tray's poll
	}
	c.mu.Lock()
	if c.waiting || c.closed || ctx.Err() != nil {
		c.mu.Unlock()
		return
	}
	pctx, cancel := context.WithCancel(ctx)
	c.waiting, c.cancel, c.err = true, cancel, ""
	c.gen++
	gen := c.gen
	p := c.d.prompter
	if p != nil {
		c.wg.Add(1)
	}
	c.mu.Unlock()
	c.d.changed()
	if p == nil {
		return
	}
	req := promptRequest(c.d.info, st, c.d.now(), c.d.step)
	go func() {
		defer c.wg.Done()
		key, err := p.Ask(pctx, req)
		c.finish(ctx, pctx, gen, key, err)
	}()
}

// finish handles the notification's answer for question gen.
func (c *promptCoordinator) finish(ctx, pctx context.Context, gen uint64, key string, err error) {
	c.mu.Lock()
	if !c.waiting || c.gen != gen {
		c.mu.Unlock()
		return // the menu or the poll already resolved it
	}
	if pctx.Err() != nil {
		// Quit: resolve silently.
		c.resolveLocked()
		c.mu.Unlock()
		c.d.changed()
		return
	}
	if errors.Is(err, system.ErrNotificationGone) {
		// The server withdrew the notification (do-not-disturb, expiry):
		// the question stays open through the menu items, with no tooltip
		// error. Only a stderr line records it.
		c.mu.Unlock()
		fmt.Fprintln(os.Stderr, err)
		return
	}
	if err != nil {
		// The notification failed; the menu items stay as the fallback.
		c.err = err.Error()
		c.mu.Unlock()
		fmt.Fprintln(os.Stderr, err)
		c.d.changed()
		return
	}
	c.resolveLocked()
	c.mu.Unlock()
	c.d.changed()
	c.apply(ctx, key)
}

// resolveLocked ends the pending question and cancels its notification.
func (c *promptCoordinator) resolveLocked() {
	c.waiting = false
	if c.cancel != nil {
		c.cancel()
		c.cancel = nil
	}
}

// menuAnswer handles a click on a fallback menu item. It does nothing
// unless a question is pending; otherwise it closes the notification and
// applies key off the caller's goroutine.
func (c *promptCoordinator) menuAnswer(ctx context.Context, key string) {
	c.mu.Lock()
	if !c.waiting || c.closed {
		c.mu.Unlock()
		return
	}
	c.resolveLocked()
	c.wg.Add(1)
	c.mu.Unlock()
	c.d.changed()
	go func() {
		defer c.wg.Done()
		c.apply(ctx, key)
	}()
}

// apply carries out an answer, detached from ctx's cancellation and bounded
// by core.OperationTimeout. A session that ended or expired meanwhile is
// left alone, so an answer never brings a session back.
func (c *promptCoordinator) apply(ctx context.Context, key string) {
	var op func(context.Context) error
	switch key {
	case keyContinue:
		if c.d.extend != nil {
			// Extend's under-lock check is the authority; false is a no-op.
			op = func(ctx context.Context) error { _, err := c.d.extend(ctx); return err }
		}
	case keyEnd:
		op = c.d.disable
	}
	if op == nil || c.d.status == nil {
		return // dismissed or unknown: the timer still ends the session
	}
	st, err := c.d.status()
	if err == nil && (!st.Active || st.ExpiresAt.IsZero() || !c.d.now().Before(st.ExpiresAt)) {
		return
	}
	if err == nil {
		opCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), core.OperationTimeout)
		err = op(opCtx)
		cancel()
	}
	c.mu.Lock()
	c.err = ""
	if err != nil {
		c.err = err.Error()
	}
	c.mu.Unlock()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	c.d.changed()
}

// check cancels a pending question once the session has ended or expired;
// the tray's poll calls it with fresh state and redraws afterwards.
func (c *promptCoordinator) check(st core.State, now time.Time) {
	if st.Active && !st.ExpiresAt.IsZero() && now.Before(st.ExpiresAt) {
		return
	}
	c.mu.Lock()
	if c.waiting {
		c.resolveLocked()
	}
	c.mu.Unlock()
}

func (c *promptCoordinator) pending() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.waiting
}

func (c *promptCoordinator) errText() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// wait stops new work and blocks until running prompts and engine calls
// have finished. Prompts end when the ctx given to watch is cancelled.
func (c *promptCoordinator) wait() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.wg.Wait()
}

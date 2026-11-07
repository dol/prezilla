package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"
)

// Engine applies switches in order and restores them in reverse, persisting state.
// Every read-modify-write holds the Store's cross-process lock.
type Engine struct {
	mu        sync.Mutex // guards switches
	switches  []Switch
	restorers []Switch
	store     *Store
	now       func() time.Time
}

func NewEngine(switches []Switch, store *Store, now func() time.Time) *Engine {
	return &Engine{switches: switches, store: store, now: now}
}

// SetRestorers registers extra switches that Disable consults only for saved
// entries whose ID has no configured switch, so a switch removed from the
// config after "on" is still restored.
func (e *Engine) SetRestorers(all []Switch) { e.restorers = all }

// SetSwitches replaces the configured switches used by later operations.
func (e *Engine) SetSwitches(sw []Switch) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.switches = sw
}

func (e *Engine) configured() []Switch {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.switches
}

// Enable turns presentation mode on. timeout <= 0 means no deadline.
// If already active it only replaces the deadline.
func (e *Engine) Enable(ctx context.Context, timeout time.Duration) error {
	unlock, err := e.store.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	return e.enableLocked(ctx, timeout)
}

func (e *Engine) enableLocked(ctx context.Context, timeout time.Duration) error {
	switches := e.configured()
	st, err := e.store.Load()
	if err != nil {
		return err
	}
	st.ExpiresAt = time.Time{}
	if timeout > 0 {
		st.ExpiresAt = e.now().Add(timeout)
	}
	if st.Active {
		return e.store.Save(st)
	}
	if st.Saved == nil {
		st.Saved = map[string]string{}
	}
	var errs []error
	for _, sw := range switches {
		saved, err := sw.Enable(ctx)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", sw.ID(), err))
			continue
		}
		st.Saved[sw.ID()] = saved
	}
	st.Active = len(st.Saved) > 0
	if err := e.store.Save(st); err != nil {
		// Originals would be lost: best-effort roll back what was applied.
		errs = append(errs, fmt.Errorf("save state: %w", err))
		for i := len(switches) - 1; i >= 0; i-- {
			sw := switches[i]
			saved, ok := st.Saved[sw.ID()]
			if !ok {
				continue
			}
			if derr := sw.Disable(ctx, saved); derr != nil {
				errs = append(errs, fmt.Errorf("rollback %s: %w", sw.ID(), derr))
			}
		}
	}
	return errors.Join(errs...)
}

// Disable restores all saved switches in reverse order. No-op when inactive.
func (e *Engine) Disable(ctx context.Context) error {
	unlock, err := e.store.Lock()
	if err != nil {
		return err
	}
	defer unlock()
	return e.disableLocked(ctx)
}

func (e *Engine) disableLocked(ctx context.Context) error {
	switches := e.configured()
	st, err := e.store.Load()
	if err != nil {
		return err
	}
	if !st.Active {
		return nil
	}
	var errs []error
	known := map[string]bool{}
	for i := len(switches) - 1; i >= 0; i-- {
		sw := switches[i]
		known[sw.ID()] = true
		saved, ok := st.Saved[sw.ID()]
		if !ok {
			continue
		}
		if err := sw.Disable(ctx, saved); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", sw.ID(), err))
			continue
		}
		delete(st.Saved, sw.ID())
	}
	for i := len(e.restorers) - 1; i >= 0; i-- {
		sw := e.restorers[i]
		if known[sw.ID()] {
			continue
		}
		known[sw.ID()] = true
		saved, ok := st.Saved[sw.ID()]
		if !ok {
			continue
		}
		if err := sw.Disable(ctx, saved); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", sw.ID(), err))
			continue
		}
		delete(st.Saved, sw.ID())
	}
	var orphans []string
	for id := range st.Saved {
		if !known[id] {
			orphans = append(orphans, id)
		}
	}
	sort.Strings(orphans)
	for _, id := range orphans {
		errs = append(errs, fmt.Errorf("%s: no configured switch to restore", id))
		delete(st.Saved, id)
	}
	st.Active = len(st.Saved) > 0
	if !st.Active {
		st.ExpiresAt = time.Time{}
	}
	if err := e.store.Save(st); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// EnableOne applies one switch inside an active session and saves its original
// value; the deadline is untouched. It does nothing and reports false when the
// mode is off. A switch that is already saved is left alone. A failed switch
// changes no state; a failed save rolls the switch back best-effort.
func (e *Engine) EnableOne(ctx context.Context, sw Switch) (bool, error) {
	unlock, err := e.store.Lock()
	if err != nil {
		return false, err
	}
	defer unlock()
	st, err := e.store.Load()
	if err != nil {
		return false, err
	}
	if !st.Active {
		return false, nil
	}
	if _, ok := st.Saved[sw.ID()]; ok {
		return true, nil
	}
	saved, err := sw.Enable(ctx)
	if err != nil {
		return true, fmt.Errorf("%s: %w", sw.ID(), err)
	}
	if st.Saved == nil {
		st.Saved = map[string]string{}
	}
	st.Saved[sw.ID()] = saved
	if err := e.store.Save(st); err != nil {
		errs := []error{fmt.Errorf("save state: %w", err)}
		if derr := sw.Disable(ctx, saved); derr != nil {
			errs = append(errs, fmt.Errorf("rollback %s: %w", sw.ID(), derr))
		}
		return true, errors.Join(errs...)
	}
	return true, nil
}

// DisableOne restores one saved switch inside an active session. The mode stays
// active even when no switch is left, and the deadline is untouched. It does
// nothing and reports false when the mode is off; an ID that was never saved is
// a no-op. A failed restore keeps the saved entry.
func (e *Engine) DisableOne(ctx context.Context, id string) (bool, error) {
	unlock, err := e.store.Lock()
	if err != nil {
		return false, err
	}
	defer unlock()
	st, err := e.store.Load()
	if err != nil {
		return false, err
	}
	if !st.Active {
		return false, nil
	}
	saved, ok := st.Saved[id]
	if !ok {
		return true, nil
	}
	var sw Switch
	for _, c := range append(append([]Switch{}, e.restorers...), e.configured()...) {
		if c.ID() == id {
			sw = c
			break
		}
	}
	if sw == nil {
		return true, fmt.Errorf("%s: no configured switch to restore", id)
	}
	if err := sw.Disable(ctx, saved); err != nil {
		return true, fmt.Errorf("%s: %w", id, err)
	}
	delete(st.Saved, id)
	if err := e.store.Save(st); err != nil {
		return true, err
	}
	return true, nil
}

// Toggle flips the mode and reports whether it is now active.
func (e *Engine) Toggle(ctx context.Context, timeout time.Duration) (bool, error) {
	unlock, err := e.store.Lock()
	if err != nil {
		return false, err
	}
	defer unlock()
	st, err := e.store.Load()
	if err != nil {
		return false, err
	}
	if st.Active {
		err = e.disableLocked(ctx)
	} else {
		err = e.enableLocked(ctx, timeout)
	}
	after, lerr := e.store.Load()
	if lerr != nil {
		return false, errors.Join(err, lerr)
	}
	return after.Active, err
}

// DisableIfExpired disables when active and the deadline has passed.
func (e *Engine) DisableIfExpired(ctx context.Context) (bool, error) {
	unlock, err := e.store.Lock()
	if err != nil {
		return false, err
	}
	defer unlock()
	st, err := e.store.Load()
	if err != nil {
		return false, err
	}
	if !st.Active || st.ExpiresAt.IsZero() || e.now().Before(st.ExpiresAt) {
		return false, nil
	}
	return true, e.disableLocked(ctx)
}

// Extend moves the deadline of a live session to now+timeout unless it is
// already later; it never clears or shortens it. Under the state lock it
// changes nothing and reports false when the mode is off, has no deadline,
// the deadline has been reached, or timeout <= 0, so it can never turn a
// session back on. No switch is ever called.
func (e *Engine) Extend(ctx context.Context, timeout time.Duration) (bool, error) {
	if timeout <= 0 {
		return false, nil
	}
	unlock, err := e.store.Lock()
	if err != nil {
		return false, err
	}
	defer unlock()
	st, err := e.store.Load()
	if err != nil {
		return false, err
	}
	now := e.now()
	if !st.Active || st.ExpiresAt.IsZero() || !now.Before(st.ExpiresAt) {
		return false, nil
	}
	if next := now.Add(timeout); next.After(st.ExpiresAt) {
		st.ExpiresAt = next
	}
	if err := e.store.Save(st); err != nil {
		return false, err
	}
	return true, nil
}

// Status reads the state without locking; Save's atomic rename means it
// always sees a whole file.
func (e *Engine) Status() (State, error) { return e.store.Load() }

package core

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"
)

// Engine applies switches in order and restores them in reverse, persisting state.
// Every read-modify-write holds the Store's cross-process lock.
type Engine struct {
	switches []Switch
	store    *Store
	now      func() time.Time
}

func NewEngine(switches []Switch, store *Store, now func() time.Time) *Engine {
	return &Engine{switches: switches, store: store, now: now}
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
	for _, sw := range e.switches {
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
		for i := len(e.switches) - 1; i >= 0; i-- {
			sw := e.switches[i]
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
	st, err := e.store.Load()
	if err != nil {
		return err
	}
	if !st.Active {
		return nil
	}
	var errs []error
	known := map[string]bool{}
	for i := len(e.switches) - 1; i >= 0; i-- {
		sw := e.switches[i]
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

// Status reads the state without locking; Save's atomic rename means it
// always sees a whole file.
func (e *Engine) Status() (State, error) { return e.store.Load() }

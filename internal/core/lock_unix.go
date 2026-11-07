//go:build unix

package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const lockPollInterval = 25 * time.Millisecond

// Lock takes an exclusive cross-process lock on <state path>.lock, polling
// until it is free or the lock timeout passes. Callers must call unlock.
func (s *Store) Lock() (unlock func(), err error) {
	lockPath := s.path + ".lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, fmt.Errorf("create state dir for lock %s: %w", lockPath, err)
	}
	f, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open lock %s: %w", lockPath, err)
	}
	deadline := time.Now().Add(s.lockTimeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			f.Close()
			return nil, fmt.Errorf("lock %s: %w", lockPath, err)
		}
		if !time.Now().Before(deadline) {
			f.Close()
			return nil, fmt.Errorf("lock %s: timed out after %s waiting for another Prezilla process", lockPath, s.lockTimeout)
		}
		time.Sleep(lockPollInterval)
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

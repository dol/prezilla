package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// State is the persisted presentation-mode state.
type State struct {
	Active    bool              `json:"active"`
	Saved     map[string]string `json:"saved"`
	ExpiresAt time.Time         `json:"expires_at"` // zero = no deadline
}

// Store persists State as JSON at a path.
type Store struct {
	path        string
	lockTimeout time.Duration // how long Lock waits for another holder
}

// defaultLockTimeout bounds how long Lock waits for another process.
const defaultLockTimeout = 30 * time.Second

// OperationTimeout bounds one whole engine operation (and so how long the
// state lock is held). It must stay below defaultLockTimeout so a stuck
// holder is cut off before waiters give up.
const OperationTimeout = 25 * time.Second

// CorrectiveTimeout bounds one best-effort corrective call a switch makes
// after a failed change (for example unmuting after a mute request that
// errored but may have landed). It runs detached from the caller's context,
// so it can extend an operation past OperationTimeout:
// OperationTimeout + CorrectiveTimeout + the state save must stay below
// defaultLockTimeout so waiters are never cut off first.
const CorrectiveTimeout = 3 * time.Second

func NewStore(path string) *Store { return &Store{path: path, lockTimeout: defaultLockTimeout} }

// Load returns the zero State if the file does not exist.
func (s *Store) Load() (State, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, fs.ErrNotExist) {
		return State{}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("read state %s: %w", s.path, err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("parse state %s: %w", s.path, err)
	}
	return st, nil
}

// Save writes atomically and durably: fsynced temp file in the same
// directory, rename, then a best-effort fsync of the directory.
func (s *Store) Save(st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(s.path, data, 0o700, 0o600, "state")
}

// writeFileAtomic writes data to path through an fsynced temp file in the
// same directory and a rename, creating the directory with dirPerm. what
// names the file in errors.
func writeFileAtomic(path string, data []byte, dirPerm, perm os.FileMode, what string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		return fmt.Errorf("create %s dir %s: %w", what, dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+what+"-*.tmp")
	if err != nil {
		return fmt.Errorf("write %s %s: %w", what, path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s %s: %w", what, path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s %s: %w", what, path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("write %s %s: %w", what, path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write %s %s: %w", what, path, err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write %s %s: %w", what, path, err)
	}
	syncDir(dir)
	return nil
}

// syncDir best-effort fsyncs dir so a completed rename survives a crash.
// Errors are ignored: not every platform or filesystem supports it.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	d.Close()
}

// DefaultStatePath is $XDG_STATE_HOME/prezilla/state.json, else ~/.local/state/prezilla/state.json.
func DefaultStatePath() string {
	dir := os.Getenv("XDG_STATE_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(dir, "prezilla", "state.json")
}

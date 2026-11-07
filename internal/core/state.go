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
	path string
}

func NewStore(path string) *Store { return &Store{path: path} }

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

// Save writes atomically via a temp file in the same directory.
func (s *Store) Save(st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create state dir %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("write state %s: %w", s.path, err)
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write state %s: %w", s.path, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("write state %s: %w", s.path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write state %s: %w", s.path, err)
	}
	if err := os.Rename(tmp.Name(), s.path); err != nil {
		return fmt.Errorf("write state %s: %w", s.path, err)
	}
	return nil
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

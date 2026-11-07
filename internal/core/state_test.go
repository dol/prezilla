package core

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStoreRoundTrip(t *testing.T) {
	s := NewStore(filepath.Join(t.TempDir(), "state.json"))
	want := State{Active: true, Saved: map[string]string{"icons": "true"}, ExpiresAt: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
	if err := s.Save(want); err != nil {
		t.Fatal(err)
	}
	got, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !got.Active || got.Saved["icons"] != "true" || !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Fatalf("got %+v", got)
	}
}

func TestStoreMissingFileIsZeroState(t *testing.T) {
	got, err := NewStore(filepath.Join(t.TempDir(), "x.json")).Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.Active || len(got.Saved) != 0 || !got.ExpiresAt.IsZero() {
		t.Fatalf("got %+v", got)
	}
}

func TestStoreCorruptFileErrorNamesPathAndKeepsFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "state.json")
	orig := []byte(`{corrupt`)
	os.WriteFile(p, orig, 0o600)
	if _, err := NewStore(p).Load(); err == nil || !strings.Contains(err.Error(), p) {
		t.Fatalf("err = %v", err)
	}
	b, _ := os.ReadFile(p)
	if !bytes.Equal(b, orig) {
		t.Fatalf("file changed: %q", b)
	}
}

func TestStoreSaveCreatesParentDirs(t *testing.T) {
	p := filepath.Join(t.TempDir(), "a", "b", "state.json")
	if err := NewStore(p).Save(State{Active: true}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", fi.Mode().Perm())
	}
}

func TestOperationTimeoutBelowLockTimeout(t *testing.T) {
	if OperationTimeout >= defaultLockTimeout {
		t.Errorf("OperationTimeout %s must be below lock timeout %s", OperationTimeout, defaultLockTimeout)
	}
}

func TestOperationAndCorrectiveFitInsideLockWait(t *testing.T) {
	if CorrectiveTimeout <= 0 || CorrectiveTimeout > 3*time.Second {
		t.Fatalf("CorrectiveTimeout = %v, want (0, 3s]", CorrectiveTimeout)
	}
	if OperationTimeout+CorrectiveTimeout >= defaultLockTimeout {
		t.Fatalf("OperationTimeout %v + CorrectiveTimeout %v must stay below lock wait %v",
			OperationTimeout, CorrectiveTimeout, defaultLockTimeout)
	}
}

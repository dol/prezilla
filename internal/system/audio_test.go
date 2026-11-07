package system

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPulseAudioNoServerErrorsFast(t *testing.T) {
	t.Setenv("PULSE_SERVER", "unix:"+filepath.Join(t.TempDir(), "nonexistent"))
	start := time.Now()
	_, err := PulseAudio{}.DefaultSinkName(context.Background())
	if err == nil || !strings.Contains(err.Error(), "pulse: connect:") {
		t.Fatalf("got %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %s", d)
	}
}

func TestPulseAudioRefusesRelativeSocket(t *testing.T) {
	// Truly unset (t.Setenv restores them afterwards): an empty-but-set
	// PULSE_SERVER would already make the library refuse.
	for _, k := range []string{"PULSE_SERVER", "XDG_RUNTIME_DIR"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	// A server listening at the relative path the library would fall back to.
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "pulse"), 0o700); err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("unix", filepath.Join(dir, "pulse", "native"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var n atomic.Int32
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			n.Add(1)
			c.Close()
		}
	}()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	_, err = PulseAudio{Timeout: time.Second}.DefaultSinkName(context.Background())
	if err == nil || err.Error() != "pulse: connect: neither PULSE_SERVER nor XDG_RUNTIME_DIR is set" {
		t.Fatalf("got %v", err)
	}
	time.Sleep(50 * time.Millisecond)
	if got := n.Load(); got != 0 {
		t.Fatalf("dialled the relative socket %d times", got)
	}
}

// listen starts a unix listener, points PULSE_SERVER at it and counts the
// connections it accepts. If hold is set, accepted connections stay open and
// silent until the test ends.
func listen(t *testing.T, hold bool) *atomic.Int32 {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PULSE_SERVER", "unix:"+path)
	var n atomic.Int32
	done := make(chan struct{})
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			n.Add(1)
			if hold {
				go func() { <-done; c.Close() }()
			} else {
				c.Close()
			}
		}
	}()
	t.Cleanup(func() { close(done); l.Close() })
	return &n
}

func TestPulseAudioHonoursContext(t *testing.T) {
	n := listen(t, false)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	a := PulseAudio{}
	calls := map[string]func() error{
		"DefaultSinkName": func() error { _, err := a.DefaultSinkName(ctx); return err },
		"SinkMuted":       func() error { _, err := a.SinkMuted(ctx, "x"); return err },
		"SetSinkMute":     func() error { return a.SetSinkMute(ctx, "x", true) },
		"SinkExists":      func() error { _, err := a.SinkExists(ctx, "x"); return err },
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if got := n.Load(); got != 0 {
		t.Fatalf("%d connections made with a cancelled context", got)
	}
}

func TestPulseAudioCancelMidCall(t *testing.T) {
	n := listen(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for n.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	start := time.Now()
	err := PulseAudio{Timeout: 5 * time.Second}.SetSinkMute(ctx, "x", true)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %s", d)
	}
}

func TestPulseAudioTimeoutDefault(t *testing.T) {
	if got := (PulseAudio{}).timeout(); got != DefaultTimeout {
		t.Fatalf("got %s", got)
	}
}

func TestFakeAudioRecordsAndUpdates(t *testing.T) {
	f := &FakeAudio{Default: "a", Sinks: []string{"a"}}
	if err := f.SetSinkMute(context.Background(), "a", true); err != nil {
		t.Fatal(err)
	}
	m, err := f.SinkMuted(context.Background(), "a")
	if err != nil || !m {
		t.Fatalf("got (%v, %v)", m, err)
	}
	if ok, _ := f.SinkExists(context.Background(), "b"); ok {
		t.Fatal("b exists")
	}
}

package system

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestX11WindowsNoDisplayErrorsFast(t *testing.T) {
	t.Setenv("DISPLAY", "")
	start := time.Now()
	_, err := X11Windows{}.ShowingDesktop(context.Background())
	if err == nil || !strings.Contains(err.Error(), "x11: connect:") {
		t.Fatalf("got %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %s", d)
	}
}

// listenX points DISPLAY at a unix socket (xgb dials "<path>:<n>" for the
// display "<path>:<n>") and counts accepted connections. Accepted
// connections stay open and silent until the test ends.
func listenX(t *testing.T) *atomic.Int32 {
	t.Helper()
	path := filepath.Join(t.TempDir(), "x") + ":0"
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("DISPLAY", path)
	var n atomic.Int32
	done := make(chan struct{})
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			n.Add(1)
			go func() { <-done; c.Close() }()
		}
	}()
	t.Cleanup(func() { close(done); l.Close() })
	return &n
}

func TestX11WindowsHonoursContext(t *testing.T) {
	n := listenX(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := X11Windows{}
	if _, err := w.ShowingDesktop(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("ShowingDesktop: got %v", err)
	}
	if err := w.SetShowingDesktop(ctx, true); !errors.Is(err, context.Canceled) {
		t.Errorf("SetShowingDesktop: got %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := n.Load(); got != 0 {
		t.Fatalf("%d connections made with a cancelled context", got)
	}
}

func TestX11WindowsCancelMidCall(t *testing.T) {
	n := listenX(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for n.Load() == 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	start := time.Now()
	err := X11Windows{Timeout: 5 * time.Second}.SetShowingDesktop(ctx, true)
	if !errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %s", d)
	}
}

func TestX11WindowsOwnTimeout(t *testing.T) {
	listenX(t)
	err := X11Windows{Timeout: 100 * time.Millisecond}.SetShowingDesktop(context.Background(), true)
	if !errors.Is(err, context.DeadlineExceeded) || !strings.HasPrefix(err.Error(), "x11:") {
		t.Fatalf("got %v", err)
	}
}

func TestX11WindowsTimeoutDefault(t *testing.T) {
	if got := (X11Windows{}).timeout(); got != DefaultTimeout {
		t.Fatalf("got %s", got)
	}
}

func TestFakeWindowsRecordsAndUpdates(t *testing.T) {
	f := &FakeWindows{}
	if err := f.SetShowingDesktop(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	v, err := f.ShowingDesktop(context.Background())
	if err != nil || !v {
		t.Fatalf("got (%v, %v)", v, err)
	}
	if len(f.Calls) != 2 {
		t.Fatalf("calls %v", f.Calls)
	}
}

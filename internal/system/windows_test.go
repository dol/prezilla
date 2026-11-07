package system

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"
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

func TestFakeWindowsSetErrOnce(t *testing.T) {
	f := &FakeWindows{SetErrOnce: errors.New("once")}
	if err := f.SetShowingDesktop(context.Background(), true); err == nil || f.Showing {
		t.Fatalf("first set: err %v showing %v", err, f.Showing)
	}
	if err := f.SetShowingDesktop(context.Background(), true); err != nil || !f.Showing {
		t.Fatalf("second set: err %v showing %v", err, f.Showing)
	}
}

func TestSendShowingDesktopInternsBeforeContextCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var interned, sent int
	intern := func(name string) (xproto.Atom, error) {
		interned++
		if name != "_NET_SHOWING_DESKTOP" {
			t.Errorf("interned %q", name)
		}
		cancel() // the caller gives up during the round trip
		return 42, nil
	}
	send := func(xproto.Atom, int) error { sent++; return nil }
	err := sendShowingDesktop(ctx, true, intern, send)
	if !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), "x11:") {
		t.Fatalf("err = %v", err)
	}
	if interned != 1 || sent != 0 {
		t.Fatalf("interned %d sent %d", interned, sent)
	}
}

func TestSendShowingDesktopSendsResolvedAtom(t *testing.T) {
	for _, show := range []bool{true, false} {
		var gotAtom xproto.Atom
		gotN := -1
		err := sendShowingDesktop(context.Background(), show,
			func(string) (xproto.Atom, error) { return 42, nil },
			func(a xproto.Atom, n int) error { gotAtom, gotN = a, n; return nil })
		want := 0
		if show {
			want = 1
		}
		if err != nil || gotAtom != 42 || gotN != want {
			t.Errorf("show %v: err %v atom %d n %d", show, err, gotAtom, gotN)
		}
	}
}

func TestSendShowingDesktopErrors(t *testing.T) {
	err := sendShowingDesktop(context.Background(), true,
		func(string) (xproto.Atom, error) { return 0, errors.New("intern boom") },
		func(xproto.Atom, int) error { t.Error("sent"); return nil })
	if err == nil || !strings.HasPrefix(err.Error(), "x11: set showing desktop:") || !strings.Contains(err.Error(), "intern boom") {
		t.Errorf("intern err = %v", err)
	}
	err = sendShowingDesktop(context.Background(), true,
		func(string) (xproto.Atom, error) { return 42, nil },
		func(xproto.Atom, int) error { return errors.New("send boom") })
	if err == nil || !strings.HasPrefix(err.Error(), "x11: set showing desktop:") || !strings.Contains(err.Error(), "send boom") {
		t.Errorf("send err = %v", err)
	}
}

func TestRegularVisible(t *testing.T) {
	for _, c := range []struct {
		name          string
		types, states []string
		want          bool
	}{
		{"untyped", nil, nil, true},
		{"normal", []string{"_NET_WM_WINDOW_TYPE_NORMAL"}, []string{"_NET_WM_STATE_FOCUSED"}, true},
		{"dialog", []string{"_NET_WM_WINDOW_TYPE_DIALOG"}, nil, true},
		{"dialog then utility", []string{"_NET_WM_WINDOW_TYPE_UTILITY", "_NET_WM_WINDOW_TYPE_NORMAL"}, nil, true},
		{"hidden", nil, []string{"_NET_WM_STATE_HIDDEN"}, false},
		{"skip taskbar", []string{"_NET_WM_WINDOW_TYPE_NORMAL"}, []string{"_NET_WM_STATE_SKIP_TASKBAR"}, false},
		{"dock", []string{"_NET_WM_WINDOW_TYPE_DOCK"}, nil, false},
		{"desktop", []string{"_NET_WM_WINDOW_TYPE_DESKTOP"}, nil, false},
		{"splash", []string{"_NET_WM_WINDOW_TYPE_SPLASH"}, nil, false},
	} {
		if got := regularVisible(c.types, c.states); got != c.want {
			t.Errorf("%s: got %v", c.name, got)
		}
	}
}

func TestRestoreOrder(t *testing.T) {
	hidden := func(id uint32) bool { return id != 3 }
	got := restoreOrder([]uint32{5, 1, 3, 9, 1, 2}, []uint32{1, 2, 3, 5}, hidden)
	if want := []uint32{5, 1, 2}; !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	if got := restoreOrder(nil, []uint32{1}, hidden); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestIsHidden(t *testing.T) {
	if !isHidden([]string{"_NET_WM_STATE_ABOVE", "_NET_WM_STATE_HIDDEN"}) || isHidden(nil) {
		t.Fatal("isHidden")
	}
}

func TestX11WindowsListAndRestoreHonourContext(t *testing.T) {
	n := listenX(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := X11Windows{}
	if _, err := w.VisibleWindows(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("VisibleWindows: got %v", err)
	}
	if err := w.RestoreWindows(ctx, []uint32{1}); !errors.Is(err, context.Canceled) {
		t.Errorf("RestoreWindows: got %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	if got := n.Load(); got != 0 {
		t.Fatalf("%d connections made with a cancelled context", got)
	}
}

func TestX11WindowsListOwnTimeout(t *testing.T) {
	listenX(t)
	_, err := X11Windows{Timeout: 100 * time.Millisecond}.VisibleWindows(context.Background())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
}

func TestFakeWindowsVisibleAndRestore(t *testing.T) {
	f := &FakeWindows{Visible: []uint32{4, 5}}
	got, err := f.VisibleWindows(context.Background())
	if err != nil || !slices.Equal(got, []uint32{4, 5}) {
		t.Fatalf("got %v, %v", got, err)
	}
	if err := f.RestoreWindows(context.Background(), []uint32{4, 5}); err != nil {
		t.Fatal(err)
	}
	if want := []string{"VisibleWindows", "RestoreWindows 4,5"}; !slices.Equal(f.Calls, want) {
		t.Fatalf("calls %v", f.Calls)
	}
}

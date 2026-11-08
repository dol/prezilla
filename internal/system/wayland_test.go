package system

import (
	"context"
	"errors"
	"io"
	"net"
	"reflect"
	"sync"
	"testing"
	"time"
)

const fakeHandleBase = 0xff000000

type fakeToplevel struct {
	title, appID string
	minimized    bool
}

// fakeCompositor speaks just enough Wayland to serve WaylandToplevels.
type fakeCompositor struct {
	t         *testing.T
	conn      net.Conn
	toplevels []fakeToplevel
	noManager bool

	mu       sync.Mutex
	requests []string // "<handle>:<op>[:<seat>]"
	registry uint32
	seat     uint32
}

func (f *fakeCompositor) send(id uint32, op uint16, e wlEncoder) {
	msg := make([]byte, 8, 8+len(e))
	wlOrder.PutUint32(msg, id)
	wlOrder.PutUint32(msg[4:], uint32(8+len(e))<<16|uint32(op))
	f.current().Write(append(msg, e...))
}

func (f *fakeCompositor) current() net.Conn {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conn
}

func (f *fakeCompositor) setConn(c net.Conn) {
	f.mu.Lock()
	f.conn = c
	f.mu.Unlock()
}

func (f *fakeCompositor) announceToplevels(manager uint32) {
	for i, tl := range f.toplevels {
		h := uint32(fakeHandleBase + 1 + i)
		var e wlEncoder
		e.u32(h)
		f.send(manager, wlrManagerToplevel, e)
		e = nil
		e.str(tl.title)
		f.send(h, wlrHandleTitle, e)
		e = nil
		e.str(tl.appID)
		f.send(h, wlrHandleAppID, e)
		e = nil
		var state wlEncoder
		if tl.minimized {
			state.u32(wlrStateMinimized)
		}
		e.u32(uint32(len(state)))
		e = append(e, state...)
		f.send(h, wlrHandleState, e)
		f.send(h, wlrHandleDone, nil)
	}
}

func (f *fakeCompositor) serve(conn net.Conn) {
	var hdr [8]byte
	for {
		if _, err := io.ReadFull(conn, hdr[:]); err != nil {
			return
		}
		id := wlOrder.Uint32(hdr[:])
		size := int(wlOrder.Uint32(hdr[4:]) >> 16)
		op := uint16(wlOrder.Uint32(hdr[4:]))
		payload := make([]byte, size-8)
		io.ReadFull(conn, payload)
		d := wlDecoder{b: payload}
		switch {
		case id == waylandDisplayID && op == wlDisplayGetRegistry:
			f.mu.Lock()
			f.registry = d.u32()
			reg := f.registry
			f.mu.Unlock()
			for _, g := range []struct {
				name  uint32
				iface string
				ver   uint32
			}{{1, "wl_seat", 7}, {3, "wl_output", 4}, {2, waylandManagerIface, 3}} {
				if g.iface == waylandManagerIface && f.noManager {
					continue
				}
				var e wlEncoder
				e.u32(g.name)
				e.str(g.iface)
				e.u32(g.ver)
				f.send(reg, wlRegistryGlobal, e)
			}
		case id == waylandDisplayID && op == wlDisplaySync:
			var e wlEncoder
			e.u32(0)
			f.send(d.u32(), wlCallbackDone, e)
		case id == f.registry && op == wlRegistryBind:
			name, iface, version, newID := d.u32(), d.str(), d.u32(), d.u32()
			f.mu.Lock()
			f.requests = append(f.requests, "bind "+iface+" "+string(rune('0'+version)))
			f.mu.Unlock()
			if name == 1 {
				f.mu.Lock()
				f.seat = newID
				f.mu.Unlock()
			}
			if name == 2 {
				f.announceToplevels(newID)
			}
		default:
			r := ""
			switch op {
			case wlrHandleUnsetMinimized:
				r = "unset_minimized"
			case wlrHandleActivate:
				f.mu.Lock()
				r = "activate"
				if d.u32() != f.seat {
					r = "activate on the wrong seat"
				}
				f.mu.Unlock()
			case wlrHandleDestroy:
				r = "destroy"
			}
			f.mu.Lock()
			f.requests = append(f.requests, r+" "+itoa(id-fakeHandleBase))
			f.mu.Unlock()
		}
	}
}

func itoa(v uint32) string { return string(rune('0' + v)) }

func (f *fakeCompositor) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.requests...)
}

// newWayland returns a WaylandToplevels wired to a fake compositor.
func newWayland(t *testing.T, tl []fakeToplevel, noManager bool) (*WaylandToplevels, *fakeCompositor, *int) {
	t.Helper()
	f := &fakeCompositor{t: t, toplevels: tl, noManager: noManager}
	dials := new(int)
	var dmu sync.Mutex
	w := &WaylandToplevels{Timeout: 3 * time.Second, Dial: func(context.Context) (net.Conn, error) {
		dmu.Lock()
		*dials++
		dmu.Unlock()
		client, server := net.Pipe()
		f.setConn(server)
		go f.serve(server)
		return client, nil
	}}
	t.Cleanup(w.Close)
	return w, f, dials
}

func TestWaylandListSortedWithTitlesAndAppIDs(t *testing.T) {
	w, f, _ := newWayland(t, []fakeToplevel{{"Slides — Impress", "soffice", false}, {"Terminal", "foot", true}, {"", "ghost", false}}, false)
	got, err := w.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []Window{{ID: "4278190081", Title: "Slides — Impress", AppID: "soffice"}, {ID: "4278190082", Title: "Terminal", AppID: "foot"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	if reqs := f.got(); len(reqs) != 2 || reqs[0] != "bind wl_seat 1" || reqs[1] != "bind "+waylandManagerIface+" 3" {
		t.Errorf("requests %v", reqs)
	}
}

func TestWaylandFocusActivatesAndUnminimizes(t *testing.T) {
	w, f, _ := newWayland(t, []fakeToplevel{{"A", "a", false}, {"B", "b", true}}, false)
	ws, err := w.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Focus(context.Background(), ws[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := w.Focus(context.Background(), ws[1].ID); err != nil {
		t.Fatal(err)
	}
	reqs := f.got()[2:]
	want := []string{"activate 1", "unset_minimized 2", "activate 2"}
	if !reflect.DeepEqual(reqs, want) {
		t.Errorf("requests %v, want %v", reqs, want)
	}
}

func TestWaylandFocusUnknownWindow(t *testing.T) {
	w, _, _ := newWayland(t, []fakeToplevel{{"A", "a", false}}, false)
	for _, id := range []string{"12345", "x"} {
		if err := w.Focus(context.Background(), id); !errors.Is(err, ErrNoWindow) {
			t.Errorf("Focus(%q) = %v", id, err)
		}
	}
}

func TestWaylandWithoutManagerIsUnsupported(t *testing.T) {
	w, _, _ := newWayland(t, nil, true)
	if _, err := w.List(context.Background()); !errors.Is(err, ErrNoWindowFocus) {
		t.Errorf("List: %v", err)
	}
	if err := w.Focus(context.Background(), "1"); !errors.Is(err, ErrNoWindowFocus) {
		t.Errorf("Focus: %v", err)
	}
}

func TestWaylandReconnectsAfterTheConnectionBreaks(t *testing.T) {
	w, f, dials := newWayland(t, []fakeToplevel{{"A", "a", false}}, false)
	if _, err := w.List(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.current().Close()
	<-w.conn.dead
	ws, err := w.List(context.Background())
	if err != nil || len(ws) != 1 || *dials != 2 {
		t.Fatalf("ws %v, err %v, dials %d", ws, err, *dials)
	}
}

func TestWaylandDefaultDialNeedsDisplayAndRuntimeDir(t *testing.T) {
	for _, env := range []map[string]string{{}, {"WAYLAND_DISPLAY": "wayland-0"}} {
		w := &WaylandToplevels{Getenv: func(k string) string { return env[k] }}
		if _, err := w.List(context.Background()); !errors.Is(err, ErrNoWindowFocus) {
			t.Errorf("%v: %v", env, err)
		}
	}
}

func TestWaylandTimesOutOnASilentCompositor(t *testing.T) {
	w := &WaylandToplevels{Timeout: 50 * time.Millisecond, Dial: func(context.Context) (net.Conn, error) {
		c, _ := net.Pipe()
		return c, nil
	}}
	_, err := w.List(context.Background())
	if err == nil {
		t.Fatal("expected a timeout")
	}
}

func TestDefaultWindowFocusPicksBySession(t *testing.T) {
	env := func(kv map[string]string) func(string) string { return func(k string) string { return kv[k] } }
	if _, ok := DefaultWindowFocus(env(map[string]string{"WAYLAND_DISPLAY": "w", "DISPLAY": ":0"})).(*WaylandToplevels); !ok {
		t.Error("wayland expected")
	}
	if _, ok := DefaultWindowFocus(env(map[string]string{"DISPLAY": ":0"})).(X11Windows); !ok {
		t.Error("x11 expected")
	}
	if _, err := DefaultWindowFocus(env(nil)).List(context.Background()); !errors.Is(err, ErrNoWindowFocus) {
		t.Errorf("none: %v", err)
	}
}

func TestListableKeepsMinimizedDropsSkipTaskbar(t *testing.T) {
	normal := []string{"_NET_WM_WINDOW_TYPE_NORMAL"}
	if !listable(normal, []string{"_NET_WM_STATE_HIDDEN"}) {
		t.Error("minimized window must be listed")
	}
	if listable(normal, []string{"_NET_WM_STATE_SKIP_TASKBAR"}) {
		t.Error("skip-taskbar window must not be listed")
	}
	if listable([]string{"_NET_WM_WINDOW_TYPE_DOCK"}, nil) {
		t.Error("dock must not be listed")
	}
}

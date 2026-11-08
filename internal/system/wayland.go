package system

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"time"
)

// A minimal Wayland client: just enough of the wire protocol to bind
// wl_seat and zwlr_foreign_toplevel_manager_v1, which wlroots-based
// compositors (sway, labwc, wayfire, river, ...) and others offer. No file
// descriptors are passed, so a plain unix socket is enough.

const (
	waylandDisplayID = 1 // wl_display, always object 1

	// wl_display
	wlDisplaySync        = 0
	wlDisplayGetRegistry = 1
	wlDisplayError       = 0
	// wl_registry
	wlRegistryBind   = 0
	wlRegistryGlobal = 0
	// wl_callback
	wlCallbackDone = 0
	// zwlr_foreign_toplevel_manager_v1
	wlrManagerToplevel = 0
	// zwlr_foreign_toplevel_handle_v1 events
	wlrHandleTitle  = 0
	wlrHandleAppID  = 1
	wlrHandleState  = 4
	wlrHandleDone   = 5
	wlrHandleClosed = 6
	// ... and requests
	wlrHandleUnsetMinimized = 3
	wlrHandleActivate       = 4
	wlrHandleDestroy        = 7
	// zwlr_foreign_toplevel_handle_v1.state value
	wlrStateMinimized = 1

	waylandManagerIface = "zwlr_foreign_toplevel_manager_v1"
	waylandManagerMax   = 3
	waylandSeatIface    = "wl_seat"
	waylandMaxMessage   = 1 << 20
)

var wlOrder = binary.NativeEndian

// wlEncoder builds the arguments of one request.
type wlEncoder []byte

func (e *wlEncoder) u32(v uint32) { *e = wlOrder.AppendUint32(*e, v) }

func (e *wlEncoder) str(s string) {
	e.u32(uint32(len(s) + 1))
	*e = append(*e, s...)
	*e = append(*e, 0)
	for len(*e)%4 != 0 {
		*e = append(*e, 0)
	}
}

// wlDecoder reads the arguments of one event; a short or malformed payload
// sets bad and reads zero values from then on.
type wlDecoder struct {
	b   []byte
	bad bool
}

func (d *wlDecoder) u32() uint32 {
	if len(d.b) < 4 {
		d.bad = true
		return 0
	}
	v := wlOrder.Uint32(d.b)
	d.b = d.b[4:]
	return v
}

// bytes reads a length-prefixed, 4-byte padded blob (a string or an array).
func (d *wlDecoder) bytes() []byte {
	n := int(d.u32())
	pad := (n + 3) &^ 3
	if d.bad || n > len(d.b) || pad > len(d.b) {
		d.bad = true
		return nil
	}
	v := d.b[:n]
	d.b = d.b[pad:]
	return v
}

func (d *wlDecoder) str() string {
	b := d.bytes()
	if len(b) > 0 && b[len(b)-1] == 0 {
		b = b[:len(b)-1]
	}
	return string(b)
}

type wlGlobal struct{ name, version uint32 }

type wlToplevel struct {
	title, appID string
	minimized    bool
	done         bool
}

// waylandConn is one connection with the bound toplevel manager.
type waylandConn struct {
	c    net.Conn
	dead chan struct{} // closed when the reader ends

	wmu sync.Mutex // serializes writes

	mu        sync.Mutex // guards the fields below
	err       error      // why the connection ended
	nextID    uint32
	kinds     map[uint32]string
	callbacks map[uint32]chan struct{}
	globals   map[string]wlGlobal
	seat      uint32
	toplevels map[uint32]*wlToplevel
}

func (c *waylandConn) send(id uint32, op uint16, args wlEncoder) error {
	msg := make([]byte, 8, 8+len(args))
	wlOrder.PutUint32(msg, id)
	wlOrder.PutUint32(msg[4:], uint32(8+len(args))<<16|uint32(op))
	msg = append(msg, args...)
	c.wmu.Lock()
	defer c.wmu.Unlock()
	_, err := c.c.Write(msg)
	return err
}

// newObject reserves a client-side object id of the given kind.
func (c *waylandConn) newObject(kind string) uint32 {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	c.kinds[c.nextID] = kind
	return c.nextID
}

func (c *waylandConn) fail(err error) {
	c.mu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.mu.Unlock()
}

func (c *waylandConn) failure() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	return errors.New("connection closed")
}

// read dispatches events until the connection ends.
func (c *waylandConn) read() {
	defer close(c.dead)
	var hdr [8]byte
	for {
		if _, err := io.ReadFull(c.c, hdr[:]); err != nil {
			c.fail(err)
			return
		}
		id := wlOrder.Uint32(hdr[:])
		size := int(wlOrder.Uint32(hdr[4:]) >> 16)
		op := uint16(wlOrder.Uint32(hdr[4:]))
		if size < 8 || size > waylandMaxMessage || size%4 != 0 {
			c.fail(fmt.Errorf("malformed message size %d", size))
			return
		}
		payload := make([]byte, size-8)
		if _, err := io.ReadFull(c.c, payload); err != nil {
			c.fail(err)
			return
		}
		c.dispatch(id, op, wlDecoder{b: payload})
	}
}

func (c *waylandConn) dispatch(id uint32, op uint16, d wlDecoder) {
	c.mu.Lock()
	kind := c.kinds[id]
	var destroy bool
	switch kind {
	case "display":
		if op == wlDisplayError {
			d.u32()
			code := d.u32()
			if c.err == nil {
				c.err = fmt.Errorf("protocol error %d: %s", code, d.str())
			}
		}
	case "registry":
		if op == wlRegistryGlobal {
			name, iface, version := d.u32(), d.str(), d.u32()
			if _, dup := c.globals[iface]; !d.bad && !dup {
				c.globals[iface] = wlGlobal{name, version}
			}
		}
	case "callback":
		if op == wlCallbackDone {
			if ch := c.callbacks[id]; ch != nil {
				close(ch)
				delete(c.callbacks, id)
			}
		}
	case "manager":
		if op == wlrManagerToplevel {
			if h := d.u32(); !d.bad {
				c.kinds[h] = "toplevel"
				c.toplevels[h] = &wlToplevel{}
			}
		}
	case "toplevel":
		t := c.toplevels[id]
		if t == nil {
			break
		}
		switch op {
		case wlrHandleTitle:
			t.title = d.str()
		case wlrHandleAppID:
			t.appID = d.str()
		case wlrHandleState:
			state := wlDecoder{b: d.bytes()}
			t.minimized = false
			for !state.bad && len(state.b) > 0 {
				if state.u32() == wlrStateMinimized {
					t.minimized = true
				}
			}
		case wlrHandleDone:
			t.done = true
		case wlrHandleClosed:
			delete(c.toplevels, id)
			destroy = true
		}
	}
	c.mu.Unlock()
	if destroy {
		_ = c.send(id, wlrHandleDestroy, nil)
	}
}

// roundtrip waits until the compositor has handled every request sent so far
// and the resulting events have been dispatched.
func (c *waylandConn) roundtrip(ctx context.Context) error {
	id := c.newObject("callback")
	ch := make(chan struct{})
	c.mu.Lock()
	c.callbacks[id] = ch
	c.mu.Unlock()
	var e wlEncoder
	e.u32(id)
	if err := c.send(waylandDisplayID, wlDisplaySync, e); err != nil {
		return err
	}
	select {
	case <-ch:
		c.mu.Lock()
		err := c.err
		c.mu.Unlock()
		return err
	case <-c.dead:
		return c.failure()
	case <-ctx.Done():
		return ctx.Err()
	}
}

// bind binds the global iface at the lower of its version and max.
func (c *waylandConn) bind(registry uint32, iface string, max uint32) (uint32, bool) {
	c.mu.Lock()
	g, ok := c.globals[iface]
	c.mu.Unlock()
	if !ok {
		return 0, false
	}
	id := c.newObject(map[string]string{waylandSeatIface: "seat", waylandManagerIface: "manager"}[iface])
	var e wlEncoder
	e.u32(g.name)
	e.str(iface)
	e.u32(min(g.version, max))
	e.u32(id)
	if err := c.send(registry, wlRegistryBind, e); err != nil {
		return 0, false
	}
	return id, true
}

func (c *waylandConn) close() { _ = c.c.Close() }

// limit makes writes give up when ctx's deadline passes, so a compositor
// that stops reading cannot hold a call forever.
func (c *waylandConn) limit(ctx context.Context) {
	dl, _ := ctx.Deadline()
	_ = c.c.SetWriteDeadline(dl)
}

// WaylandToplevels lists and focuses windows through the
// zwlr_foreign_toplevel_manager_v1 protocol. The connection is opened on the
// first call and kept; it reconnects after it breaks. The zero value works
// (os.Getenv); a compositor without the protocol yields ErrNoWindowFocus.
type WaylandToplevels struct {
	// Getenv defaults to os.Getenv.
	Getenv func(string) string
	// Timeout bounds each call; zero means DefaultTimeout.
	Timeout time.Duration
	// Dial opens the compositor socket; it defaults to a unix socket at
	// $XDG_RUNTIME_DIR/$WAYLAND_DISPLAY.
	Dial func(ctx context.Context) (net.Conn, error)

	mu   sync.Mutex // one call at a time
	conn *waylandConn
}

func (w *WaylandToplevels) timeout() time.Duration {
	if w.Timeout > 0 {
		return w.Timeout
	}
	return DefaultTimeout
}

func (w *WaylandToplevels) dial(ctx context.Context) (net.Conn, error) {
	if w.Dial != nil {
		return w.Dial(ctx)
	}
	getenv := w.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	display := getenv("WAYLAND_DISPLAY")
	if display == "" {
		return nil, fmt.Errorf("%w: WAYLAND_DISPLAY is not set", ErrNoWindowFocus)
	}
	path := display
	if !filepath.IsAbs(display) {
		dir := getenv("XDG_RUNTIME_DIR")
		if dir == "" {
			return nil, fmt.Errorf("%w: XDG_RUNTIME_DIR is not set", ErrNoWindowFocus)
		}
		path = filepath.Join(dir, display)
	}
	var d net.Dialer
	return d.DialContext(ctx, "unix", path)
}

// connect opens a connection and binds the seat and the toplevel manager.
func (w *WaylandToplevels) connect(ctx context.Context) (*waylandConn, error) {
	nc, err := w.dial(ctx)
	if err != nil {
		if errors.Is(err, ErrNoWindowFocus) {
			return nil, err
		}
		return nil, fmt.Errorf("wayland: connect: %w", err)
	}
	c := &waylandConn{
		c: nc, dead: make(chan struct{}), nextID: waylandDisplayID,
		kinds:     map[uint32]string{waylandDisplayID: "display"},
		callbacks: map[uint32]chan struct{}{},
		globals:   map[string]wlGlobal{},
		toplevels: map[uint32]*wlToplevel{},
	}
	c.limit(ctx)
	go c.read()
	fail := func(err error) (*waylandConn, error) {
		c.close()
		return nil, err
	}
	registry := c.newObject("registry")
	var e wlEncoder
	e.u32(registry)
	if err := c.send(waylandDisplayID, wlDisplayGetRegistry, e); err != nil {
		return fail(fmt.Errorf("wayland: %w", err))
	}
	if err := c.roundtrip(ctx); err != nil {
		return fail(fmt.Errorf("wayland: %w", err))
	}
	seat, hasSeat := c.bind(registry, waylandSeatIface, 1)
	if _, ok := c.bind(registry, waylandManagerIface, waylandManagerMax); !ok || !hasSeat {
		return fail(fmt.Errorf("%w: the compositor lacks %s", ErrNoWindowFocus, waylandManagerIface))
	}
	c.seat = seat
	// The compositor sends every toplevel with its properties right after
	// the bind; this roundtrip waits for them.
	if err := c.roundtrip(ctx); err != nil {
		return fail(fmt.Errorf("wayland: %w", err))
	}
	return c, nil
}

// session returns the live connection, opening one if needed. The caller
// holds w.mu.
func (w *WaylandToplevels) session(ctx context.Context) (*waylandConn, error) {
	if w.conn != nil {
		select {
		case <-w.conn.dead:
			w.conn = nil
		default:
			w.conn.limit(ctx)
			return w.conn, nil
		}
	}
	c, err := w.connect(ctx)
	if err != nil {
		return nil, err
	}
	w.conn = c
	return c, nil
}

// drop forgets and closes a connection that failed.
func (w *WaylandToplevels) drop(c *waylandConn) {
	c.close()
	if w.conn == c {
		w.conn = nil
	}
}

func (w *WaylandToplevels) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, w.timeout())
}

// List implements WindowFocus. Windows without a title are left out.
func (w *WaylandToplevels) List(ctx context.Context) ([]Window, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	ctx, cancel := w.bounded(ctx)
	defer cancel()
	c, err := w.session(ctx)
	if err != nil {
		return nil, err
	}
	if err := c.roundtrip(ctx); err != nil {
		w.drop(c)
		return nil, fmt.Errorf("wayland: %w", err)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	ids := make([]uint32, 0, len(c.toplevels))
	for id, t := range c.toplevels {
		if t.done && t.title != "" {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	out := make([]Window, len(ids))
	for i, id := range ids {
		t := c.toplevels[id]
		out[i] = Window{ID: strconv.FormatUint(uint64(id), 10), Title: t.title, AppID: t.appID}
	}
	return out, nil
}

// Focus implements WindowFocus: it unminimizes the window if needed and
// activates it on the seat.
func (w *WaylandToplevels) Focus(ctx context.Context, id string) error {
	n, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		return fmt.Errorf("%w: %q", ErrNoWindow, id)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	ctx, cancel := w.bounded(ctx)
	defer cancel()
	c, err := w.session(ctx)
	if err != nil {
		return err
	}
	c.mu.Lock()
	t, ok := c.toplevels[uint32(n)]
	minimized := ok && t.minimized
	seat := c.seat
	c.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", ErrNoWindow, id)
	}
	if minimized {
		if err := c.send(uint32(n), wlrHandleUnsetMinimized, nil); err != nil {
			w.drop(c)
			return fmt.Errorf("wayland: %w", err)
		}
	}
	var e wlEncoder
	e.u32(seat)
	if err := c.send(uint32(n), wlrHandleActivate, e); err != nil {
		w.drop(c)
		return fmt.Errorf("wayland: %w", err)
	}
	// A roundtrip surfaces a protocol error the compositor raised.
	if err := c.roundtrip(ctx); err != nil {
		w.drop(c)
		return fmt.Errorf("wayland: %w", err)
	}
	return nil
}

// Close ends the connection, if one is open.
func (w *WaylandToplevels) Close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.conn != nil {
		w.conn.close()
		w.conn = nil
	}
}

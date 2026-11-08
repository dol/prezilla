package system

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"slices"
	"strconv"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/composite"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
)

// WindowPicker shows the windows as cards and returns the one the user
// picked.
type WindowPicker interface {
	// Pick returns the ID of the chosen window, ErrCancelled when the user
	// closed the picker without choosing, or ctx's error.
	Pick(ctx context.Context, windows []Window) (string, error)
}

// thumbCache remembers the last thumbnail captured of each window, so a
// window that has no pixels now (minimized) still shows how it looked.
type thumbCache struct {
	mu sync.Mutex
	m  map[string]*image.RGBA
}

// resolve returns fresh when there is one, remembering it, otherwise the
// remembered thumbnail (or nil).
func (c *thumbCache) resolve(id string, fresh *image.RGBA) *image.RGBA {
	c.mu.Lock()
	defer c.mu.Unlock()
	if fresh != nil {
		if c.m == nil {
			c.m = map[string]*image.RGBA{}
		}
		c.m[id] = fresh
		return fresh
	}
	return c.m[id]
}

// prune forgets the windows that are not in keep.
func (c *thumbCache) prune(keep []Window) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for id := range c.m {
		if !slices.ContainsFunc(keep, func(w Window) bool { return w.ID == id }) {
			delete(c.m, id)
		}
	}
}

// X11Picker is the built-in window picker for X11: a window of cards, each
// with a thumbnail of the window and its title, drawn like the About window
// (embedded fonts, PutImage, no toolkit). The Window IDs must be X window
// ids, as X11Windows.List returns them. A window without pixels (minimized,
// on another workspace) shows the thumbnail captured the last time it was
// visible, or a placeholder card when it never was. Use one X11Picker for the
// whole session so that memory persists.
type X11Picker struct {
	cache thumbCache
}

// maxCapturePixels bounds one thumbnail capture (the source is full size).
const maxCapturePixels = 1 << 26

// unpack is the inverse of pack for a 32 bits-per-pixel ZPixmap in the
// server's format: rows of w pixels, opaque RGBA out.
func (pf pixelFormat) unpack(data []byte, w, h int) *image.RGBA {
	if w <= 0 || h <= 0 || len(data) < w*h*4 {
		return nil
	}
	rs, rw := maskShift(pf.red)
	gs, gw := maskShift(pf.green)
	bs, bw := maskShift(pf.blue)
	// Each channel is widened to 8 bits.
	ch := func(v uint32, shift, width uint) uint8 {
		return uint8((v >> shift & (1<<width - 1)) * 255 / (1<<width - 1))
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; i++ {
		var v uint32
		if pf.msbFirst {
			v = binary.BigEndian.Uint32(data[i*4:])
		} else {
			v = binary.LittleEndian.Uint32(data[i*4:])
		}
		img.Pix[i*4], img.Pix[i*4+1], img.Pix[i*4+2], img.Pix[i*4+3] = ch(v, rs, rw), ch(v, gs, gw), ch(v, bs, bw), 0xff
	}
	return img
}

// errNoPixels means a window has nothing to capture right now.
var errNoPixels = errors.New("window has no pixels")

// captureWindow reads the contents of win. It first asks the Composite
// extension for the window's off-screen pixmap, so windows hidden behind
// others read correctly (with a compositing window manager the window is
// already redirected; otherwise it is redirected for the moment of the
// capture), and falls back to reading the window itself.
func captureWindow(xu *xgbutil.XUtil, pf pixelFormat, win xproto.Window, haveComposite bool) (*image.RGBA, error) {
	conn := xu.Conn()
	attrs, err := xproto.GetWindowAttributes(conn, win).Reply()
	if err != nil {
		return nil, err
	}
	if attrs.MapState != xproto.MapStateViewable {
		return nil, errNoPixels
	}
	geom, err := xproto.GetGeometry(conn, xproto.Drawable(win)).Reply()
	if err != nil {
		return nil, err
	}
	w, h := int(geom.Width), int(geom.Height)
	if w == 0 || h == 0 || w*h > maxCapturePixels {
		return nil, errNoPixels
	}
	read := func(d xproto.Drawable) (*image.RGBA, error) {
		r, err := xproto.GetImage(conn, xproto.ImageFormatZPixmap, d, 0, 0, uint16(w), uint16(h), 0xffffffff).Reply()
		if err != nil {
			return nil, err
		}
		img := pf.unpack(r.Data, w, h)
		if img == nil {
			return nil, errNoPixels
		}
		return img, nil
	}
	if haveComposite {
		if img, err := captureNamedPixmap(conn, win, read); err == nil {
			return img, nil
		}
	}
	return read(xproto.Drawable(win))
}

// captureNamedPixmap reads win through its Composite pixmap.
func captureNamedPixmap(conn *xgb.Conn, win xproto.Window, read func(xproto.Drawable) (*image.RGBA, error)) (*image.RGBA, error) {
	pix, err := xproto.NewPixmapId(conn)
	if err != nil {
		return nil, err
	}
	name := func() error { return composite.NameWindowPixmapChecked(conn, win, pix).Check() }
	if err := name(); err != nil {
		// Not redirected yet: redirect automatically for this capture.
		if composite.RedirectWindowChecked(conn, win, composite.RedirectAutomatic).Check() != nil {
			return nil, err
		}
		defer composite.UnredirectWindow(conn, win, composite.RedirectAutomatic)
		if err := name(); err != nil {
			return nil, err
		}
	}
	defer xproto.FreePixmap(conn, pix)
	return read(xproto.Drawable(pix))
}

// Pick implements WindowPicker.
func (p *X11Picker) Pick(ctx context.Context, windows []Window) (id string, err error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	// A library panic must not take the tray down.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("window picker: x11: internal error: %v", r)
		}
	}()
	xu, cerr := xgbutil.NewConn()
	if cerr != nil {
		return "", fmt.Errorf("%w: %w", ErrNoDisplay, cerr)
	}
	defer xu.Conn().Close()
	scr := xu.Screen()
	pf, err := pixelFormatFor(xu.Setup(), scr)
	if err != nil {
		return "", err
	}
	scale := aboutScale(scr.WidthInPixels, scr.WidthInMillimeters)
	fonts, err := newAboutFonts(scale)
	if err != nil {
		return "", fmt.Errorf("window picker: fonts: %w", err)
	}
	defer fonts.close()
	conn := xu.Conn()

	haveComposite := composite.Init(conn) == nil
	box := pickerThumbBox(scale)
	items := make([]pickerItem, 0, len(windows))
	for _, w := range windows {
		it := pickerItem{ID: w.ID, Title: w.Title}
		var fresh *image.RGBA
		if n, err := strconv.ParseUint(w.ID, 10, 32); err == nil {
			if shot, err := captureWindow(xu, pf, xproto.Window(n), haveComposite); err == nil {
				fresh = fitThumb(shot, box)
			}
		}
		it.Thumb = p.cache.resolve(w.ID, fresh)
		items = append(items, it)
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}

	p.cache.prune(windows)
	layout := layoutPicker(fonts, items, scale, image.Pt(int(scr.WidthInPixels), int(scr.HeightInPixels)))
	items = items[:layout.shown]
	model := newPickerModel(layout)

	w := &xwin{xu: xu, pf: pf, atom: map[string]xproto.Atom{}, maxR: int(xu.Setup().MaximumRequestLength) * 4}
	w.win, _ = xproto.NewWindowId(conn)
	w.gc, _ = xproto.NewGcontextId(conn)
	bg := pf.pack(solid(aboutBg))
	pixel := binary.LittleEndian.Uint32(bg)
	if pf.msbFirst {
		pixel = binary.BigEndian.Uint32(bg)
	}
	const events = xproto.EventMaskExposure | xproto.EventMaskKeyPress | xproto.EventMaskButtonPress |
		xproto.EventMaskButtonRelease | xproto.EventMaskPointerMotion | xproto.EventMaskLeaveWindow
	x, y := (int(scr.WidthInPixels)-layout.size.X)/2, (int(scr.HeightInPixels)-layout.size.Y)/2
	if err := xproto.CreateWindowChecked(conn, scr.RootDepth, w.win, scr.Root, int16(max(x, 0)), int16(max(y, 0)),
		uint16(layout.size.X), uint16(layout.size.Y), 0, xproto.WindowClassInputOutput, scr.RootVisual,
		xproto.CwBackPixel|xproto.CwEventMask, []uint32{pixel, events}).Check(); err != nil {
		return "", fmt.Errorf("window picker: x11: create window: %w", err)
	}
	xproto.CreateGC(conn, w.gc, xproto.Drawable(w.win), 0, nil)
	const title = "Choose a window"
	w.prop("WM_NAME", "STRING", 8, len(title), []byte(title))
	w.prop("_NET_WM_NAME", "UTF8_STRING", 8, len(title), []byte(title))
	w.prop("WM_CLASS", "STRING", 8, 2*len("prezilla")+2, []byte("prezilla\x00prezilla\x00"))
	w.prop("WM_NORMAL_HINTS", "WM_SIZE_HINTS", 32, 18, words32(sizeHints(layout.size.X, layout.size.Y)...))
	w.prop("WM_HINTS", "WM_HINTS", 32, 9, words32(1|2, 1, 1, 0, 0, 0, 0, 0, 0))
	w.prop("WM_PROTOCOLS", "ATOM", 32, 1, words32(uint32(w.intern("WM_DELETE_WINDOW"))))
	w.prop("_NET_WM_WINDOW_TYPE", "ATOM", 32, 1, words32(uint32(w.intern("_NET_WM_WINDOW_TYPE_DIALOG"))))
	w.prop("_NET_WM_STATE", "ATOM", 32, 1, words32(uint32(w.intern("_NET_WM_STATE_ABOVE"))))
	xproto.MapWindow(conn, w.win)

	// Closing the connection is what wakes WaitForEvent, so a watchdog does
	// that when ctx ends; the server then destroys the window.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			xproto.DestroyWindow(conn, w.win)
			conn.Close()
		case <-done:
		}
	}()

	current := renderPicker(layout, fonts, items, model.view)
	w.put(current)
	wmDelete := uint32(w.intern("WM_DELETE_WINDOW"))
	for {
		ev, xerr := conn.WaitForEvent()
		if ev == nil && xerr == nil {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			return "", errors.New("window picker: x11: connection closed")
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if xerr != nil {
			continue
		}
		act := pickNone
		switch e := ev.(type) {
		case xproto.ExposeEvent:
			if e.Count == 0 {
				w.put(current)
			}
		case xproto.KeyPressEvent:
			if sym, shift, ok := w.keysym(e); ok {
				act = model.key(pickerKeyFor(sym, shift))
			}
		case xproto.ButtonPressEvent:
			if e.Detail == 1 {
				act = model.press(image.Pt(int(e.EventX), int(e.EventY)))
			}
		case xproto.ButtonReleaseEvent:
			if e.Detail == 1 {
				act = model.release(image.Pt(int(e.EventX), int(e.EventY)))
			}
		case xproto.MotionNotifyEvent:
			act = model.pointerMove(image.Pt(int(e.EventX), int(e.EventY)))
		case xproto.LeaveNotifyEvent:
			act = model.pointerLeave()
		case xproto.ClientMessageEvent:
			if e.Type == w.intern("WM_PROTOCOLS") && e.Data.Data32[0] == wmDelete {
				act = pickClose
			}
		}
		switch act {
		case pickRedraw:
			current = renderPicker(layout, fonts, items, model.view)
			w.put(current)
		case pickChoose:
			xproto.DestroyWindowChecked(conn, w.win).Check()
			return items[model.view.sel].ID, nil
		case pickClose:
			xproto.DestroyWindowChecked(conn, w.win).Check()
			return "", ErrCancelled
		}
	}
}

// More X keysyms the picker reacts to.
const (
	ksHome, ksLeft, ksUp, ksRight, ksDown, ksEnd = 0xff50, 0xff51, 0xff52, 0xff53, 0xff54, 0xff57
)

// pickerKeyFor maps a keysym (and the Shift state) to the key the model knows.
func pickerKeyFor(sym uint32, shift bool) pickerKey {
	switch sym {
	case ksHome:
		return pkHome
	case ksEnd:
		return pkEnd
	case ksLeft:
		return pkLeft
	case ksRight:
		return pkRight
	case ksUp:
		return pkUp
	case ksDown:
		return pkDown
	}
	switch keyFor(sym, shift) {
	case keyEnter:
		return pkEnter
	case keySpace:
		return pkSpace
	case keyEscape:
		return pkEscape
	case keyTab:
		return pkTab
	case keyShiftTab:
		return pkShiftTab
	}
	return pkOther
}

var _ WindowPicker = (*X11Picker)(nil)

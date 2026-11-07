package system

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math/bits"
	"os/exec"

	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"

	"prezilla/internal/about"
)

// ErrNoDisplay means there is no X display to open a window on; callers fall
// back to the About notification.
var ErrNoDisplay = errors.New("about: no X display")

// X11About shows the About window itself, drawn with the embedded Go fonts
// into an in-memory image and sent to the server with PutImage: no toolkit,
// no cgo, no system fonts.
type X11About struct {
	// Icon is the PNG shown at the top and set as the window icon; nil
	// shows no icon.
	Icon []byte
	// Open opens a URL in the user's browser; nil runs xdg-open. It is only
	// ever called with http(s) URLs. A failure is shown in the window.
	Open func(url string) error
}

// openURL runs xdg-open from "/" (where no relative path can shadow the URL)
// and reaps it in the background.
func openURL(url string) error {
	cmd := exec.Command("xdg-open", url)
	cmd.Dir = "/"
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func decodePNG(b []byte) (image.Image, error) { return png.Decode(bytes.NewReader(b)) }

// pixelFormat is how the server wants ZPixmap pixels: the visual's channel
// masks, 32 bits per pixel, and the setup's image byte order.
type pixelFormat struct {
	red, green, blue uint32
	msbFirst         bool
}

// maskShift returns how far a channel mask sits from bit 0 and its width.
func maskShift(mask uint32) (shift, width uint) {
	return uint(bits.TrailingZeros32(mask)), uint(bits.OnesCount32(mask))
}

// pack converts img to 4 bytes per pixel in the server's format.
func (pf pixelFormat) pack(img *image.RGBA) []byte {
	rs, rw := maskShift(pf.red)
	gs, gw := maskShift(pf.green)
	bs, bw := maskShift(pf.blue)
	b := img.Bounds()
	out := make([]byte, 0, b.Dx()*b.Dy()*4)
	for y := b.Min.Y; y < b.Max.Y; y++ {
		row := img.Pix[(y-b.Min.Y)*img.Stride:]
		for x := 0; x < b.Dx(); x++ {
			// The window is opaque, so premultiplied equals plain RGB.
			r, g, bl := uint32(row[x*4]), uint32(row[x*4+1]), uint32(row[x*4+2])
			v := r>>(8-rw)<<rs | g>>(8-gw)<<gs | bl>>(8-bw)<<bs
			if pf.msbFirst {
				out = binary.BigEndian.AppendUint32(out, v)
			} else {
				out = binary.LittleEndian.AppendUint32(out, v)
			}
		}
	}
	return out
}

// pixelFormatFor checks that the root visual is 24/32-bit TrueColor with a
// 32 bits-per-pixel pixmap format and returns how to pack for it.
func pixelFormatFor(setup *xproto.SetupInfo, scr *xproto.ScreenInfo) (pixelFormat, error) {
	var vis *xproto.VisualInfo
	for _, d := range scr.AllowedDepths {
		for i := range d.Visuals {
			if d.Visuals[i].VisualId == scr.RootVisual {
				vis = &d.Visuals[i]
			}
		}
	}
	if vis == nil || vis.Class != xproto.VisualClassTrueColor ||
		(scr.RootDepth != 24 && scr.RootDepth != 32) ||
		vis.RedMask == 0 || vis.GreenMask == 0 || vis.BlueMask == 0 {
		return pixelFormat{}, fmt.Errorf("about: the X display has no 24-bit TrueColor visual (depth %d)", scr.RootDepth)
	}
	for _, f := range setup.PixmapFormats {
		if f.Depth == scr.RootDepth && f.BitsPerPixel == 32 {
			return pixelFormat{vis.RedMask, vis.GreenMask, vis.BlueMask,
				setup.ImageByteOrder == xproto.ImageOrderMSBFirst}, nil
		}
	}
	return pixelFormat{}, fmt.Errorf("about: the X display has no 32 bits-per-pixel format for depth %d", scr.RootDepth)
}

// rowChunks splits height rows of stride bytes into [from,to) row ranges
// whose PutImage requests stay within maxReq bytes.
func rowChunks(height, stride, maxReq int) [][2]int {
	per := max((maxReq-64)/stride, 1)
	var out [][2]int
	for y := 0; y < height; y += per {
		out = append(out, [2]int{y, min(y+per, height)})
	}
	return out
}

// sizeHints is a WM_NORMAL_HINTS value that fixes the window at w x h: a
// window manager then offers no resizing.
func sizeHints(w, h int) []uint32 {
	const pMinSize, pMaxSize = 16, 32
	hints := make([]uint32, 18)
	hints[0] = pMinSize | pMaxSize
	hints[5], hints[6] = uint32(w), uint32(h) // min
	hints[7], hints[8] = uint32(w), uint32(h) // max
	return hints
}

// words32 encodes format-32 property data in the connection's byte order
// (xgb always connects little-endian).
func words32(v ...uint32) []byte {
	out := make([]byte, 0, len(v)*4)
	for _, x := range v {
		out = binary.LittleEndian.AppendUint32(out, x)
	}
	return out
}

// X keysyms the window reacts to.
const (
	ksSpace, ksTab, ksReturn, ksEscape, ksKPEnter, ksLeftTab = 0x20, 0xff09, 0xff0d, 0xff1b, 0xff8d, 0xfe20
)

// keyFor maps a keysym (and the Shift state) to the key the model knows.
func keyFor(sym uint32, shift bool) aboutKey {
	switch sym {
	case ksReturn, ksKPEnter:
		return keyEnter
	case ksSpace:
		return keySpace
	case ksEscape:
		return keyEscape
	case ksTab:
		if shift {
			return keyShiftTab
		}
		return keyTab
	case ksLeftTab:
		return keyShiftTab
	}
	return keyOther
}

// xwin is the window's X plumbing for one Show.
type xwin struct {
	xu   *xgbutil.XUtil
	win  xproto.Window
	gc   xproto.Gcontext
	pf   pixelFormat
	maxR int
	atom map[string]xproto.Atom
}

func (w *xwin) intern(name string) xproto.Atom {
	if a, ok := w.atom[name]; ok {
		return a
	}
	r, err := xproto.InternAtom(w.xu.Conn(), false, uint16(len(name)), name).Reply()
	if err != nil || r == nil {
		return 0
	}
	w.atom[name] = r.Atom
	return r.Atom
}

func (w *xwin) prop(name, typ string, format byte, n int, data []byte) {
	xproto.ChangeProperty(w.xu.Conn(), xproto.PropModeReplace, w.win, w.intern(name), w.intern(typ),
		format, uint32(n), data)
}

// put sends the whole image, in as many PutImage requests as the server's
// request size needs.
func (w *xwin) put(img *image.RGBA) {
	data := w.pf.pack(img)
	stride := img.Bounds().Dx() * 4
	for _, c := range rowChunks(img.Bounds().Dy(), stride, w.maxR) {
		xproto.PutImage(w.xu.Conn(), xproto.ImageFormatZPixmap, xproto.Drawable(w.win), w.gc,
			uint16(img.Bounds().Dx()), uint16(c[1]-c[0]), 0, int16(c[0]), 0, w.xu.Screen().RootDepth,
			data[c[0]*stride:c[1]*stride])
	}
}

// Show implements AboutWindow. It returns nil once the user closed the
// window, ctx's error when ctx ended first, and an error wrapping
// ErrNoDisplay when there is no X server to talk to. Any other error means
// the display cannot show the window (for example no TrueColor visual).
func (a X11About) Show(ctx context.Context, info about.Info) (err error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	// A library panic must not take the tray down.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("about: x11: internal error: %v", r)
		}
	}()
	xu, cerr := xgbutil.NewConn()
	if cerr != nil {
		return fmt.Errorf("%w: %w", ErrNoDisplay, cerr)
	}
	defer xu.Conn().Close()
	if err := ctx.Err(); err != nil {
		return err
	}
	scr := xu.Screen()
	pf, err := pixelFormatFor(xu.Setup(), scr)
	if err != nil {
		return err
	}
	scale := aboutScale(scr.WidthInPixels, scr.WidthInMillimeters)
	fonts, err := newAboutFonts(scale)
	if err != nil {
		return fmt.Errorf("about: fonts: %w", err)
	}
	defer fonts.close()

	var icon image.Image
	if a.Icon != nil {
		if icon, err = decodePNG(a.Icon); err != nil {
			icon = nil // the window works without its icon
		}
	}
	open := a.Open
	if open == nil {
		open = openURL
	}

	layout := layoutAbout(fonts, info, "", scale)
	model := newAboutModel(layout)
	w := &xwin{xu: xu, pf: pf, atom: map[string]xproto.Atom{},
		maxR: int(xu.Setup().MaximumRequestLength) * 4}
	conn := xu.Conn()

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
		return fmt.Errorf("about: x11: create window: %w", err)
	}
	xproto.CreateGC(conn, w.gc, xproto.Drawable(w.win), 0, nil)

	title := "About " + info.Name
	w.prop("WM_NAME", "STRING", 8, len(title), []byte(title))
	w.prop("_NET_WM_NAME", "UTF8_STRING", 8, len(title), []byte(title))
	w.prop("WM_CLASS", "STRING", 8, 2*len("prezilla")+2, []byte("prezilla\x00prezilla\x00"))
	w.prop("WM_NORMAL_HINTS", "WM_SIZE_HINTS", 32, 18, words32(sizeHints(layout.size.X, layout.size.Y)...))
	w.prop("WM_HINTS", "WM_HINTS", 32, 9, words32(1|2, 1, 1, 0, 0, 0, 0, 0, 0)) // input, normal state
	w.prop("WM_PROTOCOLS", "ATOM", 32, 1, words32(uint32(w.intern("WM_DELETE_WINDOW"))))
	w.prop("_NET_WM_WINDOW_TYPE", "ATOM", 32, 1, words32(uint32(w.intern("_NET_WM_WINDOW_TYPE_DIALOG"))))
	w.prop("_NET_WM_STATE", "ATOM", 32, 1, words32(uint32(w.intern("_NET_WM_STATE_ABOVE"))))
	if icon != nil {
		ic := iconARGB(icon)
		w.prop("_NET_WM_ICON", "CARDINAL", 32, len(ic), words32(ic...))
	}

	// The hand cursor comes from the standard "cursor" font (XC_hand2).
	var hand xproto.Cursor
	if font, err := xproto.NewFontId(conn); err == nil {
		if xproto.OpenFontChecked(conn, font, uint16(len("cursor")), "cursor").Check() == nil {
			if hand, err = xproto.NewCursorId(conn); err == nil {
				const xcHand2 = 60
				xproto.CreateGlyphCursor(conn, hand, font, font, xcHand2, xcHand2+1, 0, 0, 0, 0xffff, 0xffff, 0xffff)
			}
			xproto.CloseFont(conn, font)
		}
	}
	setCursor := func(c xproto.Cursor) {
		xproto.ChangeWindowAttributes(conn, w.win, xproto.CwCursor, []uint32{uint32(c)})
	}

	xproto.MapWindow(conn, w.win)

	// Closing the connection is what wakes WaitForEvent, so a watchdog does
	// that when ctx ends (Quit); the server then destroys the window.
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

	var (
		status  string
		current *image.RGBA
	)
	redraw := func(relayout bool) {
		if relayout {
			l := layoutAbout(fonts, info, status, scale)
			l.size = layout.size // the window never changes size
			layout = l
		}
		current = renderAbout(layout, fonts, icon, model.view)
		w.put(current)
	}
	redraw(false)
	handShown := false
	apply := func(act aboutAct) bool {
		switch act {
		case actClose:
			return true
		case actOpenLink:
			if err := open(info.Website); err != nil {
				status = "Couldn't open a browser. The link is: " + info.Website
				model.view.status = status
				redraw(true)
			}
		case actRedraw:
			redraw(false)
		}
		if hh := model.handHover(); hh != handShown {
			handShown = hh
			if hh && hand != 0 {
				setCursor(hand)
			} else {
				setCursor(0)
			}
		}
		return false
	}

	wmDelete := uint32(w.intern("WM_DELETE_WINDOW"))
	for {
		ev, xerr := conn.WaitForEvent()
		if ev == nil && xerr == nil {
			// The connection is gone: ctx ended, or the server died.
			if err := ctx.Err(); err != nil {
				return err
			}
			return errors.New("about: x11: connection closed")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if xerr != nil {
			continue // a failed request (for example a lost atom) is not fatal
		}
		var act aboutAct
		switch e := ev.(type) {
		case xproto.ExposeEvent:
			if e.Count == 0 {
				w.put(current)
			}
		case xproto.KeyPressEvent:
			act = model.key(w.key(e))
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
				act = actClose
			}
		}
		if apply(act) {
			// Close promptly: destroy the window, then the deferred Close.
			xproto.DestroyWindowChecked(conn, w.win).Check()
			return nil
		}
	}
}

// key looks the pressed key's keysym up (first column of the keyboard
// mapping, which covers every key the window uses).
func (w *xwin) key(e xproto.KeyPressEvent) aboutKey {
	m, err := xproto.GetKeyboardMapping(w.xu.Conn(), e.Detail, 1).Reply()
	if err != nil || m == nil || m.KeysymsPerKeycode == 0 || len(m.Keysyms) == 0 {
		return keyOther
	}
	sym := uint32(m.Keysyms[0])
	return keyFor(sym, e.State&xproto.ModMaskShift != 0)
}

// solid is a 1x1 image of c, for packing a single pixel value.
func solid(c color.RGBA) *image.RGBA {
	return &image.RGBA{Pix: []uint8{c.R, c.G, c.B, c.A}, Stride: 4, Rect: image.Rect(0, 0, 1, 1)}
}

var _ AboutWindow = X11About{}

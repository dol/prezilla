package system

import (
	"context"
	"errors"
	"image"
	"os"
	"testing"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"

	"prezilla/internal/about"
)

// liveX is a test client that fakes input through XTest. The live tests need
// a display with no window manager and PREZILLA_X11_LIVE=1, for example
// `Xvfb :99 -noreset` (without -noreset Xvfb resets and refuses connections
// whenever its last client leaves).
type liveX struct {
	t    *testing.T
	conn *xgb.Conn
	scr  *xproto.ScreenInfo
	lay  aboutLayout
}

func newLiveX(t *testing.T) *liveX {
	t.Helper()
	if os.Getenv("PREZILLA_X11_LIVE") != "1" || os.Getenv("DISPLAY") == "" {
		t.Skip("set PREZILLA_X11_LIVE=1 and DISPLAY (Xvfb, no window manager) to run")
	}
	conn, err := xgb.NewConn()
	if err != nil {
		t.Skip("no X server:", err)
	}
	t.Cleanup(conn.Close)
	if err := xtest.Init(conn); err != nil {
		t.Skip("no XTEST:", err)
	}
	scr := xproto.Setup(conn).DefaultScreen(conn)
	f, err := newAboutFonts(aboutScale(scr.WidthInPixels, scr.WidthInMillimeters))
	if err != nil {
		t.Fatal(err)
	}
	defer f.close()
	lay := layoutAbout(f, about.New("live"), "", aboutScale(scr.WidthInPixels, scr.WidthInMillimeters))
	return &liveX{t, conn, scr, lay}
}

// origin is where X11About puts the window (centered; no window manager).
func (x *liveX) origin() image.Point {
	return image.Pt((int(x.scr.WidthInPixels)-x.lay.size.X)/2, (int(x.scr.HeightInPixels)-x.lay.size.Y)/2)
}

func (x *liveX) moveTo(p image.Point) {
	o := x.origin().Add(p)
	xtest.FakeInput(x.conn, xproto.MotionNotify, 0, 0, x.scr.Root, int16(o.X), int16(o.Y), 0)
	x.conn.Sync()
}

func (x *liveX) click(p image.Point) {
	x.moveTo(p)
	xtest.FakeInput(x.conn, xproto.ButtonPress, 1, 0, x.scr.Root, 0, 0, 0)
	xtest.FakeInput(x.conn, xproto.ButtonRelease, 1, 0, x.scr.Root, 0, 0, 0)
	x.conn.Sync()
}

func (x *liveX) key(sym uint32) {
	setup := xproto.Setup(x.conn)
	n := int(setup.MaxKeycode-setup.MinKeycode) + 1
	m, err := xproto.GetKeyboardMapping(x.conn, setup.MinKeycode, byte(n)).Reply()
	if err != nil {
		x.t.Fatal(err)
	}
	per := int(m.KeysymsPerKeycode)
	for i := 0; i < n; i++ {
		if uint32(m.Keysyms[i*per]) == sym {
			code := byte(i) + byte(setup.MinKeycode)
			xtest.FakeInput(x.conn, xproto.KeyPress, code, 0, x.scr.Root, 0, 0, 0)
			xtest.FakeInput(x.conn, xproto.KeyRelease, code, 0, x.scr.Root, 0, 0, 0)
			x.conn.Sync()
			return
		}
	}
	x.t.Fatalf("no keycode for keysym %#x", sym)
}

// show runs Show in the background and returns its result channel; the window
// is up after a short wait.
func (x *liveX) show(ctx context.Context, a X11About) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- a.Show(ctx, about.New("live")) }()
	time.Sleep(700 * time.Millisecond)
	x.moveTo(image.Pt(5, 5)) // the pointer decides who gets the keys
	return ch
}

func wantDone(t *testing.T, ch <-chan error, want error) {
	t.Helper()
	select {
	case err := <-ch:
		if !errors.Is(err, want) && err != want {
			t.Fatalf("Show returned %v, want %v", err, want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Show did not return")
	}
}

func TestLiveX11AboutEscapeCloses(t *testing.T) {
	x := newLiveX(t)
	ch := x.show(context.Background(), X11About{})
	x.key(ksEscape)
	wantDone(t, ch, nil)
}

func TestLiveX11AboutCloseButtonClick(t *testing.T) {
	x := newLiveX(t)
	ch := x.show(context.Background(), X11About{})
	b := x.lay.button
	x.click(b.Min.Add(b.Size().Div(2)))
	wantDone(t, ch, nil)
}

func TestLiveX11AboutLinkOpensOnClickAndEnter(t *testing.T) {
	x := newLiveX(t)
	opened := make(chan string, 4)
	ch := x.show(context.Background(), X11About{Open: func(u string) error { opened <- u; return nil }})
	l := x.lay.link
	x.click(l.Min.Add(l.Size().Div(2)))
	x.key(ksTab) // focus was on the link after the click: Tab moves to Close
	x.key(ksTab) // and back to the link
	x.key(ksReturn)
	for i := 0; i < 2; i++ {
		select {
		case u := <-opened:
			if u != about.Website {
				t.Errorf("opened %q", u)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("link open %d missing", i+1)
		}
	}
	x.key(ksEscape)
	wantDone(t, ch, nil)
}

func TestLiveX11AboutCancelReturnsCtxErr(t *testing.T) {
	x := newLiveX(t)
	ctx, cancel := context.WithCancel(context.Background())
	ch := x.show(ctx, X11About{})
	cancel()
	wantDone(t, ch, context.Canceled)
}

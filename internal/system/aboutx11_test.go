package system

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"

	"prezilla/internal/about"
)

func TestPixelFormatPack(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 2, 1))
	img.SetRGBA(0, 0, color.RGBA{0x12, 0x34, 0x56, 0xff})
	img.SetRGBA(1, 0, color.RGBA{0xff, 0, 0x80, 0xff})
	le := pixelFormat{red: 0xff0000, green: 0xff00, blue: 0xff}
	if got, want := le.pack(img), []byte{0x56, 0x34, 0x12, 0, 0x80, 0, 0xff, 0}; !bytes.Equal(got, want) {
		t.Errorf("LSB first: % x, want % x", got, want)
	}
	be := le
	be.msbFirst = true
	if got, want := be.pack(img), []byte{0, 0x12, 0x34, 0x56, 0, 0xff, 0, 0x80}; !bytes.Equal(got, want) {
		t.Errorf("MSB first: % x, want % x", got, want)
	}
	// BGR order and a 5-bit channel (a masked visual).
	bgr := pixelFormat{red: 0xff, green: 0xff00, blue: 0xff0000}
	if got := bgr.pack(img)[:4]; !bytes.Equal(got, []byte{0x12, 0x34, 0x56, 0}) {
		t.Errorf("BGR: % x", got)
	}
	deep := pixelFormat{red: 0xf80000, green: 0x00fc00, blue: 0x0000f8}
	if got := deep.pack(img)[4:8]; !bytes.Equal(got, []byte{0x80, 0x00, 0xf8, 0}) {
		t.Errorf("reduced masks: % x", got)
	}
}

func TestPixelFormatPackOfSubImageUsesStride(t *testing.T) {
	big := image.NewRGBA(image.Rect(0, 0, 4, 2))
	big.SetRGBA(0, 1, color.RGBA{1, 2, 3, 255})
	got := pixelFormat{red: 0xff0000, green: 0xff00, blue: 0xff}.pack(big)
	if len(got) != 4*2*4 || !bytes.Equal(got[16:20], []byte{3, 2, 1, 0}) {
		t.Errorf("% x", got)
	}
}

func TestRowChunks(t *testing.T) {
	cases := []struct{ h, stride, max int }{
		{840, 1140 * 4, 262140}, {10, 100, 1 << 20}, {5, 1000, 100}, {1, 4, 64},
	}
	for _, c := range cases {
		next, bytesSent := 0, 0
		for _, r := range rowChunks(c.h, c.stride, c.max) {
			if r[0] != next || r[1] <= r[0] {
				t.Fatalf("%+v: chunk %v after row %d", c, r, next)
			}
			if n := (r[1] - r[0]) * c.stride; n > c.max-64 && r[1]-r[0] > 1 {
				t.Errorf("%+v: chunk %v is %d bytes", c, r, n)
			}
			next = r[1]
			bytesSent += (r[1] - r[0]) * c.stride
		}
		if next != c.h || bytesSent != c.h*c.stride {
			t.Errorf("%+v: covered %d rows", c, next)
		}
	}
}

func visualScreen(class byte, depth byte, masks [3]uint32) (*xproto.SetupInfo, *xproto.ScreenInfo) {
	setup := &xproto.SetupInfo{PixmapFormats: []xproto.Format{{Depth: 1, BitsPerPixel: 1}, {Depth: depth, BitsPerPixel: 32}}}
	scr := &xproto.ScreenInfo{RootVisual: 0x21, RootDepth: depth, AllowedDepths: []xproto.DepthInfo{
		{Depth: depth, Visuals: []xproto.VisualInfo{{VisualId: 0x20, Class: 4}, {VisualId: 0x21, Class: class,
			RedMask: masks[0], GreenMask: masks[1], BlueMask: masks[2]}}}}}
	return setup, scr
}

func TestPixelFormatFor(t *testing.T) {
	rgb := [3]uint32{0xff0000, 0xff00, 0xff}
	for _, depth := range []byte{24, 32} {
		setup, scr := visualScreen(xproto.VisualClassTrueColor, depth, rgb)
		pf, err := pixelFormatFor(setup, scr)
		if err != nil || pf.red != 0xff0000 || pf.msbFirst {
			t.Errorf("depth %d: %+v %v", depth, pf, err)
		}
		setup.ImageByteOrder = xproto.ImageOrderMSBFirst
		if pf, _ := pixelFormatFor(setup, scr); !pf.msbFirst {
			t.Error("byte order ignored")
		}
	}
	bad := map[string]func() (*xproto.SetupInfo, *xproto.ScreenInfo){
		"pseudo color": func() (*xproto.SetupInfo, *xproto.ScreenInfo) {
			return visualScreen(xproto.VisualClassPseudoColor, 24, rgb)
		},
		"depth 16": func() (*xproto.SetupInfo, *xproto.ScreenInfo) {
			return visualScreen(xproto.VisualClassTrueColor, 16, rgb)
		},
		"no masks": func() (*xproto.SetupInfo, *xproto.ScreenInfo) {
			return visualScreen(xproto.VisualClassTrueColor, 24, [3]uint32{})
		},
		"no 32 bpp format": func() (*xproto.SetupInfo, *xproto.ScreenInfo) {
			setup, scr := visualScreen(xproto.VisualClassTrueColor, 24, rgb)
			setup.PixmapFormats[1].BitsPerPixel = 24
			return setup, scr
		},
		"unknown root visual": func() (*xproto.SetupInfo, *xproto.ScreenInfo) {
			setup, scr := visualScreen(xproto.VisualClassTrueColor, 24, rgb)
			scr.RootVisual = 0x99
			return setup, scr
		},
	}
	for name, mk := range bad {
		if _, err := pixelFormatFor(mk()); err == nil {
			t.Errorf("%s: want an error", name)
		}
	}
}

func TestSizeHintsFixTheSize(t *testing.T) {
	h := sizeHints(380, 281)
	if len(h) != 18 || h[0] != 16|32 || h[5] != 380 || h[6] != 281 || h[7] != 380 || h[8] != 281 {
		t.Errorf("%v", h)
	}
}

func TestWords32(t *testing.T) {
	if got := words32(1, 0x01020304); !bytes.Equal(got, []byte{1, 0, 0, 0, 4, 3, 2, 1}) {
		t.Errorf("% x", got)
	}
}

func TestKeyFor(t *testing.T) {
	cases := []struct {
		sym   uint32
		shift bool
		want  aboutKey
	}{
		{ksReturn, false, keyEnter}, {ksKPEnter, false, keyEnter}, {ksSpace, false, keySpace},
		{ksEscape, false, keyEscape}, {ksTab, false, keyTab}, {ksTab, true, keyShiftTab},
		{ksLeftTab, true, keyShiftTab}, {'a', false, keyOther},
	}
	for _, c := range cases {
		if got := keyFor(c.sym, c.shift); got != c.want {
			t.Errorf("keyFor(%#x, %v) = %v, want %v", c.sym, c.shift, got, c.want)
		}
	}
}

func TestX11AboutNoDisplay(t *testing.T) {
	t.Setenv("DISPLAY", ":9999")
	t.Setenv("XAUTHORITY", "")
	start := time.Now()
	err := X11About{}.Show(context.Background(), about.New("1"))
	if !errors.Is(err, ErrNoDisplay) {
		t.Fatalf("err %v", err)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("took %v", time.Since(start))
	}
}

func TestX11AboutCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (X11About{}).Show(ctx, about.New("1")); !errors.Is(err, context.Canceled) {
		t.Errorf("err %v", err)
	}
}

func TestOpenURLMissingProgram(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := openURL("https://example.org"); err == nil {
		t.Error("want an error without xdg-open")
	}
}

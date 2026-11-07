package system

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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

// fakeAbout is a scripted AboutWindow.
type fakeAbout struct {
	err   error
	calls int
}

func (f *fakeAbout) Show(context.Context, about.Info) error { f.calls++; return f.err }

func TestFallbackAbout(t *testing.T) {
	missing := fmt.Errorf("%w: exec: not found", ErrNoZenity)
	noStart := fmt.Errorf("about: %w: %w", errZenityStart, errors.New("exit status 1: cannot open display"))
	other := errors.New("about: zenity: exit status 2")
	ctxErr := context.Canceled
	secondFail := errors.New("about: no X display")
	cases := []struct {
		name          string
		primary       error
		secondary     error
		wantSecondary int
		want          error
	}{
		{"primary shown", nil, nil, 0, nil},
		{"zenity missing", missing, nil, 1, nil},
		{"zenity cannot start", noStart, nil, 1, nil},
		{"secondary closed with error", missing, secondFail, 1, secondFail},
		{"real zenity failure is final", other, nil, 0, other},
		{"ctx error is final", ctxErr, nil, 0, ctxErr},
	}
	for _, c := range cases {
		p, s := &fakeAbout{err: c.primary}, &fakeAbout{err: c.secondary}
		f := &FallbackAbout{Primary: p, Secondary: s}
		err := f.Show(context.Background(), about.New("1"))
		if !errors.Is(err, c.want) && err != c.want {
			t.Errorf("%s: err %v, want %v", c.name, err, c.want)
		}
		if p.calls != 1 || s.calls != c.wantSecondary {
			t.Errorf("%s: primary %d secondary %d calls", c.name, p.calls, s.calls)
		}
	}
}

func TestFallbackAboutCancelledCtxSkipsSecondary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	p := &fakeAbout{err: ErrNoZenity}
	s := &fakeAbout{}
	cancel()
	err := (&FallbackAbout{Primary: p, Secondary: s}).Show(ctx, about.New("1"))
	if !errors.Is(err, ErrNoZenity) || s.calls != 0 {
		t.Errorf("err %v secondary calls %d", err, s.calls)
	}
}

func TestFallbackAboutNotesOnce(t *testing.T) {
	var notes []error
	f := &FallbackAbout{Primary: &fakeAbout{err: ErrNoZenity}, Secondary: &fakeAbout{},
		Note: func(err error) { notes = append(notes, err) }}
	for i := 0; i < 3; i++ {
		if err := f.Show(context.Background(), about.New("1")); err != nil {
			t.Fatal(err)
		}
	}
	if len(notes) != 1 || !errors.Is(notes[0], ErrNoZenity) {
		t.Errorf("notes %v", notes)
	}
}

// The real ZenityAbout errors drive the fallback: a dead-display start
// failure reaches the secondary window, a user close does not.
func TestFallbackAboutWithRealZenity(t *testing.T) {
	key := zenityKey("--icon=prezilla", wantMarkup)
	for msg, wantSecondary := range map[string]int{"Gtk-WARNING **: cannot open display: :0": 1, "": 0} {
		z, r := newZenity("4.2.1")
		r.Err[key] = fmt.Errorf("zenity: %w: %s", exitErr(1), msg)
		s := &fakeAbout{}
		if err := (&FallbackAbout{Primary: z, Secondary: s}).Show(context.Background(), about.New("1.2.3")); err != nil {
			t.Errorf("%q: %v", msg, err)
		}
		if s.calls != wantSecondary {
			t.Errorf("%q: secondary calls %d, want %d", msg, s.calls, wantSecondary)
		}
	}
	s := &fakeAbout{}
	err := (&FallbackAbout{Primary: ZenityAbout{}, Secondary: s}).Show(context.Background(), about.New("1"))
	if err != nil || s.calls != 1 {
		t.Errorf("no runner: err %v secondary calls %d", err, s.calls)
	}
}

func TestFallbackAboutShouldFallbackOverridesTheDefaultRule(t *testing.T) {
	boom := errors.New("x11: unsupported visual")
	for name, c := range map[string]struct {
		err    error
		should func(error) bool
		second bool
	}{
		"custom rule says yes":             {boom, func(error) bool { return true }, true},
		"custom rule says no":              {boom, func(error) bool { return false }, false},
		"dead display is not retried":      {ErrNoDisplay, func(err error) bool { return !errors.Is(err, ErrNoDisplay) }, false},
		"default rule keeps zenity logic":  {ErrNoZenity, nil, true},
		"default rule ignores other error": {boom, nil, false},
	} {
		p, s := &fakeAbout{err: c.err}, &fakeAbout{}
		f := &FallbackAbout{Primary: p, Secondary: s, ShouldFallback: c.should}
		_ = f.Show(context.Background(), about.New("1"))
		if (s.calls > 0) != c.second {
			t.Errorf("%s: secondary called %d times, want called=%v", name, s.calls, c.second)
		}
	}
}

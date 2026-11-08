package system

import (
	"fmt"
	"image"
	"image/color"
	"testing"
)

func pickerFonts(t *testing.T) *aboutFonts {
	t.Helper()
	f, err := newAboutFonts(1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.close)
	return f
}

func items(n int) []pickerItem {
	out := make([]pickerItem, n)
	for i := range out {
		out[i] = pickerItem{ID: fmt.Sprint(i), Title: fmt.Sprintf("Window %d", i)}
	}
	return out
}

func TestFitThumbKeepsAspectAndNeverEnlarges(t *testing.T) {
	box := image.Pt(220, 138)
	wide := fitThumb(image.NewRGBA(image.Rect(0, 0, 2200, 600)), box)
	if wide.Bounds().Dx() != 220 || wide.Bounds().Dy() != 60 {
		t.Errorf("wide: %v", wide.Bounds())
	}
	tall := fitThumb(image.NewRGBA(image.Rect(0, 0, 300, 1380)), box)
	if tall.Bounds().Dx() != 30 || tall.Bounds().Dy() != 138 {
		t.Errorf("tall: %v", tall.Bounds())
	}
	small := fitThumb(image.NewRGBA(image.Rect(0, 0, 100, 50)), box)
	if small.Bounds().Dx() != 100 || small.Bounds().Dy() != 50 {
		t.Errorf("small: %v", small.Bounds())
	}
	if fitThumb(image.NewRGBA(image.Rect(0, 0, 0, 0)), box) != nil {
		t.Error("empty source must give nil")
	}
}

func TestLayoutPickerGridFitsTheScreen(t *testing.T) {
	f := pickerFonts(t)
	l := layoutPicker(f, items(7), 1, image.Pt(1920, 1080))
	if l.cols != 5 || l.shown != 7 || len(l.cards) != 7 {
		t.Fatalf("cols %d shown %d cards %d", l.cols, l.shown, len(l.cards))
	}
	for i, c := range l.cards {
		if !c.In(image.Rectangle{Max: l.size}) {
			t.Errorf("card %d %v outside window %v", i, c, l.size)
		}
		if !l.thumbs[i].In(c) {
			t.Errorf("thumb %d outside its card", i)
		}
	}
	if l.cards[5].Min.Y <= l.cards[0].Max.Y {
		t.Error("second row must sit below the first")
	}
	// A narrow screen gets fewer columns, and a short one drops cards.
	narrow := layoutPicker(f, items(7), 1, image.Pt(600, 2000))
	if narrow.cols != 2 {
		t.Errorf("narrow cols %d", narrow.cols)
	}
	short := layoutPicker(f, items(15), 1, image.Pt(1920, 400))
	if short.shown >= 15 || short.shown < short.cols {
		t.Errorf("short shown %d", short.shown)
	}
	if one := layoutPicker(f, items(1), 1, image.Pt(1920, 1080)); one.cols != 1 {
		t.Errorf("single card cols %d", one.cols)
	}
	if none := layoutPicker(f, nil, 1, image.Pt(1920, 1080)); none.shown != 0 || none.size.X == 0 {
		t.Errorf("empty: %+v", none.size)
	}
}

func TestPickerKeyboardNavigation(t *testing.T) {
	m := newPickerModel(layoutPicker(pickerFonts(t), items(7), 1, image.Pt(1920, 1080))) // 5 columns
	steps := []struct {
		k    pickerKey
		want int
		act  pickerAct
	}{
		{pkRight, 1, pickRedraw}, {pkLeft, 0, pickRedraw}, {pkLeft, 0, pickNone},
		{pkDown, 5, pickRedraw}, {pkDown, 5, pickNone}, {pkUp, 0, pickRedraw},
		{pkEnd, 6, pickRedraw}, {pkHome, 0, pickRedraw},
		{pkShiftTab, 6, pickRedraw}, {pkTab, 0, pickRedraw},
	}
	for i, s := range steps {
		if act := m.key(s.k); act != s.act || m.view.sel != s.want {
			t.Errorf("step %d: act %v sel %d, want %v %d", i, act, m.view.sel, s.act, s.want)
		}
	}
	if m.key(pkEnter) != pickChoose || m.key(pkSpace) != pickChoose {
		t.Error("Enter and Space choose")
	}
	if m.key(pkEscape) != pickClose {
		t.Error("Esc closes")
	}
	if m.key(pkOther) != pickNone {
		t.Error("other keys do nothing")
	}
}

func TestPickerEmptyListChoosesNothing(t *testing.T) {
	m := newPickerModel(layoutPicker(pickerFonts(t), nil, 1, image.Pt(800, 600)))
	for _, k := range []pickerKey{pkEnter, pkTab, pkRight, pkEnd} {
		if m.key(k) != pickNone {
			t.Errorf("key %v acted on an empty list", k)
		}
	}
	if m.key(pkEscape) != pickClose {
		t.Error("Esc still closes")
	}
}

func TestPickerMouseChoosesOnRelease(t *testing.T) {
	m := newPickerModel(layoutPicker(pickerFonts(t), items(3), 1, image.Pt(1920, 1080)))
	in := func(i int) image.Point { return m.l.cards[i].Min.Add(image.Pt(5, 5)) }
	if m.pointerMove(in(1)) != pickRedraw || m.view.hover != 1 || m.pointerMove(in(1)) != pickNone {
		t.Error("hover")
	}
	m.press(in(2))
	if m.view.sel != 2 || m.view.pressed != 2 {
		t.Errorf("press: %+v", m.view)
	}
	if m.release(in(1)) != pickRedraw || m.view.pressed != -1 {
		t.Error("release on another card must not choose")
	}
	m.press(in(2))
	if m.release(in(2)) != pickChoose || m.view.sel != 2 {
		t.Error("release on the pressed card chooses it")
	}
	m.press(image.Pt(0, 0))
	if m.release(image.Pt(0, 0)) == pickChoose {
		t.Error("clicking outside a card chooses nothing")
	}
	if m.pointerLeave() != pickRedraw || m.view.hover != -1 {
		t.Error("leave clears hover")
	}
}

func count(img *image.RGBA, r image.Rectangle, c color.RGBA) int {
	n := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if img.RGBAAt(x, y) == c {
				n++
			}
		}
	}
	return n
}

func TestRenderPickerDrawsThumbRingAndPlaceholder(t *testing.T) {
	f := pickerFonts(t)
	its := items(2)
	red := image.NewRGBA(image.Rect(0, 0, 100, 60))
	for i := 0; i < len(red.Pix); i += 4 {
		red.Pix[i], red.Pix[i+3] = 0xff, 0xff
	}
	its[0].Thumb = red
	l := layoutPicker(f, its, 1, image.Pt(1920, 1080))
	img := renderPicker(l, f, its, pickerView{sel: 0, hover: -1, pressed: -1})

	if got := count(img, l.thumbs[0], color.RGBA{0xff, 0, 0, 0xff}); got != 100*60 {
		t.Errorf("thumbnail pixels: %d", got)
	}
	ringRect := l.cards[0].Inset(-3)
	if count(img, ringRect, aboutRing) == 0 {
		t.Error("selected card has no ring")
	}
	if count(img, l.cards[1].Inset(-3), aboutRing) != 0 {
		t.Error("unselected card has a ring")
	}
	if count(img, l.thumbs[1], aboutButton) == 0 {
		t.Error("window without pixels has no placeholder")
	}
	// Hover changes the card color.
	hot := renderPicker(l, f, its, pickerView{sel: 0, hover: 1, pressed: -1})
	if count(hot, l.cards[1], aboutButtonHot) == 0 {
		t.Error("hovered card is not highlighted")
	}
}

func TestPixelFormatUnpackInvertsPack(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 3, 2))
	for i := 0; i < 6; i++ {
		src.Pix[i*4], src.Pix[i*4+1], src.Pix[i*4+2], src.Pix[i*4+3] = uint8(i*40), uint8(255-i*40), uint8(i*7), 0xff
	}
	for _, pf := range []pixelFormat{
		{0xff0000, 0x00ff00, 0x0000ff, false},
		{0xff0000, 0x00ff00, 0x0000ff, true},
		{0x0000ff, 0x00ff00, 0xff0000, false},
	} {
		got := pf.unpack(pf.pack(src), 3, 2)
		if got == nil || string(got.Pix) != string(src.Pix) {
			t.Errorf("%+v: %v", pf, got)
		}
	}
	if (pixelFormat{0xff0000, 0xff00, 0xff, false}).unpack([]byte{1, 2, 3}, 3, 2) != nil {
		t.Error("short data must give nil")
	}
}

func TestPickerKeyFor(t *testing.T) {
	cases := []struct {
		sym   uint32
		shift bool
		want  pickerKey
	}{
		{ksLeft, false, pkLeft}, {ksRight, false, pkRight}, {ksUp, false, pkUp}, {ksDown, false, pkDown},
		{ksHome, false, pkHome}, {ksEnd, false, pkEnd}, {ksReturn, false, pkEnter}, {ksKPEnter, false, pkEnter},
		{ksSpace, false, pkSpace}, {ksEscape, false, pkEscape}, {ksTab, false, pkTab}, {ksTab, true, pkShiftTab},
		{ksLeftTab, false, pkShiftTab}, {'a', false, pkOther},
	}
	for _, c := range cases {
		if got := pickerKeyFor(c.sym, c.shift); got != c.want {
			t.Errorf("sym %#x shift %v = %v, want %v", c.sym, c.shift, got, c.want)
		}
	}
}

func TestThumbCacheKeepsTheLastSnapshotOfAMinimizedWindow(t *testing.T) {
	var c thumbCache
	shot := image.NewRGBA(image.Rect(0, 0, 4, 3))
	if got := c.resolve("1", nil); got != nil {
		t.Errorf("nothing remembered yet: %v", got)
	}
	if got := c.resolve("1", shot); got != shot {
		t.Error("a fresh capture is used")
	}
	if got := c.resolve("1", nil); got != shot {
		t.Error("a window without pixels shows its last snapshot")
	}
	newer := image.NewRGBA(image.Rect(0, 0, 8, 6))
	c.resolve("1", newer)
	if got := c.resolve("1", nil); got != newer {
		t.Error("the newest capture replaces the old one")
	}
	c.resolve("2", shot)
	c.prune([]Window{{ID: "2"}})
	if c.resolve("1", nil) != nil || c.resolve("2", nil) != shot {
		t.Error("prune keeps only the listed windows")
	}
}

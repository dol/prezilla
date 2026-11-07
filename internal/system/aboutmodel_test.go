package system

import (
	"image"
	"image/color"
	"math"
	"strings"
	"testing"

	"prezilla/internal/about"
	"prezilla/internal/tray/icons"
)

func testFonts(t *testing.T, scale float64) *aboutFonts {
	t.Helper()
	f, err := newAboutFonts(scale)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(f.close)
	return f
}

func TestAboutScale(t *testing.T) {
	cases := []struct {
		px, mm uint16
		want   float64
	}{
		{1280, 339, 1},    // 96 dpi
		{1920, 508, 1},    // 96 dpi
		{3840, 600, 1.75}, // 163 dpi
		{3840, 300, 3},    // clamped
		{1280, 1000, 1},   // very low dpi
		{0, 300, 1},
		{1280, 0, 1},
	}
	for _, c := range cases {
		if got := aboutScale(c.px, c.mm); got != c.want {
			t.Errorf("aboutScale(%d,%d) = %v, want %v", c.px, c.mm, got, c.want)
		}
	}
	if got := aboutScale(2880, 508); got != 1.5 { // 144 dpi
		t.Errorf("144 dpi: %v", got)
	}
}

func TestWrapText(t *testing.T) {
	f := testFonts(t, 1)
	const w = 120
	lines := wrapText(f.body, "a long description that has to wrap onto several lines", w)
	if len(lines) < 3 {
		t.Fatalf("lines %q", lines)
	}
	for _, l := range lines {
		if textWidth(f.body, l) > w {
			t.Errorf("line %q is %d px wide, max %d", l, textWidth(f.body, l), w)
		}
	}
	if got := strings.Join(lines, " "); got != "a long description that has to wrap onto several lines" {
		t.Errorf("text changed: %q", got)
	}
	if got := wrapText(f.body, "one\ntwo", w); len(got) != 2 || got[0] != "one" || got[1] != "two" {
		t.Errorf("newlines: %q", got)
	}
	if got := wrapText(f.body, "", w); len(got) != 1 || got[0] != "" {
		t.Errorf("empty: %q", got)
	}
	// A word wider than a line is broken between runes, losing nothing.
	long := strings.Repeat("W", 40)
	parts := wrapText(f.body, long, w)
	if len(parts) < 2 || strings.Join(parts, "") != long {
		t.Fatalf("long word: %q", parts)
	}
	for _, p := range parts {
		if textWidth(f.body, p) > w {
			t.Errorf("part %q too wide", p)
		}
	}
	// Markup-free: angle brackets and ampersands stay as they are.
	if got := wrapText(f.body, "<b>&amp;</b>", 400); got[0] != "<b>&amp;</b>" {
		t.Errorf("markup: %q", got)
	}
}

func TestClipLines(t *testing.T) {
	f := testFonts(t, 1)
	in := []string{"first line of text", "second line of text that is long", "third"}
	got := clipLines(f.body, append([]string(nil), in...), 2, 150)
	if len(got) != 2 || !strings.HasSuffix(got[1], "…") || textWidth(f.body, got[1]) > 150 {
		t.Errorf("clipped: %q", got)
	}
	if got := clipLines(f.body, in[:2], 2, 150); len(got) != 2 || strings.Contains(got[1], "…") {
		t.Errorf("not clipped: %q", got)
	}
}

func TestLayoutFitsAndCenters(t *testing.T) {
	for _, scale := range []float64{1, 1.5, 2.25, 3} {
		f := testFonts(t, scale)
		l := layoutAbout(f, about.New("dev"), "Couldn't open a browser. The link is: "+about.Website, scale)
		bounds := image.Rectangle{Max: l.size}
		if want := int(math.Round(380 * scale)); l.size.X != want {
			t.Errorf("scale %v: width %d, want %d", scale, l.size.X, want)
		}
		if h := float64(l.size.Y) / scale; h < 255 || h > 300 {
			t.Errorf("scale %v: logical height %.0f, want about 280", scale, h)
		}
		for name, r := range map[string]image.Rectangle{"icon": l.icon, "link": l.link, "button": l.button} {
			if !r.In(bounds) {
				t.Errorf("scale %v: %s %v outside %v", scale, name, r, bounds)
			}
		}
		if got := l.icon.Dx(); got != int(math.Round(64*scale)) {
			t.Errorf("icon %d px", got)
		}
		for _, r := range []image.Rectangle{l.icon, l.link, l.button} {
			if d := (l.size.X - r.Min.X) - r.Max.X; d < -1 || d > 1 {
				t.Errorf("%v is not centered in width %d", r, l.size.X)
			}
		}
		for _, line := range l.lines {
			w := textWidth(f.body, line.text)
			if line.role == roleTitle {
				w = textWidth(f.title, line.text)
			}
			if line.x < 0 || line.x+w > l.size.X || line.y > l.size.Y {
				t.Errorf("scale %v: line %q at %d,%d (%d px) is clipped", scale, line.text, line.x, line.y, w)
			}
		}
		// Top to bottom: icon, lines, button; no overlap with the button.
		if l.icon.Max.Y > l.lines[0].y || l.button.Min.Y < l.lines[len(l.lines)-1].y {
			t.Errorf("scale %v: order icon %v first line %d button %v", scale, l.icon, l.lines[0].y, l.button)
		}
	}
}

func TestLayoutWrapsLongComments(t *testing.T) {
	f := testFonts(t, 1)
	info := about.New("dev")
	short := layoutAbout(f, info, "", 1)
	info.Comments = strings.Repeat("A rather long description of the program. ", 4)
	long := layoutAbout(f, info, "", 1)
	if long.size.Y <= short.size.Y {
		t.Errorf("height %d did not grow from %d", long.size.Y, short.size.Y)
	}
	for _, l := range long.lines {
		if l.x < 0 {
			t.Errorf("line %q starts left of the window", l.text)
		}
	}
	if !long.button.In(image.Rectangle{Max: long.size}) {
		t.Errorf("button %v outside %v", long.button, long.size)
	}
}

func TestLayoutLinkOnlyForWebURLs(t *testing.T) {
	f := testFonts(t, 1)
	info := about.New("dev")
	if l := layoutAbout(f, info, "", 1); l.link.Empty() {
		t.Error("https link must be clickable")
	}
	for _, u := range []string{"file:///etc/passwd", "javascript:alert(1)", "-x", "", "ftp://x"} {
		info.Website = u
		if l := layoutAbout(f, info, "", 1); !l.link.Empty() {
			t.Errorf("%q must not be clickable", u)
		}
	}
	info.Website, info.WebsiteLabel = "http://example.org", ""
	l := layoutAbout(f, info, "", 1)
	if l.link.Empty() || l.lines[l.linkIdx].text != "http://example.org" {
		t.Errorf("label falls back to the URL: %v", l.lines)
	}
}

func TestModelKeyboardAndFocus(t *testing.T) {
	m := newAboutModel(layoutAbout(testFonts(t, 1), about.New("dev"), "", 1))
	if m.view.focus != targetButton {
		t.Fatal("Close must have the focus first")
	}
	if got := m.key(keyEnter); got != actClose {
		t.Errorf("Enter on Close: %v", got)
	}
	if got := m.key(keySpace); got != actClose {
		t.Errorf("Space on Close: %v", got)
	}
	if got := m.key(keyEscape); got != actClose {
		t.Errorf("Esc: %v", got)
	}
	if got := m.key(keyOther); got != actNone {
		t.Errorf("other key: %v", got)
	}
	if got := m.key(keyTab); got != actRedraw || m.view.focus != targetLink {
		t.Errorf("Tab: %v focus %v", got, m.view.focus)
	}
	if got := m.key(keyEnter); got != actOpenLink {
		t.Errorf("Enter on link: %v", got)
	}
	if got := m.key(keySpace); got != actOpenLink {
		t.Errorf("Space on link: %v", got)
	}
	if got := m.key(keyEscape); got != actClose {
		t.Errorf("Esc on link: %v", got)
	}
	if got := m.key(keyShiftTab); got != actRedraw || m.view.focus != targetButton {
		t.Errorf("Shift+Tab: %v focus %v", got, m.view.focus)
	}
}

func TestModelWithoutLinkKeepsFocusOnClose(t *testing.T) {
	info := about.New("dev")
	info.Website = "file:///x"
	l := layoutAbout(testFonts(t, 1), info, "", 1)
	m := newAboutModel(l)
	if got := m.key(keyTab); got != actNone || m.view.focus != targetButton {
		t.Errorf("Tab: %v focus %v", got, m.view.focus)
	}
	c := l.lines[l.linkIdx]
	if got := m.press(image.Pt(c.x+2, c.y-3)); got != actNone {
		t.Errorf("press on plain text: %v", got)
	}
}

func TestModelPointer(t *testing.T) {
	l := layoutAbout(testFonts(t, 1), about.New("dev"), "", 1)
	m := newAboutModel(l)
	inLink := l.link.Min.Add(l.link.Size().Div(2))
	inBtn := l.button.Min.Add(l.button.Size().Div(2))
	away := image.Pt(2, 2)

	if got := m.pointerMove(inLink); got != actRedraw || !m.handHover() {
		t.Errorf("hover link: %v hand %v", got, m.handHover())
	}
	if got := m.pointerMove(inLink.Add(image.Pt(1, 0))); got != actNone {
		t.Errorf("same target: %v", got)
	}
	if got := m.pointerMove(inBtn); got != actRedraw || m.handHover() || m.view.hover != targetButton {
		t.Errorf("hover button: %v", got)
	}
	if got := m.pointerLeave(); got != actRedraw || m.view.hover != targetNone {
		t.Errorf("leave: %v", got)
	}

	// A click needs press and release on the same control.
	if got := m.press(inLink); got != actRedraw || m.view.focus != targetLink || m.view.pressed != targetLink {
		t.Errorf("press link: %v %+v", got, m.view)
	}
	if got := m.release(inLink); got != actOpenLink {
		t.Errorf("release on link: %v", got)
	}
	m.press(inLink)
	if got := m.release(inBtn); got != actRedraw {
		t.Errorf("press link, release on Close: %v", got)
	}
	m.press(away)
	if got := m.release(inBtn); got != actNone {
		t.Errorf("press outside, release on Close: %v", got)
	}
	m.press(inBtn)
	if got := m.release(away); got != actRedraw {
		t.Errorf("press Close, release outside: %v", got)
	}
	m.press(inBtn)
	if got := m.release(inBtn); got != actClose {
		t.Errorf("click Close: %v", got)
	}
}

func luminance(c color.RGBA) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

func contrast(a, b color.RGBA) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func TestAboutContrast(t *testing.T) {
	text := map[string][2]color.RGBA{
		"text on window":   {aboutText, aboutBg},
		"link on window":   {aboutLink, aboutBg},
		"label on button":  {aboutText, aboutButton},
		"label on hover":   {aboutText, aboutButtonHot},
		"label on pressed": {aboutText, aboutButtonDown},
	}
	for name, p := range text {
		if c := contrast(p[0], p[1]); c < 4.5 {
			t.Errorf("%s: contrast %.2f < 4.5", name, c)
		}
	}
	// Non-text parts need 3:1 against what they sit on.
	if c := contrast(aboutRing, aboutBg); c < 3 {
		t.Errorf("focus ring: %.2f < 3", c)
	}
	if c := contrast(aboutRing, aboutButton); c < 3 {
		t.Errorf("focus ring on button: %.2f < 3", c)
	}
}

func countColor(img *image.RGBA, r image.Rectangle, c color.RGBA) int {
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

func TestRenderStates(t *testing.T) {
	f := testFonts(t, 1)
	l := layoutAbout(f, about.New("dev"), "", 1)
	base := renderAbout(l, f, nil, aboutView{focus: targetNone})
	if base.Bounds().Size() != l.size {
		t.Fatalf("size %v, want %v", base.Bounds().Size(), l.size)
	}
	if base.RGBAAt(1, 1) != aboutBg {
		t.Errorf("background %v", base.RGBAAt(1, 1))
	}
	// Alpha is opaque everywhere, so PutImage never shows garbage.
	for i := 3; i < len(base.Pix); i += 4 {
		if base.Pix[i] != 0xff {
			t.Fatalf("pixel %d is not opaque", i/4)
		}
	}

	// The link text is blue and underlined; the underline gets thicker on hover.
	under := image.Rect(l.link.Min.X, l.lines[l.linkIdx].y, l.link.Max.X, l.lines[l.linkIdx].y+8)
	blue := countColor(base, under, aboutLink)
	if blue == 0 {
		t.Error("no link-colored pixels")
	}
	hover := renderAbout(l, f, nil, aboutView{focus: targetNone, hover: targetLink})
	if got := countColor(hover, under, aboutLink); got <= blue {
		t.Errorf("hover underline %d px not thicker than %d", got, blue)
	}

	// Focus rings appear only around the focused control.
	ringIn := func(img *image.RGBA, r image.Rectangle) int { return countColor(img, l.ringRect(r), aboutRing) }
	if ringIn(base, l.button) != 0 {
		t.Error("ring without focus")
	}
	onBtn := renderAbout(l, f, nil, aboutView{focus: targetButton})
	if ringIn(onBtn, l.button) == 0 {
		t.Error("no ring around Close")
	}
	onLink := renderAbout(l, f, nil, aboutView{focus: targetLink})
	if got := countColor(onLink, l.ringRect(l.link), aboutRing); got <= blue {
		t.Errorf("no ring around the link: %d", got)
	}
	if countColor(onLink, l.ringRect(l.button), aboutRing) != 0 {
		t.Error("ring around Close while the link has the focus")
	}

	// Button states change its fill.
	mid := l.button.Min.Add(image.Pt(5, l.button.Dy()/2))
	for view, want := range map[aboutView]color.RGBA{
		{focus: targetNone}:                          aboutButton,
		{hover: targetButton}:                        aboutButtonHot,
		{hover: targetButton, pressed: targetButton}: aboutButtonDown,
	} {
		if got := renderAbout(l, f, nil, view).RGBAAt(mid.X, mid.Y); got != want {
			t.Errorf("view %+v: button fill %v, want %v", view, got, want)
		}
	}
}

func TestRenderDrawsIconAndStatus(t *testing.T) {
	f := testFonts(t, 1)
	icon, err := decodePNG(icons.Idle)
	if err != nil {
		t.Fatal(err)
	}
	l := layoutAbout(f, about.New("dev"), "", 1)
	img := renderAbout(l, f, icon, aboutView{focus: targetButton})
	if countColor(img, l.icon, aboutBg) == l.icon.Dx()*l.icon.Dy() {
		t.Error("icon area is blank")
	}
	without := layoutAbout(f, about.New("dev"), "", 1)
	with := layoutAbout(f, about.New("dev"), "Couldn't open a browser. The link is: "+about.Website, 1)
	if len(with.lines) != len(without.lines)+2 {
		t.Errorf("status adds %d lines", len(with.lines)-len(without.lines))
	}
	if with.size != without.size || with.button != without.button {
		t.Error("status must not move or resize anything")
	}
}

func TestIconARGB(t *testing.T) {
	img := image.NewNRGBA(image.Rect(0, 0, 2, 1))
	img.SetNRGBA(0, 0, color.NRGBA{R: 0x11, G: 0x22, B: 0x33, A: 0x80})
	img.SetNRGBA(1, 0, color.NRGBA{R: 0xff, A: 0})
	got := iconARGB(img)
	if len(got) != 4 || got[0] != 2 || got[1] != 1 || got[2] != 0x80112233 || got[3]>>24 != 0 {
		t.Errorf("%#x", got)
	}
	png64, err := decodePNG(icons.Idle)
	if err != nil {
		t.Fatal(err)
	}
	if g := iconARGB(png64); len(g) != 2+64*64 || g[0] != 64 || g[1] != 64 {
		t.Errorf("embedded icon: %d words, %d x %d", len(g), g[0], g[1])
	}
}

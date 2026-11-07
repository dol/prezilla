package system

import (
	"image"
	"image/color"
	"math"
	"strings"
	"unicode/utf8"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"prezilla/internal/about"
)

// This file is the pure half of the built-in About window (X11About): layout,
// rendering, hit-testing and the focus/keyboard state machine. Nothing here
// talks to an X server, so all of it is tested directly.

// Colors of the (light) About window. Text, link and the button label meet
// 4.5:1 on their backgrounds, the focus ring 3:1 (see the tests).
var (
	aboutBg         = color.RGBA{0xf6, 0xf5, 0xf4, 0xff}
	aboutText       = color.RGBA{0x2e, 0x34, 0x36, 0xff}
	aboutLink       = color.RGBA{0x1a, 0x5f, 0xb4, 0xff}
	aboutButton     = color.RGBA{0xe6, 0xe6, 0xe6, 0xff}
	aboutButtonHot  = color.RGBA{0xdc, 0xdc, 0xdc, 0xff}
	aboutButtonDown = color.RGBA{0xc8, 0xc8, 0xc8, 0xff}
	aboutBorder     = color.RGBA{0xb8, 0xb8, 0xb8, 0xff}
	aboutRing       = aboutLink
)

// Logical sizes (before the DPI scale).
const (
	aboutWidth  = 380
	aboutPad    = 16
	aboutIconPx = 64
)

// aboutScale derives the drawing scale from the screen's size in pixels and
// millimetres: dpi/96, clamped to 1x..3x and rounded to 0.25. Unknown or
// absurd sizes give 1.
func aboutScale(widthPx, widthMM uint16) float64 {
	if widthPx == 0 || widthMM == 0 {
		return 1
	}
	dpi := float64(widthPx) * 25.4 / float64(widthMM)
	s := math.Round(dpi/96*4) / 4
	return math.Min(3, math.Max(1, s))
}

// aboutFonts are the embedded Go fonts at one scale. A font.Face is not safe
// for concurrent use; one aboutFonts belongs to one window.
type aboutFonts struct {
	body, bold, title font.Face
}

func newAboutFonts(scale float64) (*aboutFonts, error) {
	reg, err := opentype.Parse(goregular.TTF)
	if err != nil {
		return nil, err
	}
	bold, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return nil, err
	}
	face := func(f *opentype.Font, px float64) (font.Face, error) {
		return opentype.NewFace(f, &opentype.FaceOptions{Size: px * scale, DPI: 72, Hinting: font.HintingFull})
	}
	var f aboutFonts
	if f.body, err = face(reg, 13); err != nil {
		return nil, err
	}
	if f.bold, err = face(bold, 13); err != nil {
		return nil, err
	}
	if f.title, err = face(bold, 17); err != nil {
		return nil, err
	}
	return &f, nil
}

func (f *aboutFonts) close() {
	f.body.Close()
	f.bold.Close()
	f.title.Close()
}

// textWidth is the advance of s in pixels.
func textWidth(face font.Face, s string) int {
	return font.MeasureString(face, s).Ceil()
}

// wrapText breaks s into lines no wider than max pixels at word boundaries; a
// single word wider than max is broken between runes. Input newlines are
// kept as breaks. Text is plain: nothing is interpreted as markup.
func wrapText(face font.Face, s string, max int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		flush := func() {
			out = append(out, line)
			line = ""
		}
		for _, word := range strings.Fields(para) {
			cand := word
			if line != "" {
				cand = line + " " + word
			}
			if textWidth(face, cand) <= max {
				line = cand
				continue
			}
			if line != "" {
				flush()
			}
			// A word that does not fit on a line of its own.
			for textWidth(face, word) > max && utf8.RuneCountInString(word) > 1 {
				n := 1
				runes := []rune(word)
				for n < len(runes) && textWidth(face, string(runes[:n+1])) <= max {
					n++
				}
				line = string(runes[:n])
				flush()
				word = string(runes[n:])
			}
			line = word
		}
		flush()
	}
	return out
}

// clipLines keeps at most n lines; when it drops some, the last kept line
// ends in an ellipsis that still fits max pixels.
func clipLines(face font.Face, lines []string, n, max int) []string {
	if len(lines) <= n {
		return lines
	}
	lines = lines[:n]
	last := []rune(lines[n-1])
	for len(last) > 0 && textWidth(face, string(last)+"…") > max {
		last = last[:len(last)-1]
	}
	lines[n-1] = string(last) + "…"
	return lines
}

// textRole picks a line's color and face.
type textRole int

const (
	roleTitle textRole = iota
	roleBody
	roleLink
	roleStatus
)

// textLine is one centered line: x is its left edge, y its baseline.
type textLine struct {
	text string
	role textRole
	x, y int
}

// aboutLayout is where everything goes, in pixels.
type aboutLayout struct {
	size    image.Point
	icon    image.Rectangle
	lines   []textLine
	link    image.Rectangle // zero when the project link is not clickable
	linkIdx int             // index into lines of the link, -1 when none
	button  image.Rectangle
	label   textLine // the "Close" label
	scale   float64
}

func (l aboutLayout) px(v float64) int { return int(math.Round(v * l.scale)) }

// isWebURL reports whether u may be opened in a browser: http(s) only, so an
// About value can never make Prezilla open another scheme.
func isWebURL(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")
}

// linkLabel is the text shown for the project link.
func linkLabel(info about.Info) string {
	if info.WebsiteLabel != "" {
		return info.WebsiteLabel
	}
	return info.Website
}

// layoutAbout positions the content, top to bottom and centered. The window
// is 380 logical px wide; its height follows the content (about 280, taller
// when the comments wrap) and is fixed once the window exists. status, when
// not empty, is shown below the link in at most two lines.
func layoutAbout(f *aboutFonts, info about.Info, status string, scale float64) aboutLayout {
	l := aboutLayout{scale: scale, linkIdx: -1}
	w := l.px(aboutWidth)
	pad := l.px(aboutPad)
	maxW := w - 2*pad
	centered := func(face font.Face, s string) int { return (w - textWidth(face, s)) / 2 }
	lineH := func(face font.Face) int { return face.Metrics().Height.Ceil() + l.px(3) }
	ascent := func(face font.Face) int { return face.Metrics().Ascent.Ceil() }

	y := pad
	ic := l.px(aboutIconPx)
	l.icon = image.Rect((w-ic)/2, y, (w-ic)/2+ic, y+ic)
	y += ic + l.px(10)

	add := func(face font.Face, role textRole, s string) {
		l.lines = append(l.lines, textLine{s, role, centered(face, s), y + ascent(face)})
		y += lineH(face)
	}
	for _, s := range wrapText(f.title, info.Name+" "+info.Version, maxW) {
		add(f.title, roleTitle, s)
	}
	y += l.px(2)
	for _, s := range wrapText(f.body, info.Comments, maxW) {
		add(f.body, roleBody, s)
	}
	y += lineH(f.body) / 2
	for _, s := range wrapText(f.body, "License: "+info.License, maxW) {
		add(f.body, roleBody, s)
	}

	label := linkLabel(info)
	if label = clipLines(f.body, wrapText(f.body, label, maxW), 1, maxW)[0]; label != "" {
		x, base := centered(f.body, label), y+ascent(f.body)
		l.linkIdx = len(l.lines)
		l.lines = append(l.lines, textLine{label, roleLink, x, base})
		if isWebURL(info.Website) {
			// A little padding makes the target easier to hit.
			l.link = image.Rect(x-l.px(2), y, x+textWidth(f.body, label)+l.px(2), y+lineH(f.body))
		}
		y += lineH(f.body)
	}

	// Always room for two status lines, so showing one never resizes anything.
	y += l.px(6)
	st := clipLines(f.body, wrapText(f.body, status, maxW), 2, maxW)
	for i := 0; i < 2; i++ {
		if i < len(st) && status != "" {
			l.lines = append(l.lines, textLine{st[i], roleStatus, centered(f.body, st[i]), y + ascent(f.body)})
		}
		y += lineH(f.body)
	}

	y += l.px(8)
	bw := max(l.px(96), textWidth(f.body, "Close")+2*l.px(24))
	bh := l.px(32)
	l.button = image.Rect((w-bw)/2, y, (w-bw)/2+bw, y+bh)
	bm := f.body.Metrics()
	l.label = textLine{"Close", roleBody, (w - textWidth(f.body, "Close")) / 2,
		y + (bh+bm.Ascent.Ceil()-bm.Descent.Ceil())/2}
	l.size = image.Pt(w, y+bh+pad)
	return l
}

// aboutTarget is a control of the window.
type aboutTarget int

const (
	targetNone aboutTarget = iota
	targetLink
	targetButton
)

// aboutView is everything that changes how the window looks.
type aboutView struct {
	focus, hover, pressed aboutTarget
	status                string
}

// aboutAct is what the model asks of the window after an input.
type aboutAct int

const (
	actNone     aboutAct = iota
	actRedraw            // look changed
	actClose             // close the window
	actOpenLink          // open the project link
)

// aboutKey is a key the window reacts to.
type aboutKey int

const (
	keyOther aboutKey = iota
	keyEnter
	keySpace
	keyEscape
	keyTab
	keyShiftTab
)

// aboutModel is the focus and pointer state machine of the window. Close is
// the default control: it has the focus first.
type aboutModel struct {
	link, button image.Rectangle
	linkOK       bool
	view         aboutView
}

func newAboutModel(l aboutLayout) *aboutModel {
	return &aboutModel{link: l.link, button: l.button, linkOK: !l.link.Empty(),
		view: aboutView{focus: targetButton}}
}

func (m *aboutModel) at(p image.Point) aboutTarget {
	switch {
	case m.linkOK && p.In(m.link):
		return targetLink
	case p.In(m.button):
		return targetButton
	}
	return targetNone
}

// handHover reports whether the pointer is over the link (hand cursor).
func (m *aboutModel) handHover() bool { return m.view.hover == targetLink }

func redrawIf(changed bool) aboutAct {
	if changed {
		return actRedraw
	}
	return actNone
}

// key handles a key press: Esc closes; Tab and Shift+Tab move the focus
// between the link and Close; Enter and Space activate the focused control.
func (m *aboutModel) key(k aboutKey) aboutAct {
	switch k {
	case keyEscape:
		return actClose
	case keyTab, keyShiftTab:
		if !m.linkOK {
			return actNone
		}
		if m.view.focus == targetLink {
			m.view.focus = targetButton
		} else {
			m.view.focus = targetLink
		}
		return actRedraw
	case keyEnter, keySpace:
		if m.view.focus == targetLink {
			return actOpenLink
		}
		return actClose
	}
	return actNone
}

// pointerMove tracks the hover state.
func (m *aboutModel) pointerMove(p image.Point) aboutAct {
	t := m.at(p)
	changed := t != m.view.hover
	m.view.hover = t
	return redrawIf(changed)
}

// pointerLeave clears the hover state when the pointer leaves the window.
func (m *aboutModel) pointerLeave() aboutAct { return m.pointerMove(image.Pt(-1, -1)) }

// press starts a click on a control and moves the focus to it.
func (m *aboutModel) press(p image.Point) aboutAct {
	t := m.at(p)
	if t == targetNone {
		return actNone
	}
	m.view.pressed, m.view.focus, m.view.hover = t, t, t
	return actRedraw
}

// release finishes a click: it counts only when it ends on the control it
// started on.
func (m *aboutModel) release(p image.Point) aboutAct {
	was := m.view.pressed
	if was == targetNone {
		return actNone
	}
	m.view.pressed = targetNone
	m.view.hover = m.at(p)
	if m.at(p) != was {
		return actRedraw
	}
	if was == targetLink {
		return actOpenLink
	}
	return actClose
}

// ringRect is the focus ring's outer rectangle around r.
func (l aboutLayout) ringRect(r image.Rectangle) image.Rectangle { return r.Inset(-l.px(3)) }

// fillRound fills r with c, rounding the corners with radius rad.
func fillRound(img *image.RGBA, r image.Rectangle, rad int, c color.RGBA) {
	r = r.Intersect(img.Bounds())
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if rad > 0 && !inRound(image.Pt(x, y), r, rad) {
				continue
			}
			img.SetRGBA(x, y, c)
		}
	}
}

// inRound reports whether pixel p lies inside r with corner radius rad.
func inRound(p image.Point, r image.Rectangle, rad int) bool {
	cx, cy := p.X, p.Y
	switch {
	case cx < r.Min.X+rad:
		cx = r.Min.X + rad
	case cx >= r.Max.X-rad:
		cx = r.Max.X - rad - 1
	}
	switch {
	case cy < r.Min.Y+rad:
		cy = r.Min.Y + rad
	case cy >= r.Max.Y-rad:
		cy = r.Max.Y - rad - 1
	}
	dx, dy := p.X-cx, p.Y-cy
	return dx*dx+dy*dy <= rad*rad
}

func drawText(img *image.RGBA, face font.Face, c color.RGBA, t textLine) {
	d := font.Drawer{Dst: img, Src: image.NewUniform(c), Face: face, Dot: fixed.P(t.x, t.y)}
	d.DrawString(t.text)
}

// renderAbout draws the window into a new image of l.size. icon may be nil.
func renderAbout(l aboutLayout, f *aboutFonts, icon image.Image, v aboutView) *image.RGBA {
	img := image.NewRGBA(image.Rectangle{Max: l.size})
	fillRound(img, img.Bounds(), 0, aboutBg)
	if icon != nil {
		draw.CatmullRom.Scale(img, l.icon, icon, icon.Bounds(), draw.Over, nil)
	}

	ring := l.px(2)
	rad := l.px(4)
	// Focus rings first, so the controls sit on top of them.
	if v.focus == targetButton {
		o := l.ringRect(l.button)
		fillRound(img, o, rad+l.px(3), aboutRing)
		fillRound(img, o.Inset(ring), rad+l.px(3)-ring, aboutBg)
	}
	if v.focus == targetLink && !l.link.Empty() {
		o := l.ringRect(l.link)
		fillRound(img, o, rad, aboutRing)
		fillRound(img, o.Inset(ring), max(rad-ring, 0), aboutBg)
	}

	bg := aboutButton
	switch {
	case v.pressed == targetButton:
		bg = aboutButtonDown
	case v.hover == targetButton:
		bg = aboutButtonHot
	}
	fillRound(img, l.button, rad, aboutBorder)
	fillRound(img, l.button.Inset(max(l.px(1), 1)), max(rad-1, 0), bg)
	drawText(img, f.body, aboutText, l.label)

	for i, t := range l.lines {
		face, c := f.body, aboutText
		switch t.role {
		case roleTitle:
			face = f.title
		case roleLink:
			if !l.link.Empty() {
				c = aboutLink
			}
		}
		drawText(img, face, c, t)
		if i == l.linkIdx && !l.link.Empty() {
			th := max(l.px(1), 1)
			if v.hover == targetLink {
				th *= 2
			}
			y := t.y + l.px(2)
			fillRound(img, image.Rect(t.x, y, t.x+textWidth(f.body, t.text), y+th), 0, aboutLink)
		}
	}
	return img
}

// iconARGB converts img to the _NET_WM_ICON layout: width, height, then one
// non-premultiplied 0xAARRGGBB value per pixel, row by row.
func iconARGB(img image.Image) []uint32 {
	b := img.Bounds()
	out := make([]uint32, 0, 2+b.Dx()*b.Dy())
	out = append(out, uint32(b.Dx()), uint32(b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			out = append(out, uint32(c.A)<<24|uint32(c.R)<<16|uint32(c.G)<<8|uint32(c.B))
		}
	}
	return out
}

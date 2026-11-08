package system

import (
	"image"
	"image/color"

	"golang.org/x/image/draw"
)

// This file is the pure half of the window picker (X11Picker): layout,
// rendering, hit-testing and the selection state machine. Nothing here talks
// to an X server, so all of it is tested directly. It shares the About
// window's fonts, colors and drawing helpers.

// Logical sizes of the picker (before the DPI scale).
const (
	pickThumbW   = 220
	pickThumbH   = 138
	pickCardPad  = 10
	pickGap      = 12
	pickMargin   = 16
	pickMaxCols  = 5
	pickTitleRow = 2 // lines of title under a thumbnail
)

var pickerCard = color.RGBA{0xff, 0xff, 0xff, 0xff}

// pickerItem is one window card. Thumb is nil for a window without pixels
// (minimized, on another workspace); it must already fit pickerThumbBox.
type pickerItem struct {
	ID, Title string
	Thumb     *image.RGBA
}

// pickerThumbBox is the thumbnail area of a card at scale.
func pickerThumbBox(scale float64) image.Point {
	return image.Pt(int(float64(pickThumbW)*scale+0.5), int(float64(pickThumbH)*scale+0.5))
}

// fitThumb scales src down to fit box, keeping its aspect ratio; a source
// that is empty or already smaller is only copied. It returns nil for an
// empty source.
func fitThumb(src image.Image, box image.Point) *image.RGBA {
	b := src.Bounds()
	if b.Empty() || box.X <= 0 || box.Y <= 0 {
		return nil
	}
	w, h := b.Dx(), b.Dy()
	if w > box.X || h > box.Y {
		if w*box.Y > h*box.X { // wider than the box: limit by width
			h, w = max(h*box.X/w, 1), box.X
		} else {
			w, h = max(w*box.Y/h, 1), box.Y
		}
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst
}

// pickerLayout is where everything goes, in pixels.
type pickerLayout struct {
	size    image.Point
	scale   float64
	heading textLine
	hint    textLine
	// shown is how many items fit on screen; later ones are not drawn.
	shown  int
	cols   int
	cards  []image.Rectangle
	thumbs []image.Rectangle
	titles [][]textLine
}

func (l pickerLayout) px(v float64) int { return int(v*l.scale + 0.5) }

// layoutPicker places up to as many cards as fit on a screen of the given
// size (at most pickMaxCols per row, using 90% of the width and height).
func layoutPicker(f *aboutFonts, items []pickerItem, scale float64, screen image.Point) pickerLayout {
	l := pickerLayout{scale: scale}
	pad, gap, margin := l.px(pickCardPad), l.px(pickGap), l.px(pickMargin)
	box := pickerThumbBox(scale)
	cardW := box.X + 2*pad
	lineH := f.body.Metrics().Height.Ceil()
	cardH := pad + box.Y + pad/2 + pickTitleRow*lineH + pad

	l.cols = max(1, min(pickMaxCols, (screen.X*9/10-2*margin+gap)/(cardW+gap)))
	l.cols = min(l.cols, max(len(items), 1))

	head := f.title.Metrics().Height.Ceil()
	headerH := margin + head + lineH/2 + lineH + margin
	maxRows := max(1, (screen.Y*9/10-headerH-margin+gap)/(cardH+gap))
	l.shown = min(len(items), l.cols*maxRows)
	rows := max((l.shown+l.cols-1)/l.cols, 1)

	gridW := l.cols*cardW + (l.cols-1)*gap
	hintText := "Click a window or press Enter. Esc closes."
	l.size = image.Pt(max(gridW, textWidth(f.title, "Choose a window"), textWidth(f.body, hintText))+2*margin,
		headerH+rows*cardH+(rows-1)*gap+margin)
	left := (l.size.X - gridW) / 2

	l.heading = textLine{text: "Choose a window", role: roleTitle, x: margin, y: margin + f.title.Metrics().Ascent.Ceil()}
	l.hint = textLine{text: hintText, role: roleBody, x: margin, y: margin + head + lineH/2 + f.body.Metrics().Ascent.Ceil()}

	for i := 0; i < l.shown; i++ {
		col, row := i%l.cols, i/l.cols
		origin := image.Pt(left+col*(cardW+gap), headerH+row*(cardH+gap))
		card := image.Rectangle{Min: origin, Max: origin.Add(image.Pt(cardW, cardH))}
		thumb := image.Rect(card.Min.X+pad, card.Min.Y+pad, card.Min.X+pad+box.X, card.Min.Y+pad+box.Y)
		l.cards = append(l.cards, card)
		l.thumbs = append(l.thumbs, thumb)

		lines := clipLines(f.body, wrapText(f.body, items[i].Title, box.X), pickTitleRow, box.X)
		var tl []textLine
		for j, s := range lines {
			tl = append(tl, textLine{text: s, role: roleBody, x: thumb.Min.X,
				y: thumb.Max.Y + pad/2 + j*lineH + f.body.Metrics().Ascent.Ceil()})
		}
		l.titles = append(l.titles, tl)
	}
	return l
}

// pickerKey is a key the picker reacts to.
type pickerKey int

const (
	pkOther pickerKey = iota
	pkEnter
	pkSpace
	pkEscape
	pkTab
	pkShiftTab
	pkLeft
	pkRight
	pkUp
	pkDown
	pkHome
	pkEnd
)

// pickerAct is what the window must do after an input.
type pickerAct int

const (
	pickNone pickerAct = iota
	pickRedraw
	pickChoose // the item at pickerModel.sel was chosen
	pickClose
)

// pickerView is what rendering needs from the model.
type pickerView struct {
	sel, hover, pressed int
}

// pickerModel is the selection and pointer state machine.
type pickerModel struct {
	l    pickerLayout
	view pickerView
}

func newPickerModel(l pickerLayout) *pickerModel {
	return &pickerModel{l: l, view: pickerView{hover: -1, pressed: -1}}
}

// at returns the index of the card under p, or -1.
func (m *pickerModel) at(p image.Point) int {
	for i, c := range m.l.cards {
		if p.In(c) {
			return i
		}
	}
	return -1
}

func (m *pickerModel) move(to int) pickerAct {
	if m.l.shown == 0 || to < 0 || to >= m.l.shown || to == m.view.sel {
		return pickNone
	}
	m.view.sel = to
	return pickRedraw
}

func (m *pickerModel) key(k pickerKey) pickerAct {
	n, sel, cols := m.l.shown, m.view.sel, m.l.cols
	switch k {
	case pkEscape:
		return pickClose
	case pkEnter, pkSpace:
		if n == 0 {
			return pickNone
		}
		return pickChoose
	case pkLeft:
		return m.move(sel - 1)
	case pkRight:
		return m.move(sel + 1)
	case pkUp:
		return m.move(sel - cols)
	case pkDown:
		return m.move(sel + cols)
	case pkHome:
		return m.move(0)
	case pkEnd:
		return m.move(n - 1)
	case pkTab:
		if n > 0 {
			return m.move((sel + 1) % n)
		}
	case pkShiftTab:
		if n > 0 {
			return m.move((sel + n - 1) % n)
		}
	}
	return pickNone
}

func (m *pickerModel) pointerMove(p image.Point) pickerAct {
	if h := m.at(p); h != m.view.hover {
		m.view.hover = h
		return pickRedraw
	}
	return pickNone
}

func (m *pickerModel) pointerLeave() pickerAct { return m.pointerMove(image.Pt(-1, -1)) }

func (m *pickerModel) press(p image.Point) pickerAct {
	m.view.pressed = m.at(p)
	if m.view.pressed >= 0 && m.view.pressed != m.view.sel {
		m.view.sel = m.view.pressed
	}
	return pickRedraw
}

// release chooses the item when the button is let go over the card it was
// pressed on.
func (m *pickerModel) release(p image.Point) pickerAct {
	pressed := m.view.pressed
	m.view.pressed = -1
	if i := m.at(p); i >= 0 && i == pressed {
		m.view.sel = i
		return pickChoose
	}
	return pickRedraw
}

// renderPicker draws the window into a new image of l.size.
func renderPicker(l pickerLayout, f *aboutFonts, items []pickerItem, v pickerView) *image.RGBA {
	img := image.NewRGBA(image.Rectangle{Max: l.size})
	fillRound(img, img.Bounds(), 0, aboutBg)
	drawText(img, f.title, aboutText, l.heading)
	drawText(img, f.body, aboutText, l.hint)

	rad := l.px(6)
	ring := max(l.px(2), 2)
	for i := 0; i < l.shown; i++ {
		card, thumb := l.cards[i], l.thumbs[i]
		if i == v.sel {
			fillRound(img, card.Inset(-ring), rad+ring, aboutRing)
		}
		bg := pickerCard
		switch {
		case v.pressed == i:
			bg = aboutButtonDown
		case v.hover == i:
			bg = aboutButtonHot
		}
		fillRound(img, card, rad, aboutBorder)
		fillRound(img, card.Inset(1), max(rad-1, 0), bg)

		if t := items[i].Thumb; t != nil {
			// Centered in the thumbnail area.
			o := thumb.Min.Add(image.Pt((thumb.Dx()-t.Bounds().Dx())/2, (thumb.Dy()-t.Bounds().Dy())/2))
			draw.Draw(img, image.Rectangle{Min: o, Max: o.Add(t.Bounds().Size())}, t, t.Bounds().Min, draw.Src)
		} else {
			fillRound(img, thumb, l.px(3), aboutButton)
			label := "No preview"
			x := thumb.Min.X + (thumb.Dx()-textWidth(f.body, label))/2
			y := thumb.Min.Y + (thumb.Dy()+f.body.Metrics().Ascent.Ceil())/2
			drawText(img, f.body, aboutText, textLine{text: label, x: x, y: y})
		}
		for _, t := range l.titles[i] {
			drawText(img, f.body, aboutText, t)
		}
	}
	return img
}

package icons

import (
	"bytes"
	"fmt"
	"image/png"
	"math"
	"os"
	"strings"
	"testing"
)

func decode(t *testing.T, data []byte) (w, h int, opaque float64) {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	b := img.Bounds()
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0 {
				n++
			}
		}
	}
	return b.Dx(), b.Dy(), float64(n) / float64(b.Dx()*b.Dy())
}

func TestTrayIconsAre64x64PNG(t *testing.T) {
	for name, data := range map[string][]byte{"idle": Idle, "active": Active} {
		w, h, _ := decode(t, data)
		if w != 64 || h != 64 {
			t.Errorf("%s is %dx%d, want 64x64", name, w, h)
		}
	}
}

func TestTrayIconsDiffer(t *testing.T) {
	if bytes.Equal(Idle, Active) {
		t.Fatal("idle and active icons are identical")
	}
}

func TestTrayIconsAreNotBlank(t *testing.T) {
	for name, data := range map[string][]byte{"idle": Idle, "active": Active} {
		_, _, frac := decode(t, data)
		if frac < 0.05 || frac >= 0.80 {
			t.Errorf("%s opaque fraction %.2f, want within [0.05, 0.80)", name, frac)
		}
	}
}

func TestHicolorSetIsComplete(t *testing.T) {
	for _, s := range []int{16, 22, 24, 32, 48, 64, 128, 256, 512} {
		p := fmt.Sprintf("../../../packaging/icons/hicolor/%dx%d/apps/prezilla.png", s, s)
		data, err := os.ReadFile(p)
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		if w, h, _ := decode(t, data); w != s || h != s {
			t.Errorf("%s is %dx%d, want %dx%d", p, w, h, s, s)
		}
	}
	svg, err := os.ReadFile("../../../packaging/icons/hicolor/scalable/apps/prezilla.svg")
	if err != nil {
		t.Fatal(err)
	}
	if s := string(svg); !strings.HasPrefix(s, "<svg") && !strings.HasPrefix(s, "<?xml") {
		t.Error("scalable svg does not start with <svg or <?xml")
	}
}

// The state must be readable from shape alone: the active screen is filled,
// the idle one is an outline, so active covers clearly more pixels.
func TestActiveIconIsFilledIdleIsOutlined(t *testing.T) {
	_, _, idle := decode(t, Idle)
	_, _, active := decode(t, Active)
	if active < 1.6*idle {
		t.Errorf("active coverage %.3f is not >= 1.6x idle %.3f (ratio %.2f)", active, idle, active/idle)
	}
}

// dominantColour returns the most frequent colour among fully opaque pixels.
func dominantColour(t *testing.T, data []byte) [3]uint8 {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	counts := map[[3]uint8]int{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a != 0xffff {
				continue
			}
			counts[[3]uint8{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8)}]++
		}
	}
	var best [3]uint8
	n := 0
	for c, k := range counts {
		if k > n {
			best, n = c, k
		}
	}
	if n == 0 {
		t.Fatal("no opaque pixels")
	}
	return best
}

// luminance is the WCAG 2 relative luminance of an sRGB colour.
func luminance(c [3]uint8) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.04045 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c[0]) + 0.7152*lin(c[1]) + 0.0722*lin(c[2])
}

func contrast(a, b [3]uint8) float64 {
	la, lb := luminance(a), luminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func TestContrastFormula(t *testing.T) {
	if got := contrast([3]uint8{0, 0, 0}, [3]uint8{255, 255, 255}); math.Abs(got-21) > 1e-9 {
		t.Errorf("black/white %.3f", got)
	}
}

func TestTrayIconColoursKeepContrast(t *testing.T) {
	panels := map[string][3]uint8{"light #f0f0f0": {0xf0, 0xf0, 0xf0}, "dark #2b2b2b": {0x2b, 0x2b, 0x2b}}
	for name, data := range map[string][]byte{"idle": Idle, "active": Active} {
		c := dominantColour(t, data)
		for pname, p := range panels {
			if r := contrast(c, p); r < 3.0 {
				t.Errorf("%s colour #%02X%02X%02X on %s: %.2f:1, want >= 3.0", name, c[0], c[1], c[2], pname, r)
			}
		}
	}
}

package icons

import (
	"bytes"
	"fmt"
	"image/png"
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

package system

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func env(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

func TestGVariantRoundTrip(t *testing.T) {
	for _, s := range []string{"file:///a b/c.png", `it's \ "x"`, ""} {
		if got := gvariantUnquote(gvariantQuote(s)); got != s {
			t.Errorf("%q round-tripped to %q", s, got)
		}
	}
	if got := gvariantUnquote("  plain \n"); got != "plain" {
		t.Errorf("unquoted: %q", got)
	}
}

func TestFileURIRoundTrip(t *testing.T) {
	p := "/home/u/My Pics/a#b.png"
	if got := pathFromURI(fileURI(p)); got != p {
		t.Errorf("got %q via %q", got, fileURI(p))
	}
	if got := pathFromURI("/plain/path.png"); got != "/plain/path.png" {
		t.Errorf("plain: %q", got)
	}
}

func TestDesktopWallpaperGNOME(t *testing.T) {
	r := &FakeRunner{Out: map[string]string{
		"gsettings get org.gnome.desktop.interface color-scheme":      "'default'",
		"gsettings get org.gnome.desktop.background picture-uri":      "'file:///home/u/My%20Pics/a.png'",
		"gsettings get org.gnome.desktop.background picture-uri-dark": "'file:///home/u/dark.png'",
	}}
	d := DesktopWallpaper{Runner: r, Getenv: env("XDG_CURRENT_DESKTOP", "ubuntu:GNOME")}
	if got, err := d.Get(context.Background()); err != nil || got != "/home/u/My Pics/a.png" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := d.Set(context.Background(), "/p/s l.png"); err != nil {
		t.Fatal(err)
	}
	want := "gsettings set org.gnome.desktop.background picture-uri 'file:///p/s%20l.png'"
	if !slices.Contains(r.Calls, want) {
		t.Errorf("missing %q in %v", want, r.Calls)
	}
}

func TestDesktopWallpaperGNOMEDarkUsesDarkKey(t *testing.T) {
	r := &FakeRunner{Out: map[string]string{
		"gsettings get org.gnome.desktop.interface color-scheme":      "'prefer-dark'",
		"gsettings get org.gnome.desktop.background picture-uri-dark": "'file:///home/u/dark.png'",
	}}
	d := DesktopWallpaper{Runner: r, Getenv: env("XDG_CURRENT_DESKTOP", "GNOME")}
	if got, err := d.Get(context.Background()); err != nil || got != "/home/u/dark.png" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := d.Set(context.Background(), "/s.png"); err != nil {
		t.Fatal(err)
	}
	if last := r.Calls[len(r.Calls)-1]; !strings.Contains(last, "picture-uri-dark 'file:///s.png'") {
		t.Errorf("last call %q", last)
	}
}

func TestDesktopWallpaperDconfDesktops(t *testing.T) {
	cases := []struct {
		desktop, key, stored, setValue string
	}{
		{"X-Cinnamon", cinnamonPictureKey, "'file:///o.png'", "'file:///s.png'"},
		{"MATE", matePictureKey, "'/o.png'", "'/s.png'"},
		{"Deepin", deepinPictureKey, "'file:///o.png'", "'file:///s.png'"},
	}
	for _, c := range cases {
		r := &FakeRunner{Out: map[string]string{"dconf read " + c.key: c.stored}}
		d := DesktopWallpaper{Runner: r, Getenv: env("XDG_CURRENT_DESKTOP", c.desktop)}
		if got, err := d.Get(context.Background()); err != nil || got != "/o.png" {
			t.Errorf("%s Get = %q, %v", c.desktop, got, err)
		}
		if err := d.Set(context.Background(), "/s.png"); err != nil || !slices.Contains(r.Calls, "dconf write "+c.key+" "+c.setValue) {
			t.Errorf("%s Set: %v, calls %v", c.desktop, err, r.Calls)
		}
	}
}

func TestDesktopWallpaperKDE(t *testing.T) {
	rc := "[Containments][1][Wallpaper][org.kde.image][General]\nImage=file:///home/u/kde%20bg.png\n" +
		"[Containments][2][Wallpaper][org.kde.slideshow][General]\nImage=file:///other.png\n"
	var script string
	d := DesktopWallpaper{Runner: &FakeRunner{},
		Getenv: env("XDG_CURRENT_DESKTOP", "KDE", "HOME", "/home/u"),
		ReadFile: func(p string) ([]byte, error) {
			if p != "/home/u/.config/"+kdeAppletsrc {
				t.Errorf("read %q", p)
			}
			return []byte(rc), nil
		},
		EvalPlasma: func(_ context.Context, s string) error { script = s; return nil }}
	if got, err := d.Get(context.Background()); err != nil || got != "/home/u/kde bg.png" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := d.Set(context.Background(), "/s.png"); err != nil || !strings.Contains(script, `writeConfig("Image", "file:///s.png")`) {
		t.Fatalf("Set: %v, script %q", err, script)
	}
	boom := errors.New("boom")
	d.EvalPlasma = func(context.Context, string) error { return boom }
	if err := d.Set(context.Background(), "/s.png"); !errors.Is(err, boom) {
		t.Errorf("err %v", err)
	}
}

func TestDesktopWallpaperLXDE(t *testing.T) {
	conf := "[*]\nwallpaper_mode=crop\nwallpaper=/home/u/lx.png\n[other]\nwallpaper=/no.png\n"
	r := &FakeRunner{}
	d := DesktopWallpaper{Runner: r,
		Getenv: env("XDG_CURRENT_DESKTOP", "LXDE", "XDG_CONFIG_HOME", "/cfg", "DESKTOP_SESSION", "Lubuntu"),
		ReadFile: func(p string) ([]byte, error) {
			if p != "/cfg/pcmanfm/Lubuntu/desktop-items-0.conf" {
				t.Errorf("read %q", p)
			}
			return []byte(conf), nil
		}}
	if got, err := d.Get(context.Background()); err != nil || got != "/home/u/lx.png" {
		t.Fatalf("Get = %q, %v", got, err)
	}
	if err := d.Set(context.Background(), "/s.png"); err != nil || !slices.Contains(r.Calls, "pcmanfm -w /s.png") {
		t.Fatalf("Set: %v, %v", err, r.Calls)
	}
}

func TestDesktopWallpaperUnsupported(t *testing.T) {
	for _, v := range []string{"", "sway", "XFCE"} {
		d := DesktopWallpaper{Runner: &FakeRunner{}, Getenv: env("XDG_CURRENT_DESKTOP", v)}
		if _, err := d.Get(context.Background()); !errors.Is(err, ErrUnsupportedDesktop) {
			t.Errorf("%q Get: %v", v, err)
		}
		if err := d.Set(context.Background(), "/s.png"); !errors.Is(err, ErrUnsupportedDesktop) {
			t.Errorf("%q Set: %v", v, err)
		}
	}
}

func TestOsascriptWallpaperQuotesPath(t *testing.T) {
	r := &FakeRunner{}
	if err := (OsascriptWallpaper{Runner: r}).Set(context.Background(), `/a "b"/c.png`); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(r.Calls[0], `set picture to "/a \"b\"/c.png"`) {
		t.Errorf("call %q", r.Calls[0])
	}
}

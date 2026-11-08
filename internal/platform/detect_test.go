package platform

import (
	"strings"
	"testing"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

func env(v string) func(string) string {
	return func(k string) string {
		if k == "XDG_CURRENT_DESKTOP" {
			return v
		}
		return ""
	}
}

func TestDetectXFCE(t *testing.T) {
	for _, v := range []string{"XFCE", "xfce", "X-Cinnamon:XFCE"} {
		got, err := Detect(env(v))
		if err != nil || got != "xfce" {
			t.Errorf("Detect(%q) = %q, %v", v, got, err)
		}
	}
}

func TestDetectUnsupportedNamesValue(t *testing.T) {
	for _, v := range []string{"GNOME", ""} {
		_, err := Detect(env(v))
		want := `unsupported desktop "` + v + `"`
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Detect(%q) err = %v, want %q", v, err, want)
		}
	}
}

func TestSwitchesRespectsConfigOrderAndIgnoresUnknown(t *testing.T) {
	cfg := core.Config{Switches: []string{core.IDMute, "bogus", core.IDIcons, core.IDNotifications}}
	sws, err := Switches("xfce", system.Backends{Runner: &system.FakeRunner{}, Xfconf: &system.FakeXfconf{}, Audio: &system.FakeAudio{}, Windows: &system.FakeWindows{}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range sws {
		ids = append(ids, s.ID())
	}
	if got := strings.Join(ids, ","); got != "mute,icons,notifications" {
		t.Errorf("ids = %s", got)
	}
	if _, err := Switches("gnome", system.Backends{Runner: &system.FakeRunner{}, Xfconf: &system.FakeXfconf{}, Audio: &system.FakeAudio{}, Windows: &system.FakeWindows{}}, cfg); err == nil {
		t.Error("expected error for unknown desktop")
	}
}

func TestAllSwitchesReturnsEveryKnownSwitchInFixedOrder(t *testing.T) {
	cfg := core.Config{Switches: []string{core.IDMute}}
	sws, err := AllSwitches("xfce", system.Backends{Runner: &system.FakeRunner{}, Xfconf: &system.FakeXfconf{}, Audio: &system.FakeAudio{}, Windows: &system.FakeWindows{}}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range sws {
		ids = append(ids, s.ID())
	}
	if got := strings.Join(ids, ","); got != "icons,notifications,presentation,mute,minimize,wallpaper" {
		t.Errorf("ids = %s", got)
	}
	if _, err := AllSwitches("gnome", system.Backends{Runner: &system.FakeRunner{}, Xfconf: &system.FakeXfconf{}, Audio: &system.FakeAudio{}, Windows: &system.FakeWindows{}}, cfg); err == nil {
		t.Error("expected error for unknown desktop")
	}
}

func TestSwitchBuildsOneAndRejectsUnsupportedDesktop(t *testing.T) {
	b := system.Backends{Runner: &system.FakeRunner{}, Xfconf: &system.FakeXfconf{}, Audio: &system.FakeAudio{}, Windows: &system.FakeWindows{}}
	sw, err := Switch("xfce", b, core.IDMute, "")
	if err != nil || sw == nil || sw.ID() != core.IDMute {
		t.Fatalf("got %v, %v", sw, err)
	}
	if _, err := Switch("xfce", b, "bogus", ""); err == nil {
		t.Error("expected error for unknown switch")
	}
	if _, err := Switch("gnome", b, core.IDMute, ""); err == nil || !strings.Contains(err.Error(), `unsupported desktop "gnome"`) {
		t.Errorf("got %v", err)
	}
}

func TestGenericDesktopOffersOnlyWallpaper(t *testing.T) {
	b := system.Backends{Wallpaper: &system.FakeWallpaper{}}
	cfg := core.Config{Switches: []string{core.IDMute, core.IDWallpaper}}
	sws, err := Switches("generic", b, cfg)
	if err != nil || len(sws) != 1 || sws[0].ID() != core.IDWallpaper {
		t.Fatalf("got %v, %v", sws, err)
	}
	if all, err := AllSwitches("generic", b, cfg); err != nil || len(all) != 1 {
		t.Errorf("All: %v, %v", all, err)
	}
	if sw, err := Switch("generic", b, core.IDWallpaper, ""); err != nil || sw == nil {
		t.Errorf("Switch: %v, %v", sw, err)
	}
	if _, err := Switch("generic", b, core.IDMute, ""); err == nil {
		t.Error("expected error for a switch without generic implementation")
	}
}

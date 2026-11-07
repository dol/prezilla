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
	sws, err := Switches("xfce", &system.FakeRunner{}, cfg)
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
	if _, err := Switches("gnome", &system.FakeRunner{}, cfg); err == nil {
		t.Error("expected error for unknown desktop")
	}
}

func TestAllSwitchesReturnsEveryKnownSwitchInFixedOrder(t *testing.T) {
	cfg := core.Config{Switches: []string{core.IDMute}}
	sws, err := AllSwitches("xfce", &system.FakeRunner{}, cfg)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, s := range sws {
		ids = append(ids, s.ID())
	}
	if got := strings.Join(ids, ","); got != "icons,notifications,mute,minimize,wallpaper" {
		t.Errorf("ids = %s", got)
	}
	if _, err := AllSwitches("gnome", &system.FakeRunner{}, cfg); err == nil {
		t.Error("expected error for unknown desktop")
	}
}

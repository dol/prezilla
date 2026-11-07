package xfce

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"prezilla/internal/system"
)

var ctx = context.Background()

func has(t *testing.T, f *system.FakeRunner, call string) {
	t.Helper()
	if !slices.Contains(f.Calls, call) {
		t.Fatalf("missing call %q in %v", call, f.Calls)
	}
}

func lacks(t *testing.T, f *system.FakeRunner, call string) {
	t.Helper()
	if slices.Contains(f.Calls, call) {
		t.Fatalf("unexpected call %q in %v", call, f.Calls)
	}
}

const iconsProp = "xfconf-query -c xfce4-desktop -p /desktop-icons/style"
const dndProp = "xfconf-query -c xfce4-notifyd -p /do-not-disturb"

func TestIconsEnableHidesAndSavesPrevious(t *testing.T) {
	f := &system.FakeRunner{Out: map[string]string{iconsProp: "2"}}
	saved, err := NewIcons(f).Enable(ctx)
	if err != nil || saved != "2" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	has(t, f, iconsProp+" -n -t int -s 0")
}

func TestIconsDisableRestoresPrevious(t *testing.T) {
	f := &system.FakeRunner{}
	if err := NewIcons(f).Disable(ctx, "2"); err != nil {
		t.Fatal(err)
	}
	has(t, f, iconsProp+" -n -t int -s 2")
}

func TestIconsUnsetPropertyIsRemovedOnRestore(t *testing.T) {
	f := &system.FakeRunner{Err: map[string]error{iconsProp: errors.New("unset")}}
	sw := NewIcons(f)
	saved, err := sw.Enable(ctx)
	if err != nil || saved != "" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	has(t, f, iconsProp+" -r")
}

func TestNotificationsAlreadyDNDStaysDNDAfterDisable(t *testing.T) {
	f := &system.FakeRunner{Out: map[string]string{dndProp: "true"}}
	sw := NewNotifications(f)
	saved, err := sw.Enable(ctx)
	if err != nil || saved != "true" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	has(t, f, dndProp+" -n -t bool -s true")
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	if f.Calls[len(f.Calls)-1] != dndProp+" -n -t bool -s true" {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestNotificationsUnreadableDefaultsToFalse(t *testing.T) {
	f := &system.FakeRunner{Err: map[string]error{dndProp: errors.New("unset")}}
	saved, err := NewNotifications(f).Enable(ctx)
	if err != nil || saved != "false" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
}

const muteGet = "env LC_ALL=C pactl get-sink-mute @DEFAULT_SINK@"

func TestMuteAlreadyMutedStaysMuted(t *testing.T) {
	f := &system.FakeRunner{Out: map[string]string{muteGet: "Mute: yes"}}
	sw := NewMute(f)
	saved, err := sw.Enable(ctx)
	if err != nil || saved != "yes" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	lacks(t, f, "pactl set-sink-mute @DEFAULT_SINK@ 0")
}

func TestMuteRestoresUnmuted(t *testing.T) {
	f := &system.FakeRunner{Out: map[string]string{muteGet: "Mute: no"}}
	sw := NewMute(f)
	saved, err := sw.Enable(ctx)
	if err != nil || saved != "no" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	has(t, f, "pactl set-sink-mute @DEFAULT_SINK@ 1")
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	has(t, f, "pactl set-sink-mute @DEFAULT_SINK@ 0")
}

func TestMuteReadFailureErrorMentionsPactl(t *testing.T) {
	f := &system.FakeRunner{Err: map[string]error{muteGet: errors.New("not found")}}
	_, err := NewMute(f).Enable(ctx)
	if err == nil || !strings.Contains(err.Error(), "pactl") {
		t.Fatalf("got %v", err)
	}
}

func TestMuteLocalizedOutputFailsClosed(t *testing.T) {
	f := &system.FakeRunner{Out: map[string]string{muteGet: "Stumm: ja"}}
	_, err := NewMute(f).Enable(ctx)
	if err == nil || !strings.Contains(err.Error(), "pactl get-sink-mute") {
		t.Fatalf("got %v", err)
	}
	lacks(t, f, "pactl set-sink-mute @DEFAULT_SINK@ 1")
}

func TestMuteEmptyOutputFailsClosed(t *testing.T) {
	f := &system.FakeRunner{}
	if _, err := NewMute(f).Enable(ctx); err == nil {
		t.Fatal("expected error")
	}
	lacks(t, f, "pactl set-sink-mute @DEFAULT_SINK@ 1")
}

const wmGet = "env LC_ALL=C wmctrl -m"

func TestMinimizeEnableShowsDesktopAndSavesState(t *testing.T) {
	f := &system.FakeRunner{Out: map[string]string{wmGet: "Name: Xfwm4\nShowing the desktop: OFF"}}
	sw := NewMinimize(f)
	saved, err := sw.Enable(ctx)
	if err != nil || saved != "off" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	has(t, f, "wmctrl -k on")
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	has(t, f, "wmctrl -k off")
}

func TestMinimizeAlreadyShowingDesktopNotRestored(t *testing.T) {
	f := &system.FakeRunner{Out: map[string]string{wmGet: "Name: Xfwm4\nShowing the desktop: ON"}}
	sw := NewMinimize(f)
	saved, err := sw.Enable(ctx)
	if err != nil || saved != "on" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	lacks(t, f, "wmctrl -k off")
}

func TestMinimizeMissingWmctrlErrorMentionsTool(t *testing.T) {
	f := &system.FakeRunner{Err: map[string]error{wmGet: errors.New("not found")}}
	_, err := NewMinimize(f).Enable(ctx)
	if err == nil || !strings.Contains(err.Error(), "wmctrl") {
		t.Fatalf("got %v", err)
	}
}

func TestMinimizeUnparsableOutputFailsClosed(t *testing.T) {
	f := &system.FakeRunner{Out: map[string]string{wmGet: "Name: Xfwm4\nDesktop zeigen: AN"}}
	_, err := NewMinimize(f).Enable(ctx)
	if err == nil || !strings.Contains(err.Error(), "wmctrl -m") {
		t.Fatalf("got %v", err)
	}
	lacks(t, f, "wmctrl -k on")
}

const (
	wpList = "xfconf-query -c xfce4-desktop -l"
	wpA    = "/backdrop/screen0/monitor0/workspace0/last-image"
	wpB    = "/backdrop/screen0/monitor1/workspace0/last-image"
	wpGet  = "xfconf-query -c xfce4-desktop -p "
)

func wpFake() *system.FakeRunner {
	return &system.FakeRunner{Out: map[string]string{
		wpList:      wpA + "\n" + wpB + "\n/desktop-icons/style",
		wpGet + wpA: "/img/a.png",
		wpGet + wpB: "/img/b.png",
	}}
}

func TestWallpaperChangesEveryMonitorAndWorkspaceProperty(t *testing.T) {
	f := wpFake()
	saved, err := NewWallpaper(f, "/img/pres.png").Enable(ctx)
	if err != nil || saved == "" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	has(t, f, wpGet+wpA+" -s /img/pres.png")
	has(t, f, wpGet+wpB+" -s /img/pres.png")
	lacks(t, f, wpGet+"/desktop-icons/style -s /img/pres.png")
}

func TestWallpaperDisableRestoresEachOriginal(t *testing.T) {
	f := wpFake()
	sw := NewWallpaper(f, "/img/pres.png")
	saved, err := sw.Enable(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	has(t, f, wpGet+wpA+" -s /img/a.png")
	has(t, f, wpGet+wpB+" -s /img/b.png")
}

func TestWallpaperDisableContinuesAfterFailure(t *testing.T) {
	f := wpFake()
	sw := NewWallpaper(f, "/img/pres.png")
	saved, _ := sw.Enable(ctx)
	f.Err = map[string]error{wpGet + wpA + " -s /img/a.png": errors.New("boom")}
	err := sw.Disable(ctx, saved)
	if err == nil || !strings.Contains(err.Error(), wpA) {
		t.Fatalf("got %v", err)
	}
	has(t, f, wpGet+wpB+" -s /img/b.png")
}

func TestWallpaperDisableBadSavedErrors(t *testing.T) {
	if err := NewWallpaper(&system.FakeRunner{}, "x").Disable(ctx, "not json"); err == nil {
		t.Fatal("expected error")
	}
}

func TestWallpaperNoPropertiesErrors(t *testing.T) {
	f := &system.FakeRunner{Out: map[string]string{wpList: "/desktop-icons/style"}}
	_, err := NewWallpaper(f, "x").Enable(ctx)
	if err == nil || !strings.Contains(err.Error(), "no wallpaper properties found") {
		t.Fatalf("got %v", err)
	}
}

func TestWallpaperEnableSetFailureRollsBackIncludingFailingProp(t *testing.T) {
	wpC := "/backdrop/screen0/monitor2/workspace0/last-image"
	f := &system.FakeRunner{
		Out: map[string]string{
			wpList:      wpA + "\n" + wpB + "\n" + wpC,
			wpGet + wpA: "/img/a.png",
			wpGet + wpB: "/img/b.png",
			wpGet + wpC: "/img/c.png",
		},
		Err: map[string]error{wpGet + wpB + " -s /img/pres.png": errors.New("boom")},
	}
	if _, err := NewWallpaper(f, "/img/pres.png").Enable(ctx); err == nil {
		t.Fatal("expected error")
	}
	has(t, f, wpGet+wpA+" -s /img/a.png")
	has(t, f, wpGet+wpB+" -s /img/b.png")
	lacks(t, f, wpGet+wpC+" -s /img/pres.png")
}

func TestWallpaperEmptyOriginalIsSkipped(t *testing.T) {
	f := wpFake()
	f.Out[wpGet+wpB] = ""
	sw := NewWallpaper(f, "/img/pres.png")
	saved, err := sw.Enable(ctx)
	if err != nil {
		t.Fatal(err)
	}
	has(t, f, wpGet+wpA+" -s /img/pres.png")
	lacks(t, f, wpGet+wpB+" -s /img/pres.png")
	if strings.Contains(saved, wpB) {
		t.Fatalf("saved %q contains empty prop", saved)
	}
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	has(t, f, wpGet+wpA+" -s /img/a.png")
	for _, c := range f.Calls {
		if strings.HasPrefix(c, wpGet+wpB+" -s") {
			t.Fatalf("empty prop was set: %q", c)
		}
	}
}

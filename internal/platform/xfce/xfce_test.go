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

const (
	sinkA = "alsa_output.pci-0000_00_1f.3.analog-stereo"
	sinkB = "alsa_output.usb-headset.analog-stereo"
)

func hasA(t *testing.T, f *system.FakeAudio, call string) {
	t.Helper()
	if !slices.Contains(f.Calls, call) {
		t.Fatalf("missing call %q in %v", call, f.Calls)
	}
}

func noSetCall(t *testing.T, f *system.FakeAudio) {
	t.Helper()
	for _, c := range f.Calls {
		if strings.HasPrefix(c, "SetSinkMute") {
			t.Fatalf("unexpected set call %q in %v", c, f.Calls)
		}
	}
}

func TestMuteAlreadyMutedStaysMuted(t *testing.T) {
	f := &system.FakeAudio{Default: sinkA, Muted: map[string]bool{sinkA: true}}
	sw := NewMute(f)
	saved, err := sw.Enable(ctx)
	if err != nil || saved != sinkA+"\tyes" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	n := len(f.Calls)
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	if len(f.Calls) != n {
		t.Fatalf("Disable issued calls %v", f.Calls[n:])
	}
	if !f.Muted[sinkA] {
		t.Fatal("sink was unmuted")
	}
}

func TestMuteRestoresUnmuted(t *testing.T) {
	f := &system.FakeAudio{Default: sinkA, Muted: map[string]bool{sinkA: false}, Sinks: []string{sinkA}}
	sw := NewMute(f)
	saved, err := sw.Enable(ctx)
	if err != nil || saved != sinkA+"\tno" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	hasA(t, f, "SetSinkMute "+sinkA+" true")
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	hasA(t, f, "SetSinkMute "+sinkA+" false")
}

func TestMuteRestoresOriginalSinkAfterDefaultChanged(t *testing.T) {
	f := &system.FakeAudio{Default: sinkA, Muted: map[string]bool{sinkA: false, sinkB: false}}
	sw := NewMute(f)
	saved, err := sw.Enable(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.Default = sinkB // headset plugged in: default moved
	f.Sinks = []string{sinkA, sinkB}
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	hasA(t, f, "SetSinkMute "+sinkA+" false")
	if f.Muted[sinkA] || f.Muted[sinkB] {
		t.Fatalf("mute state %v", f.Muted)
	}
	for _, c := range f.Calls {
		if c == "SetSinkMute "+sinkB+" false" || c == "SetSinkMute @DEFAULT_SINK@ false" {
			t.Fatalf("unexpected call %q", c)
		}
	}
}

func TestMuteVanishedSinkIsNotRestored(t *testing.T) {
	// sinkA.monitor and a prefix of sinkA must not count as sinkA; the exact
	// match is the Audio backend's job, so the fake lists only other names.
	f := &system.FakeAudio{Sinks: []string{sinkB}}
	if err := NewMute(f).Disable(ctx, sinkA+"\tno"); err != nil {
		t.Fatal(err)
	}
	hasA(t, f, "SinkExists "+sinkA)
	noSetCall(t, f)
}

func TestMuteLegacySavedValues(t *testing.T) {
	f := &system.FakeAudio{}
	if err := NewMute(f).Disable(ctx, "no"); err != nil {
		t.Fatal(err)
	}
	hasA(t, f, "SetSinkMute @DEFAULT_SINK@ false")
	if slices.Contains(f.Calls, "SinkExists @DEFAULT_SINK@") {
		t.Fatalf("alias existence checked: %v", f.Calls)
	}
	f = &system.FakeAudio{}
	if err := NewMute(f).Disable(ctx, "yes"); err != nil {
		t.Fatal(err)
	}
	if len(f.Calls) != 0 {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestMuteEmptySavedSinkNameIsNothingToRestore(t *testing.T) {
	f := &system.FakeAudio{}
	if err := NewMute(f).Disable(ctx, "\tno"); err != nil {
		t.Fatal(err)
	}
	if len(f.Calls) != 0 {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestMuteDefaultSinkLookupFailureFallsBackToAlias(t *testing.T) {
	f := &system.FakeAudio{
		DefaultErr: errors.New("too old"),
		Muted:      map[string]bool{"@DEFAULT_SINK@": false},
	}
	sw := NewMute(f)
	saved, err := sw.Enable(ctx)
	if err != nil || saved != "@DEFAULT_SINK@\tno" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	hasA(t, f, "SetSinkMute @DEFAULT_SINK@ true")
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	hasA(t, f, "SetSinkMute @DEFAULT_SINK@ false")
	if slices.Contains(f.Calls, "SinkExists @DEFAULT_SINK@") {
		t.Fatalf("alias existence checked: %v", f.Calls)
	}
}

func TestMuteEmptyDefaultSinkFallsBackToAlias(t *testing.T) {
	f := &system.FakeAudio{Default: "  ", Muted: map[string]bool{"@DEFAULT_SINK@": true}}
	saved, err := NewMute(f).Enable(ctx)
	if err != nil || saved != "@DEFAULT_SINK@\tyes" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
}

func TestMuteReadFailureFailsClosed(t *testing.T) {
	f := &system.FakeAudio{Default: sinkA, MutedErr: errors.New("not found")}
	_, err := NewMute(f).Enable(ctx)
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("got %v", err)
	}
	noSetCall(t, f)
}

func TestMuteEnableErrorNamesPulse(t *testing.T) {
	f := &system.FakeAudio{Default: sinkA, MutedErr: errors.New("pulse: connect: no server")}
	_, err := NewMute(f).Enable(ctx)
	if err == nil || !strings.Contains(err.Error(), "pulse") {
		t.Fatalf("got %v", err)
	}
	noSetCall(t, f)
}

func TestMuteSetFailureOnEnableIsReported(t *testing.T) {
	f := &system.FakeAudio{Default: sinkA, Muted: map[string]bool{sinkA: false}, SetErr: errors.New("denied")}
	if _, err := NewMute(f).Enable(ctx); err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("got %v", err)
	}
}

func TestMuteSetFailureTriggersCorrectiveUnmute(t *testing.T) {
	f := &system.FakeAudio{Default: sinkA, Muted: map[string]bool{sinkA: false}, SetErrOnce: errors.New("timed out")}
	if _, err := NewMute(f).Enable(ctx); err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("got %v", err)
	}
	want := []string{"SetSinkMute " + sinkA + " true", "SetSinkMute " + sinkA + " false"}
	var sets []string
	for _, c := range f.Calls {
		if strings.HasPrefix(c, "SetSinkMute") {
			sets = append(sets, c)
		}
	}
	if !slices.Equal(sets, want) {
		t.Fatalf("set calls %v", sets)
	}
}

func TestMuteSetFailureWhenAlreadyMutedHasNoCorrection(t *testing.T) {
	f := &system.FakeAudio{Default: sinkA, Muted: map[string]bool{sinkA: true}, SetErrOnce: errors.New("boom")}
	if _, err := NewMute(f).Enable(ctx); err == nil {
		t.Fatal("expected error")
	}
	n := 0
	for _, c := range f.Calls {
		if strings.HasPrefix(c, "SetSinkMute") {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestMuteUnknownSinkFailsClosed(t *testing.T) {
	f := &system.FakeAudio{}
	if _, err := NewMute(f).Enable(ctx); err == nil {
		t.Fatal("expected error")
	}
	noSetCall(t, f)
}

func TestMinimizeEnableShowsDesktopAndSavesState(t *testing.T) {
	f := &system.FakeWindows{}
	sw := NewMinimize(f)
	saved, err := sw.Enable(ctx)
	if err != nil || saved != "off" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	if !slices.Contains(f.Calls, "SetShowingDesktop true") || !f.Showing {
		t.Fatalf("calls %v showing %v", f.Calls, f.Showing)
	}
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(f.Calls, "SetShowingDesktop false") || f.Showing {
		t.Fatalf("calls %v showing %v", f.Calls, f.Showing)
	}
}

func TestMinimizeAlreadyShowingDesktopNotRestored(t *testing.T) {
	f := &system.FakeWindows{Showing: true}
	sw := NewMinimize(f)
	saved, err := sw.Enable(ctx)
	if err != nil || saved != "on" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	if slices.Contains(f.Calls, "SetShowingDesktop false") || !f.Showing {
		t.Fatalf("calls %v showing %v", f.Calls, f.Showing)
	}
}

func TestMinimizeReadFailureFailsClosed(t *testing.T) {
	f := &system.FakeWindows{GetErr: errors.New("x11: connect: boom")}
	_, err := NewMinimize(f).Enable(ctx)
	if err == nil || !strings.Contains(err.Error(), "x11") {
		t.Fatalf("got %v", err)
	}
	for _, c := range f.Calls {
		if strings.HasPrefix(c, "SetShowingDesktop") {
			t.Fatalf("set called: %v", f.Calls)
		}
	}
}

func TestMinimizeSetFailureIsReported(t *testing.T) {
	f := &system.FakeWindows{SetErr: errors.New("nope")}
	if _, err := NewMinimize(f).Enable(ctx); err == nil {
		t.Fatal("expected error")
	}
}

func TestMinimizeDisableCorruptSavedIsNoop(t *testing.T) {
	f := &system.FakeWindows{Showing: true}
	for _, saved := range []string{"", "garbage", "ON"} {
		if err := NewMinimize(f).Disable(ctx, saved); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.Calls) != 0 {
		t.Fatalf("calls %v", f.Calls)
	}
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

func TestMuteSinkListFailureStillAttemptsUnmute(t *testing.T) {
	f := &system.FakeAudio{SinksErr: errors.New("no server")}
	if err := NewMute(f).Disable(ctx, sinkA+"\tno"); err != nil {
		t.Fatal(err)
	}
	hasA(t, f, "SetSinkMute "+sinkA+" false")
}

func TestWallpaperWithoutImageErrorsWithoutTouchingXfconf(t *testing.T) {
	f := wpFake()
	_, err := NewWallpaper(f, "").Enable(ctx)
	if err == nil || err.Error() != `wallpaper: no image configured (set "wallpaper" in config)` {
		t.Fatalf("got %v", err)
	}
	if len(f.Calls) != 0 {
		t.Fatalf("calls %v", f.Calls)
	}
}

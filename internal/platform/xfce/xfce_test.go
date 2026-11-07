package xfce

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

var ctx = context.Background()

func has(t *testing.T, f *system.FakeXfconf, call string) {
	t.Helper()
	if !slices.Contains(f.Calls, call) {
		t.Fatalf("missing call %q in %v", call, f.Calls)
	}
}

func lacks(t *testing.T, f *system.FakeXfconf, call string) {
	t.Helper()
	if slices.Contains(f.Calls, call) {
		t.Fatalf("unexpected call %q in %v", call, f.Calls)
	}
}

const (
	iconsKey = "xfce4-desktop /desktop-icons/style"
	dndKey   = "xfce4-notifyd /do-not-disturb"
	presKey  = "xfce4-power-manager /xfce4-power-manager/presentation-mode"
)

func props(kv ...any) *system.FakeXfconf {
	f := &system.FakeXfconf{Props: map[string]system.XfconfValue{}}
	for i := 0; i < len(kv); i += 2 {
		f.Props[kv[i].(string)] = kv[i+1].(system.XfconfValue)
	}
	return f
}

func TestIconsEnableHidesAndSavesPrevious(t *testing.T) {
	f := props(iconsKey, system.XfInt(2))
	saved, err := NewIcons(f).Enable(ctx)
	if err != nil || saved != "2" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	has(t, f, "Set "+iconsKey+" int:0")
}

func TestIconsDisableRestoresPrevious(t *testing.T) {
	f := props()
	if err := NewIcons(f).Disable(ctx, "2"); err != nil {
		t.Fatal(err)
	}
	has(t, f, "Set "+iconsKey+" int:2")
}

func TestIconsUnsetPropertyIsRemovedOnRestore(t *testing.T) {
	f := props()
	sw := NewIcons(f)
	saved, err := sw.Enable(ctx)
	if err != nil || saved != "" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	has(t, f, "Reset "+iconsKey)
	if _, ok := f.Props[iconsKey]; ok {
		t.Fatal("property left set")
	}
}

func TestIconsReadFailureFailsClosed(t *testing.T) {
	f := props()
	f.Err = map[string]error{"Get " + iconsKey: errors.New("no daemon")}
	if _, err := NewIcons(f).Enable(ctx); err == nil || !strings.HasPrefix(err.Error(), "read icon style: ") {
		t.Fatalf("got %v", err)
	}
	lacks(t, f, "Set "+iconsKey+" int:0")
}

func TestIconsSetFailureIsReported(t *testing.T) {
	f := props(iconsKey, system.XfInt(1))
	f.Err = map[string]error{"Set " + iconsKey + " int:0": errors.New("boom")}
	if _, err := NewIcons(f).Enable(ctx); err == nil || !strings.HasPrefix(err.Error(), "hide icons: ") {
		t.Fatalf("got %v", err)
	}
}

func TestIconsDisableStrictSavedValue(t *testing.T) {
	for _, saved := range []string{"x", "1.5", "99999999999", "-"} {
		f := props()
		if err := NewIcons(f).Disable(ctx, saved); err == nil {
			t.Errorf("%q accepted", saved)
		}
		if len(f.Calls) != 0 {
			t.Errorf("%q: calls %v", saved, f.Calls)
		}
	}
}

func TestNotificationsAlreadyDNDStaysDNDAfterDisable(t *testing.T) {
	f := props(dndKey, system.XfBool(true))
	sw := NewNotifications(f)
	saved, err := sw.Enable(ctx)
	if err != nil || saved != "true" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	has(t, f, "Set "+dndKey+" bool:true")
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	if f.Calls[len(f.Calls)-1] != "Set "+dndKey+" bool:true" {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestNotificationsUnsetDefaultsToFalse(t *testing.T) {
	f := props()
	sw := NewNotifications(f)
	saved, err := sw.Enable(ctx)
	if err != nil || saved != "false" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	if v := f.Props[dndKey]; v != system.XfBool(false) {
		t.Fatalf("got %+v", v)
	}
}

func TestNotificationsReadFailureFailsClosed(t *testing.T) {
	f := props()
	f.Err = map[string]error{"Get " + dndKey: errors.New("no daemon")}
	if _, err := NewNotifications(f).Enable(ctx); err == nil {
		t.Fatal("expected error")
	}
	lacks(t, f, "Set "+dndKey+" bool:true")
}

func TestNotificationsDisableStrictSavedValue(t *testing.T) {
	for _, saved := range []string{"", "on", "TRUE", "1"} {
		f := props()
		if err := NewNotifications(f).Disable(ctx, saved); err == nil {
			t.Errorf("%q accepted", saved)
		}
		if len(f.Calls) != 0 {
			t.Errorf("%q: calls %v", saved, f.Calls)
		}
	}
}

func TestPresentationSavesAndRestoresEachPreviousState(t *testing.T) {
	cases := []struct {
		name  string
		f     *system.FakeXfconf
		saved string
		after string // call expected on Disable
	}{
		{"unset", props(), "unset", "Reset " + presKey},
		{"false", props(presKey, system.XfBool(false)), "false", "Set " + presKey + " bool:false"},
		{"true", props(presKey, system.XfBool(true)), "true", "Set " + presKey + " bool:true"},
	}
	for _, c := range cases {
		sw := NewPresentation(c.f)
		saved, err := sw.Enable(ctx)
		if err != nil || saved != c.saved {
			t.Fatalf("%s: got (%q, %v)", c.name, saved, err)
		}
		if v := c.f.Props[presKey]; v != system.XfBool(true) {
			t.Fatalf("%s: enable left %+v", c.name, v)
		}
		if err := sw.Disable(ctx, saved); err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := c.f.Calls[len(c.f.Calls)-1]; got != c.after {
			t.Errorf("%s: last call %q want %q", c.name, got, c.after)
		}
	}
}

func TestPresentationRestoredStateMatchesOriginal(t *testing.T) {
	f := props(presKey, system.XfBool(false))
	sw := NewPresentation(f)
	saved, _ := sw.Enable(ctx)
	_ = sw.Disable(ctx, saved)
	if v := f.Props[presKey]; v != system.XfBool(false) {
		t.Fatalf("got %+v", v)
	}
	g := props()
	saved, _ = NewPresentation(g).Enable(ctx)
	_ = NewPresentation(g).Disable(ctx, saved)
	if _, ok := g.Props[presKey]; ok {
		t.Fatal("unset property was not reset")
	}
}

func TestPresentationReadFailureFailsClosed(t *testing.T) {
	f := props()
	f.Err = map[string]error{"Get " + presKey: errors.New("no daemon")}
	if _, err := NewPresentation(f).Enable(ctx); err == nil || !strings.HasPrefix(err.Error(), "read presentation mode: ") {
		t.Fatalf("got %v", err)
	}
	lacks(t, f, "Set "+presKey+" bool:true")
}

func TestPresentationUnexpectedTypeFailsClosed(t *testing.T) {
	f := props(presKey, system.XfString("yes"))
	if _, err := NewPresentation(f).Enable(ctx); err == nil {
		t.Fatal("expected error")
	}
	lacks(t, f, "Set "+presKey+" bool:true")
}

func TestPresentationSetFailureIsReported(t *testing.T) {
	f := props()
	f.Err = map[string]error{"Set " + presKey + " bool:true": errors.New("boom")}
	if _, err := NewPresentation(f).Enable(ctx); err == nil || !strings.HasPrefix(err.Error(), "set presentation mode: ") {
		t.Fatalf("got %v", err)
	}
}

func TestPresentationDisableStrictSavedValue(t *testing.T) {
	for _, saved := range []string{"", "on", "TRUE", "Unset"} {
		f := props()
		if err := NewPresentation(f).Disable(ctx, saved); err == nil {
			t.Errorf("%q accepted", saved)
		}
		if len(f.Calls) != 0 {
			t.Errorf("%q: calls %v", saved, f.Calls)
		}
	}
}

func TestPresentationEngineRoundTrip(t *testing.T) {
	f := props(presKey, system.XfBool(false))
	eng := core.NewEngine([]core.Switch{NewPresentation(f)}, core.NewStore(t.TempDir()+"/state.json"), time.Now)
	if err := eng.Enable(ctx, 0); err != nil {
		t.Fatal(err)
	}
	if v := f.Props[presKey]; v != system.XfBool(true) {
		t.Fatalf("not enabled: %+v", v)
	}
	if err := eng.Disable(ctx); err != nil {
		t.Fatal(err)
	}
	if v := f.Props[presKey]; v != system.XfBool(false) {
		t.Fatalf("not restored: %+v", v)
	}
}

func TestPresentationEngineRollsBackWhenStateCannotBeSaved(t *testing.T) {
	blocker := t.TempDir() + "/file"
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	f := props()
	eng := core.NewEngine([]core.Switch{NewPresentation(f)}, core.NewStore(blocker+"/state.json"), time.Now)
	if err := eng.Enable(ctx, 0); err == nil {
		t.Fatal("expected error")
	}
	if _, ok := f.Props[presKey]; ok {
		t.Fatal("unset property was not reset by the rollback")
	}
}

func TestPresentationSetFailureTriggersCorrectiveRestore(t *testing.T) {
	f := props(presKey, system.XfBool(false))
	f.Err = map[string]error{"Set " + presKey + " bool:true": errors.New("boom")}
	if _, err := NewPresentation(f).Enable(ctx); err == nil {
		t.Fatal("expected error")
	}
	has(t, f, "Set "+presKey+" bool:false")
}

func TestPresentationSetFailureWhenAlreadyOnHasNoCorrection(t *testing.T) {
	f := props(presKey, system.XfBool(true))
	f.Err = map[string]error{"Set " + presKey + " bool:true": errors.New("boom")}
	if _, err := NewPresentation(f).Enable(ctx); err == nil {
		t.Fatal("expected error")
	}
	if len(f.Calls) != 2 {
		t.Fatalf("calls %v", f.Calls)
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

func TestMinimizeSetFailureTriggersCorrectiveRestore(t *testing.T) {
	f := &system.FakeWindows{SetErrOnce: errors.New("timed out")}
	saved, err := NewMinimize(f).Enable(ctx)
	if err == nil || !strings.Contains(err.Error(), "timed out") || saved != "" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	want := []string{"ShowingDesktop", "VisibleWindows", "SetShowingDesktop true", "SetShowingDesktop false"}
	if !slices.Equal(f.Calls, want) {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestMinimizeCorrectiveRunsAfterCallerGaveUp(t *testing.T) {
	f := &system.FakeWindows{SetErrOnce: errors.New("x11: context canceled")}
	cctx, cancel := context.WithCancel(ctx)
	f.OnSet = func(c context.Context, show bool) {
		if show {
			cancel()
		} else if c.Err() != nil {
			t.Errorf("corrective ran on a done context: %v", c.Err())
		} else if dl, ok := c.Deadline(); !ok || time.Until(dl) > core.CorrectiveTimeout {
			t.Errorf("corrective deadline %v %v", dl, ok)
		}
	}
	if _, err := NewMinimize(f).Enable(cctx); err == nil {
		t.Fatal("expected error")
	}
	if n := len(f.Calls); n != 4 {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestMinimizeSetFailureWhenAlreadyShowingHasNoCorrection(t *testing.T) {
	f := &system.FakeWindows{Showing: true, SetErrOnce: errors.New("boom")}
	if _, err := NewMinimize(f).Enable(ctx); err == nil {
		t.Fatal("expected error")
	}
	want := []string{"ShowingDesktop", "SetShowingDesktop true"}
	if !slices.Equal(f.Calls, want) {
		t.Fatalf("calls %v", f.Calls)
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
	wpA = "/backdrop/screen0/monitor0/workspace0/last-image"
	wpB = "/backdrop/screen0/monitor1/workspace0/last-image"
	dsk = "xfce4-desktop "
)

func wpFake() *system.FakeXfconf {
	return props(
		dsk+wpA, system.XfString("/img/a.png"),
		dsk+wpB, system.XfString("/img/b.png"),
		dsk+"/backdrop/screen0/monitor0/workspace0/image-style", system.XfInt(5),
		dsk+"/desktop-icons/style", system.XfInt(2),
	)
}

func setS(prop, v string) string { return "Set " + dsk + prop + " string:" + v }

func TestWallpaperChangesEveryMonitorAndWorkspaceProperty(t *testing.T) {
	f := wpFake()
	saved, err := NewWallpaper(f, "/img/pres.png").Enable(ctx)
	if err != nil || saved == "" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	has(t, f, setS(wpA, "/img/pres.png"))
	has(t, f, setS(wpB, "/img/pres.png"))
	lacks(t, f, setS("/desktop-icons/style", "/img/pres.png"))
	if f.Props[dsk+"/backdrop/screen0/monitor0/workspace0/image-style"] != system.XfInt(5) {
		t.Error("other backdrop property touched")
	}
}

func TestWallpaperSavedStateIsJSONOfOriginals(t *testing.T) {
	saved, err := NewWallpaper(wpFake(), "/img/pres.png").Enable(ctx)
	want := `{"` + wpA + `":"/img/a.png","` + wpB + `":"/img/b.png"}`
	if err != nil || saved != want {
		t.Fatalf("got (%q, %v) want %q", saved, err, want)
	}
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
	has(t, f, setS(wpA, "/img/a.png"))
	has(t, f, setS(wpB, "/img/b.png"))
}

func TestWallpaperDisableContinuesAfterFailure(t *testing.T) {
	f := wpFake()
	sw := NewWallpaper(f, "/img/pres.png")
	saved, _ := sw.Enable(ctx)
	f.Err = map[string]error{setS(wpA, "/img/a.png"): errors.New("boom")}
	err := sw.Disable(ctx, saved)
	if err == nil || !strings.Contains(err.Error(), wpA) {
		t.Fatalf("got %v", err)
	}
	has(t, f, setS(wpB, "/img/b.png"))
}

func TestWallpaperDisableBadSavedErrors(t *testing.T) {
	if err := NewWallpaper(&system.FakeXfconf{}, "x").Disable(ctx, "not json"); err == nil {
		t.Fatal("expected error")
	}
}

func TestWallpaperNoPropertiesErrors(t *testing.T) {
	f := props(dsk+"/desktop-icons/style", system.XfInt(2))
	_, err := NewWallpaper(f, "x").Enable(ctx)
	if err == nil || !strings.Contains(err.Error(), "no wallpaper properties found") {
		t.Fatalf("got %v", err)
	}
}

func TestWallpaperListFailureIsReported(t *testing.T) {
	f := wpFake()
	f.Err = map[string]error{"List " + dsk + "/backdrop": errors.New("no daemon")}
	if _, err := NewWallpaper(f, "x").Enable(ctx); err == nil || !strings.HasPrefix(err.Error(), "list wallpapers: ") {
		t.Fatalf("got %v", err)
	}
}

func TestWallpaperEnableSetFailureRollsBackIncludingFailingProp(t *testing.T) {
	wpC := "/backdrop/screen0/monitor2/workspace0/last-image"
	f := wpFake()
	f.Props[dsk+wpC] = system.XfString("/img/c.png")
	f.Err = map[string]error{setS(wpB, "/img/pres.png"): errors.New("boom")}
	if _, err := NewWallpaper(f, "/img/pres.png").Enable(ctx); err == nil {
		t.Fatal("expected error")
	}
	has(t, f, setS(wpA, "/img/a.png"))
	has(t, f, setS(wpB, "/img/b.png"))
	lacks(t, f, setS(wpC, "/img/pres.png"))
}

func TestWallpaperEmptyOriginalIsSkipped(t *testing.T) {
	f := wpFake()
	f.Props[dsk+wpB] = system.XfString("")
	sw := NewWallpaper(f, "/img/pres.png")
	saved, err := sw.Enable(ctx)
	if err != nil {
		t.Fatal(err)
	}
	has(t, f, setS(wpA, "/img/pres.png"))
	lacks(t, f, setS(wpB, "/img/pres.png"))
	if strings.Contains(saved, wpB) {
		t.Fatalf("saved %q contains empty prop", saved)
	}
	if err := sw.Disable(ctx, saved); err != nil {
		t.Fatal(err)
	}
	has(t, f, setS(wpA, "/img/a.png"))
	for _, c := range f.Calls {
		if strings.HasPrefix(c, "Set "+dsk+wpB) {
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

func TestMinimizeEnableSavesVisibleWindows(t *testing.T) {
	f := &system.FakeWindows{Visible: []uint32{10, 20, 30}}
	saved, err := NewMinimize(f).Enable(ctx)
	if err != nil || saved != "off:10,20,30" {
		t.Fatalf("got (%q, %v)", saved, err)
	}
	want := []string{"ShowingDesktop", "VisibleWindows", "SetShowingDesktop true"}
	if !slices.Equal(f.Calls, want) {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestMinimizeEnableVisibleWindowsFailureFallsBack(t *testing.T) {
	f := &system.FakeWindows{VisibleErr: errors.New("x11: boom")}
	saved, err := NewMinimize(f).Enable(ctx)
	if err != nil || saved != "off" || !f.Showing {
		t.Fatalf("got (%q, %v) showing %v", saved, err, f.Showing)
	}
}

func TestMinimizeEnableAlreadyShowingDoesNotListWindows(t *testing.T) {
	f := &system.FakeWindows{Showing: true, Visible: []uint32{1}}
	saved, err := NewMinimize(f).Enable(ctx)
	if err != nil || saved != "on" || slices.Contains(f.Calls, "VisibleWindows") {
		t.Fatalf("got (%q, %v) calls %v", saved, err, f.Calls)
	}
}

func TestMinimizeDisableWMStillShowingSetsFalseThenRestores(t *testing.T) {
	f := &system.FakeWindows{Showing: true}
	if err := NewMinimize(f).Disable(ctx, "off:10,20"); err != nil {
		t.Fatal(err)
	}
	want := []string{"ShowingDesktop", "SetShowingDesktop false", "RestoreWindows 10,20"}
	if !slices.Equal(f.Calls, want) || f.Showing {
		t.Fatalf("calls %v showing %v", f.Calls, f.Showing)
	}
}

func TestMinimizeDisableWMAlreadyClearedOnlyRestores(t *testing.T) {
	f := &system.FakeWindows{Showing: false}
	if err := NewMinimize(f).Disable(ctx, "off:10,20"); err != nil {
		t.Fatal(err)
	}
	want := []string{"ShowingDesktop", "RestoreWindows 10,20"}
	if !slices.Equal(f.Calls, want) {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestMinimizeDisableLegacyOffOnlyClearsFlag(t *testing.T) {
	f := &system.FakeWindows{Showing: true}
	if err := NewMinimize(f).Disable(ctx, "off"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"SetShowingDesktop false"}; !slices.Equal(f.Calls, want) {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestMinimizeDisableOnNeverRestores(t *testing.T) {
	f := &system.FakeWindows{Showing: true}
	if err := NewMinimize(f).Disable(ctx, "on"); err != nil || len(f.Calls) != 0 {
		t.Fatalf("err %v calls %v", err, f.Calls)
	}
}

func TestMinimizeDisableCorruptIdsIsNoop(t *testing.T) {
	f := &system.FakeWindows{Showing: true}
	for _, saved := range []string{"off:", "off:1,", "off:,1", "off:a", "off:-1", "off:01", "off:4294967296", "off: 1", "off;1", "on:1"} {
		if err := NewMinimize(f).Disable(ctx, saved); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.Calls) != 0 {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestMinimizeDisableSetFailureStillRestoresAndJoins(t *testing.T) {
	f := &system.FakeWindows{Showing: true, SetErr: errors.New("set boom"), RestoreErr: errors.New("restore boom")}
	err := NewMinimize(f).Disable(ctx, "off:7")
	if err == nil || !strings.Contains(err.Error(), "restore desktop: set boom") || !strings.Contains(err.Error(), "restore boom") {
		t.Fatalf("got %v", err)
	}
	if !slices.Contains(f.Calls, "RestoreWindows 7") {
		t.Fatalf("calls %v", f.Calls)
	}
}

func TestMinimizeDisableRestoreFailureIsReported(t *testing.T) {
	f := &system.FakeWindows{Showing: true, RestoreErr: errors.New("x11: timed out")}
	err := NewMinimize(f).Disable(ctx, "off:7")
	if err == nil || !strings.HasPrefix(err.Error(), "restore desktop: ") || f.Showing {
		t.Fatalf("got %v showing %v", err, f.Showing)
	}
}

func TestMinimizeDisablePassesContext(t *testing.T) {
	f := &system.FakeWindows{Showing: true}
	type key struct{}
	cctx := context.WithValue(ctx, key{}, 1)
	f.OnRestore = func(c context.Context, _ []uint32) {
		if c.Value(key{}) != 1 {
			t.Error("restore got another context")
		}
	}
	if err := NewMinimize(f).Disable(cctx, "off:7"); err != nil {
		t.Fatal(err)
	}
}

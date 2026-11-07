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

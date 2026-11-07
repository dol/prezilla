package system

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func noBus(t *testing.T) {
	t.Helper()
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "nobus"))
}

func TestDBusScreenSaverNoBusIsNotFatalToCancel(t *testing.T) {
	noBus(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	err := DBusScreenSaver{}.Watch(ctx, func(bool) { t.Error("callback without bus") })
	if err == nil || !strings.HasPrefix(err.Error(), "screensaver: connect:") {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestDBusScreenSaverCancelledContextSkipsConnect(t *testing.T) {
	noBus(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := DBusScreenSaver{}.Watch(ctx, func(bool) {})
	if !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), "screensaver:") {
		t.Fatalf("err = %v", err)
	}
}

func TestScreenSaverSignalParsing(t *testing.T) {
	cases := []struct {
		name   string
		sig    *dbus.Signal
		active bool
		ok     bool
	}{
		{"xfce true", &dbus.Signal{Name: "org.xfce.ScreenSaver.ActiveChanged", Body: []any{true}}, true, true},
		{"freedesktop false", &dbus.Signal{Name: "org.freedesktop.ScreenSaver.ActiveChanged", Body: []any{false}}, false, true},
		{"gnome true", &dbus.Signal{Name: "org.gnome.ScreenSaver.ActiveChanged", Body: []any{true}}, true, true},
		{"other member", &dbus.Signal{Name: "org.xfce.ScreenSaver.Other", Body: []any{true}}, false, false},
		{"other interface", &dbus.Signal{Name: "org.example.ScreenSaver.ActiveChanged", Body: []any{true}}, false, false},
		{"no body", &dbus.Signal{Name: "org.xfce.ScreenSaver.ActiveChanged"}, false, false},
		{"wrong type", &dbus.Signal{Name: "org.xfce.ScreenSaver.ActiveChanged", Body: []any{"yes"}}, false, false},
		{"nil", nil, false, false},
	}
	for _, c := range cases {
		active, ok := screenSaverActive(c.sig)
		if active != c.active || ok != c.ok {
			t.Errorf("%s: got %v,%v want %v,%v", c.name, active, ok, c.active, c.ok)
		}
	}
}

func TestActiveDedupDropsConsecutiveRepeats(t *testing.T) {
	var d activeDedup
	var got []bool
	for _, v := range []bool{true, true, false, false, false, true, false} {
		if d.changed(v) {
			got = append(got, v)
		}
	}
	if want := []bool{true, false, true, false}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v want %v", got, want)
	}
}

func TestFakeScreenSaverDeliversUntilCancel(t *testing.T) {
	f := &FakeScreenSaver{}
	ctx, cancel := context.WithCancel(context.Background())
	got := make(chan bool, 4)
	done := make(chan error, 1)
	go func() { done <- f.Watch(ctx, func(a bool) { got <- a }) }()
	f.Emit(true)
	f.Emit(false)
	for _, want := range []bool{true, false} {
		select {
		case v := <-got:
			if v != want {
				t.Errorf("got %v want %v", v, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("no event")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("err %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not return on cancel")
	}
}

func TestFakeScreenSaverErr(t *testing.T) {
	f := &FakeScreenSaver{Err: errors.New("screensaver: connect: x")}
	if err := f.Watch(context.Background(), func(bool) {}); err == nil {
		t.Fatal("want error")
	}
}

func TestDBusPrompterNoBusErrorsFast(t *testing.T) {
	noBus(t)
	start := time.Now()
	_, err := DBusPrompter{}.Ask(context.Background(), Prompt{Summary: "s"})
	if err == nil || !strings.HasPrefix(err.Error(), "notify: connect:") {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestDBusPrompterCancelledContextSkipsConnect(t *testing.T) {
	noBus(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := DBusPrompter{}.Ask(ctx, Prompt{Summary: "s"})
	if !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), "notify:") {
		t.Fatalf("err = %v", err)
	}
}

func TestPromptActionsAndHints(t *testing.T) {
	p := Prompt{Actions: []PromptAction{{"continue", "Continue presenting"}, {"end", "End presentation"}}}
	if got, want := promptActions(p), []string{"continue", "Continue presenting", "end", "End presentation"}; !reflect.DeepEqual(got, want) {
		t.Errorf("actions %v", got)
	}
	h := promptHints()
	if v, ok := h["urgency"].Value().(byte); !ok || v != 2 {
		t.Errorf("urgency %v", h["urgency"])
	}
	if v, ok := h["resident"].Value().(bool); !ok || !v {
		t.Errorf("resident %v", h["resident"])
	}
}

func TestHasCapability(t *testing.T) {
	if !hasCapability([]string{"body", "actions"}, "actions") || hasCapability([]string{"body"}, "actions") {
		t.Error("hasCapability")
	}
}

func TestPromptSignalMatching(t *testing.T) {
	const id = uint32(7)
	cases := []struct {
		name string
		sig  *dbus.Signal
		key  string
		done bool
	}{
		{"action", &dbus.Signal{Name: notifyIface + ".ActionInvoked", Body: []any{id, "continue"}}, "continue", true},
		{"closed", &dbus.Signal{Name: notifyIface + ".NotificationClosed", Body: []any{id, uint32(2)}}, "", true},
		{"other id action", &dbus.Signal{Name: notifyIface + ".ActionInvoked", Body: []any{uint32(8), "end"}}, "", false},
		{"other id closed", &dbus.Signal{Name: notifyIface + ".NotificationClosed", Body: []any{uint32(8), uint32(2)}}, "", false},
		{"other member", &dbus.Signal{Name: notifyIface + ".Other", Body: []any{id, "x"}}, "", false},
		{"short body", &dbus.Signal{Name: notifyIface + ".ActionInvoked", Body: []any{id}}, "", false},
		{"nil", nil, "", false},
	}
	for _, c := range cases {
		key, done := promptSignal(c.sig, id)
		if key != c.key || done != c.done {
			t.Errorf("%s: got %q,%v want %q,%v", c.name, key, done, c.key, c.done)
		}
	}
}

func TestFakePrompterCannedAndBlocking(t *testing.T) {
	f := &FakePrompter{Key: "end"}
	if k, err := f.Ask(context.Background(), Prompt{Summary: "a"}); k != "end" || err != nil {
		t.Errorf("canned %q %v", k, err)
	}
	f = &FakePrompter{Err: errors.New("notify: boom")}
	if _, err := f.Ask(context.Background(), Prompt{}); err == nil {
		t.Error("want err")
	}
	f = &FakePrompter{Answer: make(chan string, 1)}
	f.Answer <- "continue"
	if k, err := f.Ask(context.Background(), Prompt{Summary: "b"}); k != "continue" || err != nil {
		t.Errorf("answer %q %v", k, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Ask(ctx, Prompt{}); !errors.Is(err, context.Canceled) {
		t.Errorf("cancel err %v", err)
	}
	if n := len(f.Prompts()); n != 2 || f.Cancelled() != 1 {
		t.Errorf("prompts %d cancelled %d", n, f.Cancelled())
	}
}

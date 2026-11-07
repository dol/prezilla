package system

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDBusNotifierNoSessionBusErrorsFast(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "nobus"))
	start := time.Now()
	err := DBusNotifier{}.Notify(context.Background(), "s", "b", "prezilla")
	if err == nil || !strings.HasPrefix(err.Error(), "notify: connect:") {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestDBusNotifierCancelledContextSkipsConnect(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path="+filepath.Join(t.TempDir(), "nobus"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := DBusNotifier{}.Notify(ctx, "s", "b", "i")
	if !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), "notify:") {
		t.Fatalf("err = %v", err)
	}
}

func TestFakeNotifierRecords(t *testing.T) {
	f := &FakeNotifier{Err: errors.New("x")}
	err := f.Notify(context.Background(), "s", "b", "i")
	if err == nil || len(f.Calls) != 1 || f.Calls[0] != (NotifyCall{"s", "b", "i"}) {
		t.Errorf("calls %v err %v", f.Calls, err)
	}
}

func TestExecOpenerFallsBackToExoOpen(t *testing.T) {
	r := &FakeRunner{Err: map[string]error{"xdg-open https://x.org": errors.New("not found")}}
	if err := (ExecOpener{Runner: r}).Open(context.Background(), "https://x.org"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.Calls, "|"); got != "xdg-open https://x.org|exo-open https://x.org" {
		t.Errorf("calls %q", got)
	}
}

func TestExecOpenerUsesXdgOpenFirst(t *testing.T) {
	r := &FakeRunner{}
	if err := (ExecOpener{Runner: r}).Open(context.Background(), "http://x.org"); err != nil {
		t.Fatal(err)
	}
	if len(r.Calls) != 1 || r.Calls[0] != "xdg-open http://x.org" {
		t.Errorf("calls %v", r.Calls)
	}
}

func TestExecOpenerBothFailNamesBoth(t *testing.T) {
	r := &FakeRunner{Err: map[string]error{
		"xdg-open https://x.org": errors.New("xdg boom"),
		"exo-open https://x.org": errors.New("exo boom"),
	}}
	err := (ExecOpener{Runner: r}).Open(context.Background(), "https://x.org")
	if err == nil {
		t.Fatal("want error")
	}
	for _, s := range []string{"xdg-open", "exo-open", "xdg boom", "exo boom"} {
		if !strings.Contains(err.Error(), s) {
			t.Errorf("error %q lacks %q", err, s)
		}
	}
}

func TestExecOpenerRejectsNonHTTP(t *testing.T) {
	for _, u := range []string{"", "file:///etc/passwd", "-h", "javascript:x", "ftp://x"} {
		r := &FakeRunner{}
		err := (ExecOpener{Runner: r}).Open(context.Background(), u)
		if err == nil || err.Error() != "open: refusing non-http(s) URL" || len(r.Calls) != 0 {
			t.Errorf("%q: err %v calls %v", u, err, r.Calls)
		}
	}
}

func TestFakeOpenerRecords(t *testing.T) {
	f := &FakeOpener{}
	_ = f.Open(context.Background(), "https://a")
	if len(f.Calls) != 1 || f.Calls[0] != "https://a" {
		t.Errorf("%v", f.Calls)
	}
}

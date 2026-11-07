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

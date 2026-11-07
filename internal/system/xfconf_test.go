package system

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestDBusXfconfNoBus(t *testing.T) {
	noBus(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, _, err := DBusXfconf{}.Get(ctx, "c", "/p")
	if err == nil || !strings.HasPrefix(err.Error(), "xfconf: connect:") {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v", d)
	}
	if err := (DBusXfconf{}).Set(ctx, "c", "/p", XfBool(true)); err == nil || !strings.HasPrefix(err.Error(), "xfconf: connect:") {
		t.Errorf("set err = %v", err)
	}
}

func TestDBusXfconfCancelledContextSkipsConnect(t *testing.T) {
	noBus(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := DBusXfconf{}.List(ctx, "c", "/p")
	if !errors.Is(err, context.Canceled) || !strings.HasPrefix(err.Error(), "xfconf:") {
		t.Fatalf("err = %v", err)
	}
}

func TestXfconfNotFoundDetection(t *testing.T) {
	nf := fmt.Errorf("xfconf: get c /p: %w", dbus.NewError(xfconfNotFound, []any{"missing"}))
	wire := fmt.Errorf("xfconf: get c /p: %w", dbus.Error{Name: xfconfNotFound})
	other := fmt.Errorf("xfconf: get c /p: %w", dbus.NewError("org.freedesktop.DBus.Error.ServiceUnknown", nil))
	if !isNotFound(nf) || !isNotFound(wire) || isNotFound(other) || isNotFound(errors.New("x")) || isNotFound(nil) {
		t.Error("isNotFound")
	}
}

func TestXfconfFromVariant(t *testing.T) {
	cases := []struct {
		in   any
		want XfconfValue
	}{
		{"a.png", XfString("a.png")},
		{true, XfBool(true)},
		{int32(2), XfInt(2)},
		{uint32(7), XfInt(7)},
		{int64(-3), XfconfValue{Kind: XfconfInt, Int: -3}},
		{1.5, XfconfValue{Kind: XfconfOther, Str: "1.5"}},
	}
	for _, c := range cases {
		if got := xfconfFromVariant(dbus.MakeVariant(c.in)); got != c.want {
			t.Errorf("%T: got %+v want %+v", c.in, got, c.want)
		}
	}
}

func TestXfconfToVariant(t *testing.T) {
	for _, c := range []struct {
		in   XfconfValue
		want any
	}{{XfString("s"), "s"}, {XfBool(false), false}, {XfInt(0), int32(0)}} {
		v, err := xfconfToVariant(c.in)
		if err != nil || v.Value() != c.want {
			t.Errorf("%+v: got %v, %v", c.in, v, err)
		}
	}
	if _, err := xfconfToVariant(XfconfValue{Kind: XfconfInt, Int: 1 << 40}); err == nil {
		t.Error("out of range int accepted")
	}
	if _, err := xfconfToVariant(XfconfValue{Kind: XfconfOther}); err == nil {
		t.Error("other kind accepted")
	}
}

func TestXfconfValueString(t *testing.T) {
	if XfInt(-2).String() != "-2" || XfBool(true).String() != "true" || XfString("x").String() != "x" {
		t.Error("String")
	}
}

func TestFakeXfconf(t *testing.T) {
	ctx := context.Background()
	f := &FakeXfconf{}
	if _, ok, err := f.Get(ctx, "c", "/a"); ok || err != nil {
		t.Fatalf("unset get: %v %v", ok, err)
	}
	_ = f.Set(ctx, "c", "/a/x", XfInt(2))
	_ = f.Set(ctx, "c", "/a/y", XfString("s"))
	_ = f.Set(ctx, "c", "/ab", XfString("no"))
	_ = f.Set(ctx, "d", "/a/z", XfString("other channel"))
	if v, ok, _ := f.Get(ctx, "c", "/a/x"); !ok || v != XfInt(2) {
		t.Errorf("get %+v %v", v, ok)
	}
	got, _ := f.List(ctx, "c", "/a")
	if want := map[string]XfconfValue{"/a/x": XfInt(2), "/a/y": XfString("s")}; !reflect.DeepEqual(got, want) {
		t.Errorf("list %v", got)
	}
	_ = f.Reset(ctx, "c", "/a/x")
	if _, ok, _ := f.Get(ctx, "c", "/a/x"); ok {
		t.Error("reset kept property")
	}
	f.Err = map[string]error{"Set c /a/y int:1": errors.New("boom")}
	if err := f.Set(ctx, "c", "/a/y", XfInt(1)); err == nil {
		t.Error("injected error ignored")
	}
	if v, _, _ := f.Get(ctx, "c", "/a/y"); v != XfString("s") {
		t.Errorf("failed set changed state: %+v", v)
	}
	if f.Calls[1] != "Set c /a/x int:2" {
		t.Errorf("calls %v", f.Calls)
	}
}

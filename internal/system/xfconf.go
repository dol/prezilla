package system

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// XfconfKind is the type of an Xfconf property value.
type XfconfKind int

// Value kinds. XfconfOther covers types the switches never write (arrays,
// doubles, unsigned numbers outside int32); they are read as text only.
const (
	XfconfString XfconfKind = iota
	XfconfInt
	XfconfBool
	XfconfOther
)

// XfconfValue is one typed property value. Only the field matching Kind is
// meaningful (Str for XfconfString and XfconfOther).
type XfconfValue struct {
	Kind XfconfKind
	Str  string
	Int  int64
	Bool bool
}

// XfString, XfInt and XfBool build values to write. Ints are written as
// int32, the type the XFCE settings use.
func XfString(s string) XfconfValue { return XfconfValue{Kind: XfconfString, Str: s} }
func XfInt(i int32) XfconfValue     { return XfconfValue{Kind: XfconfInt, Int: int64(i)} }
func XfBool(b bool) XfconfValue     { return XfconfValue{Kind: XfconfBool, Bool: b} }

// String is the textual form: decimal for ints, "true"/"false" for bools.
func (v XfconfValue) String() string {
	switch v.Kind {
	case XfconfInt:
		return strconv.FormatInt(v.Int, 10)
	case XfconfBool:
		return strconv.FormatBool(v.Bool)
	}
	return v.Str
}

// Xfconf reads and writes properties of the XFCE settings daemon.
type Xfconf interface {
	// Get returns a property; ok is false (with a nil error) when it is not set.
	Get(ctx context.Context, channel, prop string) (v XfconfValue, ok bool, err error)
	// Set writes a property with the type of v, creating it if needed.
	Set(ctx context.Context, channel, prop string, v XfconfValue) error
	// Reset removes a property. A property that is not set is not an error.
	Reset(ctx context.Context, channel, prop string) error
	// List returns every property of the channel at or below prefix, keyed by
	// full property path. No such properties gives an empty map.
	List(ctx context.Context, channel, prefix string) (map[string]XfconfValue, error)
}

const (
	xfconfName  = "org.xfce.Xfconf"
	xfconfPath  = dbus.ObjectPath("/org/xfce/Xfconf")
	xfconfIface = "org.xfce.Xfconf"
	// xfconfNotFound is the D-Bus error name of a property that is not set.
	xfconfNotFound = "org.xfce.Xfconf.Error.PropertyNotFound"
)

// DBusXfconf talks to xfconfd on the session bus (activating it when needed).
// Each call uses its own short-lived connection. The zero value is valid.
type DBusXfconf struct {
	// Timeout bounds each call; zero means DefaultTimeout.
	Timeout time.Duration
}

func (x DBusXfconf) timeout() time.Duration {
	if x.Timeout > 0 {
		return x.Timeout
	}
	return DefaultTimeout
}

// call runs one Xfconf method and returns the reply body. Errors start with
// "xfconf: ".
func (x DBusXfconf) call(parent context.Context, what, method string, args ...any) ([]any, error) {
	if err := parent.Err(); err != nil {
		return nil, fmt.Errorf("xfconf: %w", err)
	}
	timeout := x.timeout()
	conn, err := connectSession(parent, timeout, "xfconf")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	call := conn.Object(xfconfName, xfconfPath).CallWithContext(ctx, xfconfIface+"."+method, 0, args...)
	if call.Err != nil {
		if cerr := boundErr("xfconf: "+what, ctx, parent, timeout); cerr != nil {
			return nil, cerr
		}
		return nil, fmt.Errorf("xfconf: %s: %w", what, call.Err)
	}
	return call.Body, nil
}

// isNotFound reports whether err is Xfconf's "property does not exist".
func isNotFound(err error) bool {
	// The library returns the wire error as a value, locally built ones as a pointer.
	var dv dbus.Error
	if errors.As(err, &dv) {
		return dv.Name == xfconfNotFound
	}
	var dp *dbus.Error
	return errors.As(err, &dp) && dp.Name == xfconfNotFound
}

// Get implements Xfconf.
func (x DBusXfconf) Get(ctx context.Context, channel, prop string) (XfconfValue, bool, error) {
	body, err := x.call(ctx, "get "+channel+" "+prop, "GetProperty", channel, prop)
	if err != nil {
		if isNotFound(err) {
			return XfconfValue{}, false, nil
		}
		return XfconfValue{}, false, err
	}
	if len(body) != 1 {
		return XfconfValue{}, false, fmt.Errorf("xfconf: get %s %s: unexpected reply", channel, prop)
	}
	v, ok := body[0].(dbus.Variant)
	if !ok {
		return XfconfValue{}, false, fmt.Errorf("xfconf: get %s %s: unexpected reply", channel, prop)
	}
	return xfconfFromVariant(v), true, nil
}

// Set implements Xfconf.
func (x DBusXfconf) Set(ctx context.Context, channel, prop string, v XfconfValue) error {
	variant, err := xfconfToVariant(v)
	if err != nil {
		return fmt.Errorf("xfconf: set %s %s: %w", channel, prop, err)
	}
	_, err = x.call(ctx, "set "+channel+" "+prop, "SetProperty", channel, prop, variant)
	return err
}

// Reset implements Xfconf.
func (x DBusXfconf) Reset(ctx context.Context, channel, prop string) error {
	_, err := x.call(ctx, "reset "+channel+" "+prop, "ResetProperty", channel, prop, false)
	if err != nil && isNotFound(err) {
		return nil
	}
	return err
}

// List implements Xfconf.
func (x DBusXfconf) List(ctx context.Context, channel, prefix string) (map[string]XfconfValue, error) {
	body, err := x.call(ctx, "list "+channel+" "+prefix, "GetAllProperties", channel, prefix)
	if err != nil {
		if isNotFound(err) {
			return map[string]XfconfValue{}, nil
		}
		return nil, err
	}
	if len(body) != 1 {
		return nil, fmt.Errorf("xfconf: list %s %s: unexpected reply", channel, prefix)
	}
	raw, ok := body[0].(map[string]dbus.Variant)
	if !ok {
		return nil, fmt.Errorf("xfconf: list %s %s: unexpected reply", channel, prefix)
	}
	out := make(map[string]XfconfValue, len(raw))
	for p, v := range raw {
		out[p] = xfconfFromVariant(v)
	}
	return out, nil
}

// xfconfFromVariant converts a D-Bus value to an XfconfValue. Every integer
// type becomes XfconfInt; anything unknown becomes XfconfOther with its
// printed form.
func xfconfFromVariant(v dbus.Variant) XfconfValue {
	switch t := v.Value().(type) {
	case string:
		return XfString(t)
	case bool:
		return XfBool(t)
	case int16:
		return XfconfValue{Kind: XfconfInt, Int: int64(t)}
	case int32:
		return XfconfValue{Kind: XfconfInt, Int: int64(t)}
	case int64:
		return XfconfValue{Kind: XfconfInt, Int: t}
	case uint16:
		return XfconfValue{Kind: XfconfInt, Int: int64(t)}
	case uint32:
		return XfconfValue{Kind: XfconfInt, Int: int64(t)}
	case byte:
		return XfconfValue{Kind: XfconfInt, Int: int64(t)}
	}
	return XfconfValue{Kind: XfconfOther, Str: fmt.Sprint(v.Value())}
}

// xfconfToVariant converts a value to write; ints must fit int32.
func xfconfToVariant(v XfconfValue) (dbus.Variant, error) {
	switch v.Kind {
	case XfconfString:
		return dbus.MakeVariant(v.Str), nil
	case XfconfBool:
		return dbus.MakeVariant(v.Bool), nil
	case XfconfInt:
		if v.Int < math.MinInt32 || v.Int > math.MaxInt32 {
			return dbus.Variant{}, fmt.Errorf("value %d does not fit int32", v.Int)
		}
		return dbus.MakeVariant(int32(v.Int)), nil
	}
	return dbus.Variant{}, errors.New("unsupported value kind")
}

// FakeXfconf is an in-memory Xfconf for tests. Props holds the properties,
// keyed "<channel> <prop>" (see XfconfKey); Calls records every call as
// "Get <channel> <prop>", "Set <channel> <prop> <kind>:<value>",
// "Reset <channel> <prop>" or "List <channel> <prefix>". Err returns the
// mapped error for a call string without performing it.
type FakeXfconf struct {
	mu sync.Mutex

	Calls []string
	Props map[string]XfconfValue
	Err   map[string]error
}

// XfconfKey is the FakeXfconf.Props key of a property.
func XfconfKey(channel, prop string) string { return channel + " " + prop }

var kindNames = map[XfconfKind]string{XfconfString: "string", XfconfInt: "int", XfconfBool: "bool", XfconfOther: "other"}

// Get implements Xfconf.
func (f *FakeXfconf) Get(_ context.Context, channel, prop string) (XfconfValue, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := "Get " + XfconfKey(channel, prop)
	f.Calls = append(f.Calls, call)
	if err := f.Err[call]; err != nil {
		return XfconfValue{}, false, err
	}
	v, ok := f.Props[XfconfKey(channel, prop)]
	return v, ok, nil
}

// Set implements Xfconf.
func (f *FakeXfconf) Set(_ context.Context, channel, prop string, v XfconfValue) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := "Set " + XfconfKey(channel, prop) + " " + kindNames[v.Kind] + ":" + v.String()
	f.Calls = append(f.Calls, call)
	if err := f.Err[call]; err != nil {
		return err
	}
	if f.Props == nil {
		f.Props = map[string]XfconfValue{}
	}
	f.Props[XfconfKey(channel, prop)] = v
	return nil
}

// Reset implements Xfconf.
func (f *FakeXfconf) Reset(_ context.Context, channel, prop string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := "Reset " + XfconfKey(channel, prop)
	f.Calls = append(f.Calls, call)
	if err := f.Err[call]; err != nil {
		return err
	}
	delete(f.Props, XfconfKey(channel, prop))
	return nil
}

// List implements Xfconf.
func (f *FakeXfconf) List(_ context.Context, channel, prefix string) (map[string]XfconfValue, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	call := "List " + XfconfKey(channel, prefix)
	f.Calls = append(f.Calls, call)
	if err := f.Err[call]; err != nil {
		return nil, err
	}
	out := map[string]XfconfValue{}
	keys := make([]string, 0, len(f.Props))
	for k := range f.Props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		ch, prop, _ := strings.Cut(k, " ")
		if ch == channel && (prop == prefix || strings.HasPrefix(prop, strings.TrimSuffix(prefix, "/")+"/")) {
			out[prop] = f.Props[k]
		}
	}
	return out, nil
}

package system

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// privateBus starts a throwaway dbus-daemon with no service directories (so
// nothing, least of all a real portal, can be activated on it) and points
// DBUS_SESSION_BUS_ADDRESS at it. It skips without dbus-daemon unless
// PREZILLA_REQUIRE_DBUS_DAEMON is set (as in CI).
func privateBus(t *testing.T) string {
	t.Helper()
	bin, err := exec.LookPath("dbus-daemon")
	if err != nil {
		if os.Getenv("PREZILLA_REQUIRE_DBUS_DAEMON") != "" {
			t.Fatalf("dbus-daemon required: %v", err)
		}
		t.Skip("dbus-daemon not installed")
	}
	dir := t.TempDir()
	if len(dir) > 80 { // unix socket paths are limited to ~108 bytes
		d, err := os.MkdirTemp("/tmp", "pzbus")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(d) })
		dir = d
	}
	conf := filepath.Join(dir, "bus.conf")
	err = os.WriteFile(conf, []byte(`<!DOCTYPE busconfig PUBLIC "-//freedesktop//DTD D-Bus Bus Configuration 1.0//EN"
 "http://www.freedesktop.org/standards/dbus/1.0/busconfig.dtd">
<busconfig>
  <type>session</type>
  <listen>unix:dir=`+dir+`</listen>
  <auth>EXTERNAL</auth>
  <policy context="default">
    <allow send_destination="*" eavesdrop="true"/>
    <allow eavesdrop="true"/>
    <allow own="*"/>
  </policy>
</busconfig>
`), 0o644)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--config-file="+conf, "--nofork", "--nopidfile", "--print-address")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cmd.Process.Kill()
		cmd.Wait()
	})
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("dbus-daemon: %v: %s", err, stderr.String())
	}
	addr := strings.TrimSpace(line)
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", addr)
	return addr
}

type openCall struct {
	Sender  string
	Parent  string
	Title   string
	Options map[string]dbus.Variant
	Handle  dbus.ObjectPath
}

// fakePortal owns org.freedesktop.portal.Desktop on a private bus and
// answers OpenFile by emitting reply (until stopped) on the request path.
type fakePortal struct {
	conn *dbus.Conn
	// reply is the Response body; nil means never answer.
	reply []any
	// handle overrides the returned request path when non-empty.
	handle dbus.ObjectPath
	// openErr is returned by OpenFile when set.
	openErr *dbus.Error

	mu     sync.Mutex
	opens  []openCall
	closes []dbus.ObjectPath
	opened chan struct{}
	stop   chan struct{}
}

type fakeFileChooser struct{ f *fakePortal }

func (o fakeFileChooser) OpenFile(msg dbus.Message, sender dbus.Sender, parent, title string, opts map[string]dbus.Variant) (dbus.ObjectPath, *dbus.Error) {
	f := o.f
	// The raw body keeps each variant's wire signature (opts is rebuilt).
	if len(msg.Body) == 3 {
		if raw, ok := msg.Body[2].(map[string]dbus.Variant); ok {
			opts = raw
		}
	}
	if f.openErr != nil {
		return "", f.openErr
	}
	token, _ := opts["handle_token"].Value().(string)
	handle := portalRequestPath(string(sender), token)
	if f.handle != "" {
		handle = f.handle
	}
	f.mu.Lock()
	f.opens = append(f.opens, openCall{string(sender), parent, title, opts, handle})
	f.mu.Unlock()
	if f.reply != nil {
		// Emit until stopped, so a client that subscribes late (the
		// differing-handle case) still gets it.
		go func() {
			for {
				f.conn.Emit(handle, portalResponse, f.reply...)
				select {
				case <-f.stop:
					return
				case <-time.After(20 * time.Millisecond):
				}
			}
		}()
	}
	select {
	case f.opened <- struct{}{}:
	default:
	}
	return handle, nil
}

type fakeRequest struct{ f *fakePortal }

func (o fakeRequest) Close(msg dbus.Message) *dbus.Error {
	path, _ := msg.Headers[dbus.FieldPath].Value().(dbus.ObjectPath)
	o.f.mu.Lock()
	o.f.closes = append(o.f.closes, path)
	o.f.mu.Unlock()
	return nil
}

// noOpenFile exports the FileChooser interface without OpenFile.
type noOpenFile struct{}

func (noOpenFile) SaveFile() *dbus.Error { return nil }

// newFakePortal starts the fake; setup configures it before it serves.
// chooser replaces the FileChooser object when non-nil.
func newFakePortal(t *testing.T, addr string, chooser any, setup func(f *fakePortal)) *fakePortal {
	t.Helper()
	conn, err := dbus.Connect(addr)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakePortal{conn: conn, opened: make(chan struct{}, 1), stop: make(chan struct{})}
	t.Cleanup(func() {
		close(f.stop)
		conn.Close()
	})
	if setup != nil {
		setup(f)
	}
	if chooser == nil {
		chooser = fakeFileChooser{f}
	}
	if err := conn.Export(chooser, portalPath, portalFileChooser); err != nil {
		t.Fatal(err)
	}
	if err := conn.ExportSubtree(fakeRequest{f}, "/org/freedesktop/portal/desktop/request", portalRequest); err != nil {
		t.Fatal(err)
	}
	if r, err := conn.RequestName(portalName, dbus.NameFlagDoNotQueue); err != nil || r != dbus.RequestNameReplyPrimaryOwner {
		t.Fatalf("request name: %v %v", r, err)
	}
	return f
}

func (f *fakePortal) calls() ([]openCall, []dbus.ObjectPath) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]openCall(nil), f.opens...), append([]dbus.ObjectPath(nil), f.closes...)
}

func chosen(uris ...string) []any {
	return []any{uint32(0), map[string]dbus.Variant{"uris": dbus.MakeVariant(uris)}}
}

func testCtx(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func TestPortalRequestPath(t *testing.T) {
	got := portalRequestPath(":1.42", "prezilla_ab12")
	if want := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_42/prezilla_ab12"); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
	if !got.IsValid() {
		t.Errorf("%s is not a valid object path", got)
	}
}

func TestNewHandleTokenCharset(t *testing.T) {
	a, err := newHandleToken()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := newHandleToken()
	if a == b {
		t.Errorf("tokens repeat: %s", a)
	}
	for _, r := range a {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_') {
			t.Fatalf("token %q has %q", a, r)
		}
	}
}

func TestFileURIPath(t *testing.T) {
	for uri, want := range map[string]string{
		"file:///home/u/a.png":                                 "/home/u/a.png",
		"file:///home/u/My%20Pictures/bild%20%C3%A9%C3%BC.png": "/home/u/My Pictures/bild éü.png",
		"file://localhost/x/%25%23%3F.png":                     "/x/%#?.png",
	} {
		got, err := fileURIPath(uri)
		if err != nil || got != want {
			t.Errorf("%s: got %q, %v; want %q", uri, got, err, want)
		}
	}
	for _, uri := range []string{"https://example.com/a.png", "file://otherhost/a.png", "a.png", "file:a.png", "%zz"} {
		if _, err := fileURIPath(uri); !errors.Is(err, ErrPortalFailed) {
			t.Errorf("%s: err = %v, want ErrPortalFailed", uri, err)
		}
	}
}

func TestPortalResult(t *testing.T) {
	if got, err := portalResult(chosen("file:///a%20b.png", "file:///c.png")); err != nil || got != "/a b.png" {
		t.Errorf("chosen: %q %v", got, err)
	}
	if _, err := portalResult([]any{uint32(1), map[string]dbus.Variant{}}); !errors.Is(err, ErrCancelled) {
		t.Errorf("cancel: %v", err)
	}
	if _, err := portalResult([]any{uint32(2), map[string]dbus.Variant{}}); !errors.Is(err, ErrCancelled) {
		t.Errorf("ended: %v", err)
	}
	if _, err := portalResult([]any{uint32(3), map[string]dbus.Variant{}}); !errors.Is(err, ErrPortalFailed) || errors.Is(err, ErrCancelled) {
		t.Errorf("other: %v", err)
	}
	for _, body := range [][]any{nil, {uint32(0)}, {"x", map[string]dbus.Variant{}}, chosen()} {
		if _, err := portalResult(body); !errors.Is(err, ErrPortalFailed) {
			t.Errorf("%v: %v", body, err)
		}
	}
}

func TestPortalChooserChosen(t *testing.T) {
	addr := privateBus(t)
	f := newFakePortal(t, addr, nil, func(f *fakePortal) {
		f.reply = chosen("file:///home/u/My%20Pictures/bild%20%C3%A9.png")
	})
	got, err := PortalChooser{}.ChooseImage(testCtx(t), "/home/u")
	if err != nil || got != "/home/u/My Pictures/bild é.png" {
		t.Fatalf("got %q, %v", got, err)
	}
	opens, _ := f.calls()
	if len(opens) != 1 {
		t.Fatalf("opens %v", opens)
	}
	o := opens[0]
	if o.Parent != "" || o.Title != "Choose wallpaper" {
		t.Errorf("parent %q title %q", o.Parent, o.Title)
	}
	token, _ := o.Options["handle_token"].Value().(string)
	if o.Handle != portalRequestPath(o.Sender, token) || token == "" {
		t.Errorf("handle %s token %q sender %s", o.Handle, token, o.Sender)
	}
	if m, _ := o.Options["modal"].Value().(bool); !m {
		t.Errorf("modal = %v", o.Options["modal"])
	}
	if cf, _ := o.Options["current_folder"].Value().([]byte); string(cf) != "/home/u\x00" {
		t.Errorf("current_folder = %q (%s)", cf, o.Options["current_folder"].Signature())
	}
	fv := o.Options["filters"]
	if sig := fv.Signature().String(); sig != "a(sa(us))" {
		t.Fatalf("filters signature %s", sig)
	}
	var filters []portalFilter
	if err := dbus.Store([]any{fv.Value()}, &filters); err != nil {
		t.Fatal(err)
	}
	var pats []string
	for _, p := range filters[0].Patterns {
		if p.Kind != 0 {
			t.Errorf("pattern %v is not a glob", p)
		}
		pats = append(pats, p.Pattern)
	}
	want := []string{"*.png", "*.PNG", "*.jpg", "*.JPG", "*.jpeg", "*.JPEG", "*.webp", "*.WEBP", "*.bmp", "*.BMP",
		"*.gif", "*.GIF", "*.tif", "*.TIF", "*.tiff", "*.TIFF", "*.svg", "*.SVG"}
	if len(filters) != 1 || filters[0].Name != "Images" || !reflect.DeepEqual(pats, want) {
		t.Errorf("filters %+v", filters)
	}
}

func TestPortalChooserNoStartDirOmitsCurrentFolder(t *testing.T) {
	addr := privateBus(t)
	f := newFakePortal(t, addr, nil, func(f *fakePortal) {
		f.reply = chosen("file:///a.png")
	})
	if _, err := (PortalChooser{}).ChooseImage(testCtx(t), ""); err != nil {
		t.Fatal(err)
	}
	opens, _ := f.calls()
	if _, ok := opens[0].Options["current_folder"]; ok {
		t.Errorf("current_folder sent: %v", opens[0].Options)
	}
}

func TestPortalChooserCancelled(t *testing.T) {
	addr := privateBus(t)
	newFakePortal(t, addr, nil, func(f *fakePortal) {
		f.reply = []any{uint32(1), map[string]dbus.Variant{}}
	})
	if _, err := (PortalChooser{}).ChooseImage(testCtx(t), "/"); !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v", err)
	}
}

func TestPortalChooserEndedOtherwiseIsCancelled(t *testing.T) {
	addr := privateBus(t)
	newFakePortal(t, addr, nil, func(f *fakePortal) {
		f.reply = []any{uint32(2), map[string]dbus.Variant{}}
	})
	_, err := PortalChooser{}.ChooseImage(testCtx(t), "/")
	if !errors.Is(err, ErrCancelled) {
		t.Fatalf("err = %v", err)
	}
}

func TestPortalChooserNonFileURI(t *testing.T) {
	addr := privateBus(t)
	newFakePortal(t, addr, nil, func(f *fakePortal) {
		f.reply = chosen("sftp://host/a.png")
	})
	if _, err := (PortalChooser{}).ChooseImage(testCtx(t), "/"); !errors.Is(err, ErrPortalFailed) {
		t.Fatalf("err = %v", err)
	}
}

func TestPortalChooserDifferentHandle(t *testing.T) {
	addr := privateBus(t)
	newFakePortal(t, addr, nil, func(f *fakePortal) {
		f.handle = "/org/freedesktop/portal/desktop/request/old/style"
		f.reply = chosen("file:///b.png")
	})
	if got, err := (PortalChooser{}).ChooseImage(testCtx(t), "/"); err != nil || got != "/b.png" {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestPortalChooserContextCancelClosesRequest(t *testing.T) {
	addr := privateBus(t)
	f := newFakePortal(t, addr, nil, nil) // never answers
	ctx, cancel := context.WithCancel(testCtx(t))
	go func() {
		<-f.opened
		cancel()
	}()
	_, err := PortalChooser{}.ChooseImage(ctx, "/")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	opens, closes := f.calls()
	if len(opens) != 1 || len(closes) != 1 || closes[0] != opens[0].Handle {
		t.Errorf("opens %v closes %v", opens, closes)
	}
}

func TestPortalChooserNoOwnerIsErrNoPortal(t *testing.T) {
	privateBus(t)
	_, err := PortalChooser{}.ChooseImage(testCtx(t), "/")
	if !errors.Is(err, ErrNoPortal) {
		t.Fatalf("err = %v", err)
	}
}

func TestPortalChooserUnknownMethodIsErrNoPortal(t *testing.T) {
	addr := privateBus(t)
	newFakePortal(t, addr, noOpenFile{}, nil)
	_, err := PortalChooser{}.ChooseImage(testCtx(t), "/")
	if !errors.Is(err, ErrNoPortal) {
		t.Fatalf("err = %v", err)
	}
	if name, _ := dbusErrorName(err); name != "org.freedesktop.DBus.Error.UnknownMethod" {
		t.Errorf("error name %q", name)
	}
}

func TestPortalChooserErrorReplyIsRealError(t *testing.T) {
	addr := privateBus(t)
	newFakePortal(t, addr, nil, func(f *fakePortal) {
		f.openErr = dbus.NewError("org.freedesktop.portal.Error.Failed", []any{"boom"})
	})
	_, err := PortalChooser{}.ChooseImage(testCtx(t), "/")
	if !errors.Is(err, ErrPortalFailed) || errors.Is(err, ErrNoPortal) {
		t.Fatalf("err = %v", err)
	}
}

func TestPortalChooserNoBusIsErrNoPortal(t *testing.T) {
	noBus(t)
	start := time.Now()
	_, err := PortalChooser{}.ChooseImage(testCtx(t), "/")
	if !errors.Is(err, ErrNoPortal) {
		t.Fatalf("err = %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("took %v", d)
	}
}

func TestPortalChooserCancelledContextReturnsCtxErr(t *testing.T) {
	noBus(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (PortalChooser{}).ChooseImage(ctx, "/"); err != context.Canceled {
		t.Fatalf("err = %v", err)
	}
}

func TestPortalUnavailableNames(t *testing.T) {
	for name, want := range map[string]bool{
		"org.freedesktop.DBus.Error.ServiceUnknown":    true,
		"org.freedesktop.DBus.Error.NameHasNoOwner":    true,
		"org.freedesktop.DBus.Error.UnknownMethod":     true,
		"org.freedesktop.DBus.Error.UnknownInterface":  true,
		"org.freedesktop.DBus.Error.UnknownObject":     true,
		"org.freedesktop.DBus.Error.NoReply":           true,
		"org.freedesktop.DBus.Error.Spawn.ChildExited": true,
		"org.freedesktop.DBus.Error.AccessDenied":      false,
		"org.freedesktop.portal.Error.Failed":          false,
	} {
		if got := portalUnavailable(dbus.Error{Name: name}); got != want {
			t.Errorf("%s (value): %v", name, got)
		}
		if got := portalUnavailable(dbus.NewError(name, nil)); got != want {
			t.Errorf("%s (pointer): %v", name, got)
		}
	}
	if portalUnavailable(errors.New("x")) {
		t.Error("plain error")
	}
}

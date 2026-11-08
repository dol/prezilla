package system

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// ErrNoPortal means no file chooser portal is reachable: no session bus, no
// org.freedesktop.portal.Desktop service, no FileChooser interface on it, or
// a portal that failed to start. Callers may fall back to another chooser.
var ErrNoPortal = errors.New("no file chooser portal")

// ErrPortalFailed marks a portal that was reached but failed: an error reply,
// a malformed answer, a non-file URI or a dialog that ended otherwise.
var ErrPortalFailed = errors.New("the file chooser portal failed")

const (
	portalName          = "org.freedesktop.portal.Desktop"
	portalPath          = dbus.ObjectPath("/org/freedesktop/portal/desktop")
	portalFileChooser   = "org.freedesktop.portal.FileChooser"
	portalRequest       = "org.freedesktop.portal.Request"
	portalResponse      = portalRequest + ".Response"
	portalRequestPrefix = "/org/freedesktop/portal/desktop/request/"
	portalTitle         = "Choose wallpaper"

	// defaultPortalTimeout bounds connecting, subscribing and the OpenFile
	// call (which may start the portal through D-Bus activation), not the
	// dialog itself.
	defaultPortalTimeout = 30 * time.Second
	// portalCloseTimeout bounds the best-effort Request.Close.
	portalCloseTimeout = 5 * time.Second
)

// Portal Request response codes.
const (
	portalChosen    = 0
	portalCancelled = 1
)

// PortalChooser asks for an image with the desktop's file chooser portal
// (org.freedesktop.portal.FileChooser) on the session bus, so the dialog is
// shown by the portal service. Each call uses its own private connection.
// An unreachable portal yields an error wrapping ErrNoPortal. The zero value
// is valid.
type PortalChooser struct {
	// Timeout bounds the setup (connect, subscribe, OpenFile), not the
	// dialog; zero means 30 seconds.
	Timeout time.Duration
}

func (p PortalChooser) timeout() time.Duration {
	if p.Timeout > 0 {
		return p.Timeout
	}
	return defaultPortalTimeout
}

// portalFilter is one (sa(us)) entry of the "filters" option.
type portalFilter struct {
	Name     string
	Patterns []portalPattern
}

// portalPattern is one (us) entry: kind 0 is a glob, 1 a MIME type.
type portalPattern struct {
	Kind    uint32
	Pattern string
}

// portalImageFilters is the "filters" option: one "Images" filter with the
// accepted extensions as globs.
func portalImageFilters() []portalFilter {
	f := portalFilter{Name: "Images"}
	for _, p := range imagePatterns() {
		f.Patterns = append(f.Patterns, portalPattern{0, p})
	}
	return []portalFilter{f}
}

// portalOptions builds the a{sv} options of OpenFile. current_folder is a
// NUL-terminated byte string, as the portal spec requires.
func portalOptions(token, startDir string) map[string]dbus.Variant {
	opts := map[string]dbus.Variant{
		"handle_token": dbus.MakeVariant(token),
		"modal":        dbus.MakeVariant(true),
		"filters":      dbus.MakeVariant(portalImageFilters()),
	}
	if startDir != "" {
		opts["current_folder"] = dbus.MakeVariant(append([]byte(startDir), 0))
	}
	return opts
}

// newHandleToken returns a random handle_token ([A-Za-z0-9_] only).
func newHandleToken() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "prezilla_" + hex.EncodeToString(b), nil
}

// portalRequestPath is the Request object path the portal uses for sender
// (a unique bus name like ":1.42") and token.
func portalRequestPath(sender, token string) dbus.ObjectPath {
	s := strings.ReplaceAll(strings.TrimPrefix(sender, ":"), ".", "_")
	return dbus.ObjectPath(portalRequestPrefix + s + "/" + token)
}

// dbusErrorName returns the D-Bus error name in err, if any. The library
// returns wire errors as values and locally built ones as pointers.
func dbusErrorName(err error) (string, bool) {
	var dv dbus.Error
	if errors.As(err, &dv) {
		return dv.Name, true
	}
	var dp *dbus.Error
	if errors.As(err, &dp) && dp != nil {
		return dp.Name, true
	}
	return "", false
}

// portalUnavailable reports whether a D-Bus error from OpenFile means the
// portal is not there (or could not be started).
func portalUnavailable(err error) bool {
	name, ok := dbusErrorName(err)
	if !ok {
		return false
	}
	switch name {
	case "org.freedesktop.DBus.Error.ServiceUnknown",
		"org.freedesktop.DBus.Error.NameHasNoOwner",
		"org.freedesktop.DBus.Error.UnknownMethod",
		"org.freedesktop.DBus.Error.UnknownInterface",
		"org.freedesktop.DBus.Error.UnknownObject",
		"org.freedesktop.DBus.Error.NoReply",
		"org.freedesktop.DBus.Error.TimedOut",
		"org.freedesktop.DBus.Error.Timeout":
		return true
	}
	return strings.HasPrefix(name, "org.freedesktop.DBus.Error.Spawn.")
}

// fileURIPath turns a file:// URI from the portal into a local path.
func fileURIPath(uri string) (string, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("file chooser: portal: %w: bad URI %q: %w", ErrPortalFailed, uri, err)
	}
	if u.Scheme != "file" || (u.Host != "" && u.Host != "localhost") {
		return "", fmt.Errorf("file chooser: portal: %w: not a local file: %q", ErrPortalFailed, uri)
	}
	path, err := url.PathUnescape(u.EscapedPath())
	if err != nil {
		return "", fmt.Errorf("file chooser: portal: %w: bad URI %q: %w", ErrPortalFailed, uri, err)
	}
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("file chooser: portal: %w: not a local file: %q", ErrPortalFailed, uri)
	}
	return path, nil
}

// portalResult interprets the body (u response, a{sv} results) of a
// Request.Response signal.
func portalResult(body []any) (string, error) {
	if len(body) < 2 {
		return "", fmt.Errorf("file chooser: portal: %w: malformed response %v", ErrPortalFailed, body)
	}
	code, ok := body[0].(uint32)
	results, ok2 := body[1].(map[string]dbus.Variant)
	if !ok || !ok2 {
		return "", fmt.Errorf("file chooser: portal: %w: malformed response %v", ErrPortalFailed, body)
	}
	switch code {
	case portalChosen:
	case portalCancelled:
		return "", ErrCancelled
	default:
		return "", fmt.Errorf("file chooser: portal: %w: the dialog ended with response %d", ErrPortalFailed, code)
	}
	uris, _ := results["uris"].Value().([]string)
	if len(uris) == 0 {
		return "", fmt.Errorf("file chooser: portal: %w: no file in the response", ErrPortalFailed)
	}
	return fileURIPath(uris[0])
}

// subscribePortalResponse adds a match for Request.Response on path.
func subscribePortalResponse(ctx context.Context, conn *dbus.Conn, path dbus.ObjectPath) error {
	return conn.AddMatchSignalContext(ctx, dbus.WithMatchObjectPath(path),
		dbus.WithMatchInterface(portalRequest), dbus.WithMatchMember("Response"))
}

// closePortalRequest closes the dialog of request, best effort.
func closePortalRequest(conn *dbus.Conn, request dbus.ObjectPath) {
	ctx, cancel := context.WithTimeout(context.Background(), portalCloseTimeout)
	defer cancel()
	conn.Object(portalName, request).CallWithContext(ctx, portalRequest+".Close", 0)
}

// Supported implements Chooser: the session bus is reachable and the portal
// service is running or can be started by D-Bus activation.
func (p PortalChooser) Supported(ctx context.Context) bool {
	conn, err := connectSession(ctx, p.timeout(), "file chooser: portal")
	if err != nil {
		return false
	}
	defer conn.Close()
	bus := conn.BusObject()
	var has bool
	if bus.CallWithContext(ctx, "org.freedesktop.DBus.NameHasOwner", 0, portalName).Store(&has) == nil && has {
		return true
	}
	var names []string
	if bus.CallWithContext(ctx, "org.freedesktop.DBus.ListActivatableNames", 0).Store(&names) != nil {
		return false
	}
	for _, n := range names {
		if n == portalName {
			return true
		}
	}
	return false
}

// ChooseImage implements FileChooser. It returns the chosen path, ErrCancelled
// when the dialog was dismissed, an error wrapping ErrNoPortal when no portal
// is reachable, ctx's error when ctx ended (the dialog is then closed), or an
// error wrapping ErrPortalFailed.
func (p PortalChooser) ChooseImage(ctx context.Context, startDir string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	timeout := p.timeout()
	conn, err := connectSession(ctx, timeout, "file chooser: portal")
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w: %w", ErrNoPortal, err)
	}
	defer conn.Close()
	names := conn.Names()
	if len(names) == 0 {
		return "", fmt.Errorf("file chooser: portal: %w: no unique bus name", ErrNoPortal)
	}
	token, err := newHandleToken()
	if err != nil {
		return "", fmt.Errorf("file chooser: portal: handle token: %w", err)
	}
	request := portalRequestPath(names[0], token)

	ch := make(chan *dbus.Signal, 8)
	conn.Signal(ch)
	defer conn.RemoveSignal(ch)

	sctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	// Subscribe before OpenFile, so a fast Response cannot be missed.
	if err := subscribePortalResponse(sctx, conn, request); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if berr := boundErr("file chooser: portal: subscribe", sctx, ctx, timeout); berr != nil {
			return "", fmt.Errorf("%w: %w", ErrNoPortal, berr)
		}
		return "", fmt.Errorf("file chooser: portal: %w: subscribe: %w", ErrPortalFailed, err)
	}
	call := conn.Object(portalName, portalPath).CallWithContext(sctx, portalFileChooser+".OpenFile", 0,
		"", portalTitle, portalOptions(token, startDir))
	if call.Err != nil {
		if sctx.Err() != nil {
			// The call may have reached the portal: close its dialog.
			closePortalRequest(conn, request)
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("%w: %w", ErrNoPortal, boundErr("file chooser: portal: open", sctx, ctx, timeout))
		}
		if portalUnavailable(call.Err) {
			return "", fmt.Errorf("file chooser: portal: %w: %w", ErrNoPortal, call.Err)
		}
		return "", fmt.Errorf("file chooser: portal: %w: open: %w", ErrPortalFailed, call.Err)
	}
	var handle dbus.ObjectPath
	if err := call.Store(&handle); err != nil {
		return "", fmt.Errorf("file chooser: portal: %w: open: %w", ErrPortalFailed, err)
	}
	if handle != request {
		// Older portals do not derive the path from handle_token.
		if err := subscribePortalResponse(sctx, conn, handle); err != nil {
			closePortalRequest(conn, handle)
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", fmt.Errorf("file chooser: portal: %w: subscribe: %w", ErrPortalFailed, err)
		}
		request = handle
	}
	cancel()

	for {
		select {
		case <-ctx.Done():
			closePortalRequest(conn, request)
			return "", ctx.Err()
		case sig, ok := <-ch:
			if !ok {
				return "", fmt.Errorf("file chooser: portal: %w: connection closed", ErrPortalFailed)
			}
			if sig == nil || sig.Path != request || sig.Name != portalResponse {
				continue
			}
			return portalResult(sig.Body)
		}
	}
}

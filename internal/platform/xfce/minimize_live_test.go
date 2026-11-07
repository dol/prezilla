package xfce

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/xevent"
	"github.com/jezek/xgbutil/xprop"

	"prezilla/internal/system"
)

// These tests drive the real minimize switch against a running window manager
// (xfwm4). They need PREZILLA_X11_LIVE=1 and a throwaway DISPLAY that has
// the window manager on it, for example
// `Xvfb :98 -noreset & xfwm4 --display :98 --replace &`. Never point them at
// a real session: they minimize and restore every window on it.
type liveWM struct {
	t  *testing.T
	xu *xgbutil.XUtil
}

func newLiveWM(t *testing.T) *liveWM {
	t.Helper()
	if os.Getenv("PREZILLA_X11_LIVE") != "1" || os.Getenv("DISPLAY") == "" {
		t.Skip("set PREZILLA_X11_LIVE=1 and DISPLAY (Xvfb with xfwm4) to run")
	}
	xu, err := xgbutil.NewConn()
	if err != nil {
		t.Skip("no X server:", err)
	}
	t.Cleanup(func() { xu.Conn().Close() })
	return &liveWM{t, xu}
}

func (l *liveWM) atom(name string) xproto.Atom {
	a, err := xprop.Atm(l.xu, name)
	if err != nil {
		l.t.Fatal(err)
	}
	return a
}

func (l *liveWM) root(name string, data ...interface{}) {
	cm, err := xevent.NewClientMessage(32, l.xu.RootWin(), l.atom(name), data...)
	if err != nil {
		l.t.Fatal(err)
	}
	l.send(cm)
}

func (l *liveWM) send(cm *xevent.ClientMessageEvent) {
	mask := xproto.EventMaskSubstructureNotify | xproto.EventMaskSubstructureRedirect
	if err := xevent.SendRootEvent(l.xu, cm, uint32(mask)); err != nil {
		l.t.Fatal(err)
	}
}

func (l *liveWM) newWindow(title string) xproto.Window {
	c := l.xu.Conn()
	id, err := xproto.NewWindowId(c)
	if err != nil {
		l.t.Fatal(err)
	}
	scr := l.xu.Screen()
	xproto.CreateWindow(c, scr.RootDepth, id, scr.Root, 10, 10, 200, 100, 0,
		xproto.WindowClassInputOutput, scr.RootVisual, xproto.CwBackPixel, []uint32{0xffffff})
	if err := xprop.ChangeProp(l.xu, id, 8, "WM_NAME", "STRING", []byte(title)); err != nil {
		l.t.Fatal(err)
	}
	// Input hint set: xfwm4 only focuses windows that want input.
	hints := make([]byte, 36)
	xgb.Put32(hints, 1)
	xgb.Put32(hints[4:], 1)
	xproto.ChangeProperty(c, xproto.PropModeReplace, id, xproto.AtomWmHints, xproto.AtomWmHints, 32, 9, hints)
	xproto.MapWindow(c, id)
	return id
}

func (l *liveWM) list(prop string) []uint32 {
	r, err := xproto.GetProperty(l.xu.Conn(), false, l.xu.RootWin(), l.atom(prop),
		xproto.GetPropertyTypeAny, 0, 1<<16).Reply()
	if err != nil {
		l.t.Fatal(err)
	}
	var out []uint32
	for i := 0; i+4 <= len(r.Value); i += 4 {
		out = append(out, xgb.Get32(r.Value[i:]))
	}
	return out
}

func (l *liveWM) hidden(w xproto.Window) bool {
	r, err := xproto.GetProperty(l.xu.Conn(), false, w, l.atom("_NET_WM_STATE"),
		xproto.GetPropertyTypeAny, 0, 64).Reply()
	if err != nil {
		l.t.Fatal(err)
	}
	h := l.atom("_NET_WM_STATE_HIDDEN")
	for i := 0; i+4 <= len(r.Value); i += 4 {
		if xproto.Atom(xgb.Get32(r.Value[i:])) == h {
			return true
		}
	}
	return false
}

func (l *liveWM) waitFor(what string, cond func() bool) {
	l.t.Helper()
	for i := 0; i < 100; i++ {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	l.t.Fatalf("timed out waiting for %s (active %#x, clients %x)", what, l.active(), l.list("_NET_CLIENT_LIST"))
}

// activate asks the window manager to activate w like alt+tab does.
func (l *liveWM) activate(w xproto.Window) {
	cm, err := xevent.NewClientMessage(32, w, l.atom("_NET_ACTIVE_WINDOW"), 2, 0, 0)
	if err != nil {
		l.t.Fatal(err)
	}
	l.send(cm)
}

func (l *liveWM) active() uint32 {
	if a := l.list("_NET_ACTIVE_WINDOW"); len(a) >= 1 {
		return a[0]
	}
	return 0
}

// scene maps three windows and one that is minimized before the presentation.
func (l *liveWM) scene() (a, b, c, pre xproto.Window) {
	a, b, c, pre = l.newWindow("prezilla-a"), l.newWindow("prezilla-b"), l.newWindow("prezilla-c"), l.newWindow("prezilla-pre")
	l.waitFor("windows managed", func() bool {
		cl := l.list("_NET_CLIENT_LIST")
		return slices.Contains(cl, uint32(a)) && slices.Contains(cl, uint32(b)) &&
			slices.Contains(cl, uint32(c)) && slices.Contains(cl, uint32(pre))
	})
	cm, err := xevent.NewClientMessage(32, pre, l.atom("WM_CHANGE_STATE"), 3)
	if err != nil {
		l.t.Fatal(err)
	}
	l.send(cm)
	l.waitFor("pre-minimized window hidden", func() bool { return l.hidden(pre) })
	l.t.Cleanup(func() {
		for _, w := range []xproto.Window{a, b, c, pre} {
			xproto.DestroyWindow(l.xu.Conn(), w)
		}
	})
	return
}

func TestLiveMinimize(t *testing.T) {
	for _, altTab := range []bool{false, true} {
		name := "plain"
		if altTab {
			name = "after alt+tab"
		}
		t.Run(name, func(t *testing.T) {
			l := newLiveWM(t)
			a, b, c, pre := l.scene()
			l.activate(c)
			l.waitFor("c active", func() bool { return l.active() == uint32(c) })

			ctx := context.Background()
			sw := NewMinimize(system.X11Windows{})
			saved, err := sw.Enable(ctx)
			if err != nil {
				t.Fatal(err)
			}
			t.Logf("saved %q", saved)
			for _, w := range []xproto.Window{a, b, c} {
				w := w
				l.waitFor("hidden by show desktop", func() bool { return l.hidden(w) })
			}
			if altTab {
				l.activate(b)
				l.waitFor("b active", func() bool { return l.active() == uint32(b) && !l.hidden(b) })
				t.Logf("a hidden %v, c hidden %v after activating b", l.hidden(a), l.hidden(c))
			}
			if err := sw.Disable(ctx, saved); err != nil {
				t.Fatal(err)
			}
			for _, w := range []xproto.Window{a, b, c} {
				w := w
				l.waitFor("window back", func() bool { return !l.hidden(w) })
			}
			if !l.hidden(pre) {
				t.Error("window minimized before the presentation came back")
			}
			want := c
			if altTab {
				want = b
			}
			l.waitFor("focus kept", func() bool { return l.active() == uint32(want) })
			st := l.list("_NET_CLIENT_LIST_STACKING")
			if top := st[len(st)-1]; top != uint32(want) {
				t.Errorf("top of stack %#x, want %#x (stack %x)", top, want, st)
			}
		})
	}
}

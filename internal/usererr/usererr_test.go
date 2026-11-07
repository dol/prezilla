package usererr

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

const (
	sMute        = `Couldn't change the sound: no PulseAudio or PipeWire server is running. Start your sound server, or remove "mute" from "switches" in config.json.`
	sMinimize    = `Couldn't show the desktop: no X display is reachable (Prezilla needs an X11 session). Log in to an X11 session, or remove "minimize" from "switches" in config.json.`
	sMinRestore  = `Couldn't bring your windows back: no X display is reachable (Prezilla needs an X11 session). Run "prezilla off" again in an X11 session.`
	sMuteRestore = `Couldn't restore the sound: no PulseAudio or PipeWire server is running. Start your sound server, then run "prezilla off" again.`
	sLock        = `Another Prezilla action is still running. Wait a moment and try again.`
	sWall        = `Couldn't change the wallpaper: no image is set. Set "wallpaper" to an image path in config.json, or remove "wallpaper" from "switches".`
)

func xfconfMissing() error {
	// What ExecRunner builds for a missing binary.
	return fmt.Errorf("xfconf-query: %w", &exec.Error{Name: "xfconf-query", Err: exec.ErrNotFound})
}

func cases() []struct {
	name  string
	err   error
	short string
} {
	pulse := fmt.Errorf("mute: %w", fmt.Errorf("read sink mute: %w", errors.New("pulse: connect: dial unix /run/user/1000/pulse/native: connect: no such file or directory")))
	x11 := fmt.Errorf("minimize: %w", fmt.Errorf("read showing desktop: %w", errors.New("x11: connect: cannot open display")))
	lock := errors.New("lock /s/state.json.lock: timed out after 5s waiting for another Prezilla process")
	return []struct {
		name  string
		err   error
		short string
	}{
		{"minimize show desktop", fmt.Errorf("minimize: %w", fmt.Errorf("show desktop: %w", errors.New("x11: connect: no display"))), sMinimize},
		{"mute mute sink", fmt.Errorf("mute: %w", fmt.Errorf("mute sink: %w", errors.New("pulse: connect: refused"))), sMute},
		{"minimize restore", fmt.Errorf("minimize: %w", fmt.Errorf("restore desktop: %w", errors.New("x11: connect: no display"))), sMinRestore},
		{"mute restore", fmt.Errorf("mute: %w", fmt.Errorf("unmute sink: %w", errors.New("pulse: connect: refused"))), sMuteRestore},
		{"unsupported empty desktop", errors.New(`unsupported desktop ""`), "Prezilla supports XFCE only (this session reports no desktop). Run it inside an XFCE session."},
		{"mute enable timeout", fmt.Errorf("mute: %w", fmt.Errorf("read sink mute: %w", fmt.Errorf("pulse: timed out after 5s: %w", context.DeadlineExceeded))),
			`Couldn't change the sound: the sound server didn't answer in time. Run "prezilla off", then try again.`},
		{"mute set timeout", fmt.Errorf("mute: %w", fmt.Errorf("mute sink: %w", fmt.Errorf("pulse: timed out after 5s: %w", context.DeadlineExceeded))),
			`Couldn't change the sound: the sound server didn't answer in time. Run "prezilla off", then try again.`},
		{"mute restore timeout", fmt.Errorf("mute: %w", fmt.Errorf("unmute sink: %w", fmt.Errorf("pulse: timed out after 5s: %w", context.DeadlineExceeded))),
			`Couldn't restore the sound: the sound server didn't answer in time. Run "prezilla off" again.`},
		{"minimize enable timeout", fmt.Errorf("minimize: %w", fmt.Errorf("read showing desktop: %w", fmt.Errorf("x11: timed out after 5s: %w", context.DeadlineExceeded))),
			`Couldn't show the desktop: the X server didn't answer in time. Run "prezilla off", then try again.`},
		{"minimize show timeout", fmt.Errorf("minimize: %w", fmt.Errorf("show desktop: %w", fmt.Errorf("x11: timed out after 5s: %w", context.DeadlineExceeded))),
			`Couldn't show the desktop: the X server didn't answer in time. Run "prezilla off", then try again.`},
		{"minimize restore timeout", fmt.Errorf("minimize: %w", fmt.Errorf("restore desktop: %w", fmt.Errorf("x11: timed out after 5s: %w", context.DeadlineExceeded))),
			`Couldn't bring your windows back: the X server didn't answer in time. Run "prezilla off" again.`},
		{"save state", fmt.Errorf("save state: %w", fmt.Errorf("write state /home/u/.local/state/prezilla/state.json: %w", errors.New("no space left on device"))),
			`Couldn't save Prezilla's state (/home/u/.local/state/prezilla/state.json). Check free space and permissions, then try again.`},
		{"start watcher", fmt.Errorf("start watcher: %w", errors.New("fork/exec: resource temporarily unavailable")),
			`Couldn't start the background timer, so presentation mode won't end by itself. Run "prezilla off" when you are done.`},
		{"notify no actions", errors.New("notify: server does not support actions"),
			`Your notification server can't show buttons. Use Keep presenting and Stop presenting in the tray menu.`},
		{"mute pulse", pulse, sMute},
		{"minimize x11", x11, sMinimize},
		{"icons xfconf missing", fmt.Errorf("icons: %w", xfconfMissing()),
			"Couldn't change desktop icons: xfconf-query was not found. Prezilla needs an XFCE session."},
		{"notifications xfconf missing", fmt.Errorf("notifications: %w", xfconfMissing()),
			"Couldn't change notifications: xfconf-query was not found. Prezilla needs an XFCE session."},
		{"wallpaper xfconf missing (text only)", errors.New(`wallpaper: xfconf-query -l: xfconf-query: exec: "xfconf-query": executable file not found in $PATH`),
			"Couldn't change the wallpaper: xfconf-query was not found. Prezilla needs an XFCE session."},
		{"wallpaper no image", errors.New(`wallpaper: wallpaper: no image configured (set "wallpaper" in config)`), sWall},
		{"lock", lock, sLock},
		{"parse state", errors.New("parse state /home/u/.local/state/prezilla/state.json: invalid character 'x' looking for beginning of value"),
			"Prezilla's saved state (/home/u/.local/state/prezilla/state.json) is unreadable, so it can't restore your settings automatically. Turn desktop icons, notifications, sound and windows back on and set your wallpaper again by hand (the old wallpaper path may still be readable in that file), then delete that file."},
		{"parse config", errors.New("parse config /home/u/.config/prezilla/config.json: unexpected end of JSON input"),
			"The configuration file (/home/u/.config/prezilla/config.json) isn't valid JSON. Fix it, or delete it to use the defaults."},
		{"unsupported desktop", errors.New(`unsupported desktop "GNOME"`),
			`Prezilla supports XFCE only (this session reports "GNOME"). Run it inside an XFCE session.`},
	}
}

func TestShortAndExplainPerRule(t *testing.T) {
	for _, c := range cases() {
		t.Run(c.name, func(t *testing.T) {
			if got := Short(c.err); got != c.short {
				t.Errorf("Short = %q\nwant %q", got, c.short)
			}
			want := c.short + " (details: " + c.err.Error() + ")"
			if got := Explain(c.err); got != want {
				t.Errorf("Explain = %q\nwant %q", got, want)
			}
		})
	}
}

func TestRealXfconfMissingMatchesByErrorsIs(t *testing.T) {
	err := fmt.Errorf("icons: %w", fmt.Errorf("xfconf-query: %w", exec.ErrNotFound))
	if !strings.HasPrefix(Short(err), "Couldn't change desktop icons: xfconf-query was not found.") {
		t.Errorf("Short = %q", Short(err))
	}
}

func TestUnmappedPassesThrough(t *testing.T) {
	for _, err := range []error{
		errors.New("boom"),
		errors.New(`no system tray (StatusNotifier host) found; use "prezilla toggle" from the command line`),
		fmt.Errorf("mute: %w", errors.New("pulse: set sink mute: denied")), // not a connect failure
		fmt.Errorf("minimize: %w", errors.New("x11: timed out after 5s: "+context.DeadlineExceeded.Error())),
		fmt.Errorf("mute: %w", xfconfMissing()), // cause the table does not list for mute
		errors.New("no wallpaper properties found"),
		fmt.Errorf("minimize: %w", fmt.Errorf("restore desktop: %w", fmt.Errorf("x11: %w", context.Canceled))),
		fmt.Errorf("mute: %w", fmt.Errorf("mute sink: %w", errors.New("pulse: set sink mute: timed out"))),
		fmt.Errorf("rollback mute: %w", errors.New("pulse: timed out after 5s: context deadline exceeded")),
		errors.New("save state: create state dir /x: permission denied"),
		errors.New("start watcher"),
		errors.New("notify: something else"),
		fmt.Errorf("mute: %w", errors.New("pulse: context canceled")),
		fmt.Errorf("rollback mute: %w", errors.New("pulse: connect: refused")),
		errors.New("lock /x.lock: timed out after 5s"), // not the other-process wording
	} {
		if got := Explain(err); got != err.Error() {
			t.Errorf("Explain(%q) = %q", err, got)
		}
		if got := Short(err); got != err.Error() {
			t.Errorf("Short(%q) = %q", err, got)
		}
	}
}

func TestNil(t *testing.T) {
	if Explain(nil) != "" || Short(nil) != "" {
		t.Error("nil must give an empty string")
	}
}

func TestJoinGivesOneLinePerError(t *testing.T) {
	a := fmt.Errorf("mute: %w", fmt.Errorf("mute sink: %w", errors.New("pulse: connect: refused")))
	b := fmt.Errorf("minimize: %w", fmt.Errorf("show desktop: %w", errors.New("x11: connect: no display")))
	c := errors.New("boom")
	err := errors.Join(a, errors.Join(b, c))
	want := sMute + " (details: " + a.Error() + ")\n" + sMinimize + " (details: " + b.Error() + ")\nboom"
	if got := Explain(err); got != want {
		t.Errorf("Explain = %q\nwant %q", got, want)
	}
	if got, want := Short(err), sMute+" "+sMinimize+" boom"; got != want {
		t.Errorf("Short = %q\nwant %q", got, want)
	}
}

func TestShortNeverHasDetailsAndExplainKeepsTechnicalText(t *testing.T) {
	for _, c := range cases() {
		if strings.Contains(Short(c.err), "details:") {
			t.Errorf("%s: Short has details", c.name)
		}
		if !strings.Contains(Explain(c.err), c.err.Error()) {
			t.Errorf("%s: Explain lost the technical text", c.name)
		}
	}
}

func TestProductNameSpelling(t *testing.T) {
	for _, c := range cases() {
		s := Short(c.err)
		for _, bad := range []string{"prezilla's", "Config.json", "Config.JSON"} {
			if strings.Contains(s, bad) {
				t.Errorf("%s: %q contains %q", c.name, s, bad)
			}
		}
		if strings.Contains(s, "config") && !strings.Contains(s, "config.json") && !strings.Contains(s, "configuration file") {
			t.Errorf("%s: config spelling in %q", c.name, s)
		}
	}
	for _, name := range []string{"lock", "minimize x11", "parse state", "unsupported desktop", "icons xfconf missing"} {
		for _, c := range cases() {
			if c.name == name && !strings.Contains(Short(c.err), "Prezilla") {
				t.Errorf("%s: product name not capitalised", name)
			}
		}
	}
}

func TestShortJoinPunctuation(t *testing.T) {
	if got := Short(errors.Join(errors.New("a"), errors.New("b"))); got != "a; b" {
		t.Errorf("got %q", got)
	}
	if got := Short(errors.Join(errors.New("x."), errors.New("y"))); got != "x. y" {
		t.Errorf("got %q", got)
	}
}

func TestSplit(t *testing.T) {
	err := fmt.Errorf("mute: %w", fmt.Errorf("mute sink: %w", errors.New("pulse: connect: refused")))
	s, d := Split(err)
	if s != Short(err) || d != err.Error() {
		t.Errorf("got %q, %q", s, d)
	}
	if s, d := Split(errors.New("boom")); s != "boom" || d != "" {
		t.Errorf("unmapped: %q, %q", s, d)
	}
}

func TestJoinSentences(t *testing.T) {
	for _, c := range []struct {
		parts []string
		want  string
	}{
		{nil, ""},
		{[]string{"", "a", ""}, "a"},
		{[]string{"a", "b"}, "a; b"},
		{[]string{"x.", "y"}, "x. y"},
		{[]string{"x!", "", "y?", "z"}, "x! y? z"},
	} {
		if got := JoinSentences(c.parts...); got != c.want {
			t.Errorf("%q: got %q want %q", c.parts, got, c.want)
		}
	}
}

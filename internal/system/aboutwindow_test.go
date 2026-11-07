package system

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"prezilla/internal/about"
)

// exitErr mimics *exec.ExitError for FakeRunner.
type exitErr int

func (e exitErr) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e exitErr) ExitCode() int { return int(e) }

const wantMarkup = "<b>prezilla</b> 1.2.3\nPresentation mode switcher\n\nLicense: Apache-2.0\n" +
	`<a href="https://github.com/dol/prezilla">github.com/dol/prezilla</a>`

func zenityKey(iconFlag, markup string) string {
	return strings.Join([]string{"zenity", "--question", "--title=About prezilla", "--width=380",
		"--ok-label=Open website", "--cancel-label=Close", "--modal", iconFlag, "--text=" + markup}, " ")
}

// newZenity returns a ZenityAbout over fakes; version is zenity's --version output.
func newZenity(version string) (ZenityAbout, *FakeRunner, *FakeOpener) {
	r := &FakeRunner{Out: map[string]string{"zenity --version": version}, Err: map[string]error{}}
	o := &FakeOpener{}
	return ZenityAbout{Runner: r, Opener: o}, r, o
}

func TestZenityAboutExactArgv(t *testing.T) {
	z, r, _ := newZenity("4.2.1")
	if err := z.Show(context.Background(), about.New("1.2.3")); err != nil {
		t.Fatal(err)
	}
	want := []string{"zenity --version", zenityKey("--icon=prezilla", wantMarkup)}
	if strings.Join(r.Calls, "\n---\n") != strings.Join(want, "\n---\n") {
		t.Errorf("calls\n%q\nwant\n%q", r.Calls, want)
	}
}

func TestZenityAboutIconFlagByMajorVersion(t *testing.T) {
	cases := map[string]string{
		"3.44.0":  "--icon-name=prezilla",
		"4.2.1":   "--icon=prezilla",
		"5.0":     "--icon=prezilla",
		"garbage": "--icon=prezilla",
		"":        "--icon=prezilla",
	}
	for ver, flag := range cases {
		z, r, _ := newZenity(ver)
		if err := z.Show(context.Background(), about.New("1.2.3")); err != nil {
			t.Fatalf("%q: %v", ver, err)
		}
		if len(r.Calls) != 2 || r.Calls[1] != zenityKey(flag, wantMarkup) {
			t.Errorf("version %q: calls %q, want flag %s", ver, r.Calls, flag)
		}
	}
}

func TestZenityAboutOpenWebsiteOpensOnce(t *testing.T) {
	z, _, o := newZenity("4.2.1")
	if err := z.Show(context.Background(), about.New("1.2.3")); err != nil {
		t.Fatal(err)
	}
	if len(o.Calls) != 1 || o.Calls[0] != about.Website {
		t.Errorf("opener calls %q", o.Calls)
	}
}

func TestZenityAboutClosedExitsOpenNothing(t *testing.T) {
	for _, code := range []int{1, 5, 255} {
		z, r, o := newZenity("4.2.1")
		r.Err[zenityKey("--icon=prezilla", wantMarkup)] = fmt.Errorf("zenity: %w", exitErr(code))
		if err := z.Show(context.Background(), about.New("1.2.3")); err != nil {
			t.Errorf("exit %d: %v", code, err)
		}
		if len(o.Calls) != 0 {
			t.Errorf("exit %d opened %q", code, o.Calls)
		}
	}
}

func TestZenityAboutRealFailureIsError(t *testing.T) {
	for _, cause := range []error{exitErr(2), exitErr(-1), errors.New("start failed")} {
		z, r, o := newZenity("4.2.1")
		r.Err[zenityKey("--icon=prezilla", wantMarkup)] = fmt.Errorf("zenity: %w", cause)
		err := z.Show(context.Background(), about.New("1.2.3"))
		if err == nil || errors.Is(err, ErrNoZenity) {
			t.Errorf("%v: got %v, want a non-ErrNoZenity error", cause, err)
		}
		var we *WebsiteError
		if errors.As(err, &we) {
			t.Errorf("%v: must not be a WebsiteError", cause)
		}
		if len(o.Calls) != 0 {
			t.Errorf("%v: opened %q", cause, o.Calls)
		}
	}
}

func TestZenityAboutRunnerTimeoutJustCloses(t *testing.T) {
	z, r, o := newZenity("4.2.1")
	r.Err[zenityKey("--icon=prezilla", wantMarkup)] = fmt.Errorf("zenity: timed out: %w", context.DeadlineExceeded)
	if err := z.Show(context.Background(), about.New("1.2.3")); err != nil {
		t.Errorf("got %v", err)
	}
	if len(o.Calls) != 0 {
		t.Errorf("opened %q", o.Calls)
	}
}

func TestZenityAboutCancelledContextReturnsCtxErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	z, r, o := newZenity("4.2.1")
	r.Err[zenityKey("--icon=prezilla", wantMarkup)] = fmt.Errorf("zenity: %w", exitErr(-1))
	err := z.Show(ctx, about.New("1.2.3"))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
	if len(o.Calls) != 0 {
		t.Errorf("opened %q", o.Calls)
	}
}

func TestZenityAboutOpenerFailureIsWebsiteError(t *testing.T) {
	z, _, o := newZenity("4.2.1")
	o.Err = errors.New("open: xdg-open failed")
	err := z.Show(context.Background(), about.New("1.2.3"))
	var we *WebsiteError
	want := "about: could not open https://github.com/dol/prezilla: open: xdg-open failed"
	if !errors.As(err, &we) || err.Error() != want {
		t.Errorf("got %v, want a WebsiteError %q", err, want)
	}
}

func TestZenityAboutMissingBinaryIsErrNoZenity(t *testing.T) {
	z, r, _ := newZenity("")
	cause := &exec.Error{Name: "zenity", Err: exec.ErrNotFound}
	r.Err["zenity --version"] = fmt.Errorf("zenity: %w", cause)
	err := z.Show(context.Background(), about.New("1.2.3"))
	if !errors.Is(err, ErrNoZenity) || !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("got %v, want ErrNoZenity wrapping exec.ErrNotFound", err)
	}
	if len(r.Calls) != 1 {
		t.Errorf("calls %q: the window must not run", r.Calls)
	}
}

func TestZenityAboutVersionProbeFailureIsError(t *testing.T) {
	z, r, _ := newZenity("")
	r.Err["zenity --version"] = fmt.Errorf("zenity: %w", exitErr(1))
	err := z.Show(context.Background(), about.New("1.2.3"))
	if err == nil || len(r.Calls) != 1 {
		t.Errorf("got %v, calls %q", err, r.Calls)
	}
}

func TestZenityAboutNilDependenciesAreHarmless(t *testing.T) {
	if err := (ZenityAbout{}).Show(context.Background(), about.New("1")); !errors.Is(err, ErrNoZenity) {
		t.Errorf("nil runner: %v", err)
	}
	r := &FakeRunner{Out: map[string]string{"zenity --version": "4.2.1"}}
	err := ZenityAbout{Runner: r}.Show(context.Background(), about.New("1"))
	var we *WebsiteError
	if !errors.As(err, &we) {
		t.Errorf("nil opener: %v", err)
	}
}

func TestAboutMarkupEscapesEveryValue(t *testing.T) {
	const hostile = `1<b>x</b>&"'`
	const esc = "1&lt;b&gt;x&lt;/b&gt;&amp;&quot;&apos;"
	info := about.Info{Name: hostile, Version: hostile, Comments: hostile, License: hostile,
		Website: "https://e.x/?a=1&b=" + hostile, WebsiteLabel: hostile, IconName: "x"}
	got := aboutMarkup(info)
	want := "<b>" + esc + "</b> " + esc + "\n" + esc + "\n\nLicense: " + esc + "\n" +
		`<a href="https://e.x/?a=1&amp;b=` + esc + `">` + esc + "</a>"
	if got != want {
		t.Errorf("markup\n%s\nwant\n%s", got, want)
	}
}

func TestAboutMarkupVersionEscaped(t *testing.T) {
	got := aboutMarkup(about.New(`"1<2&3"`))
	if !strings.HasPrefix(got, "<b>prezilla</b> &quot;1&lt;2&amp;3&quot;\n") {
		t.Errorf("markup %q", got)
	}
}

func TestAboutMarkupLinkOnlyForHTTP(t *testing.T) {
	for _, site := range []string{"javascript:alert(1)", "file:///etc/passwd", "ftp://x", "", "HTTPS-ish"} {
		info := about.New("1")
		info.Website = site
		info.WebsiteLabel = "label<"
		got := aboutMarkup(info)
		if strings.Contains(got, "<a ") || !strings.HasSuffix(got, "\nlabel&lt;") {
			t.Errorf("website %q: markup %q", site, got)
		}
	}
	info := about.New("1")
	info.Website, info.WebsiteLabel = "http://x.y", ""
	if got := aboutMarkup(info); !strings.HasSuffix(got, `<a href="http://x.y">http://x.y</a>`) {
		t.Errorf("empty label: %q", got)
	}
}

func TestEscapeMarkup(t *testing.T) {
	cases := map[string]string{
		`a&<>"'b`:   "a&amp;&lt;&gt;&quot;&apos;b",
		`\074b\076`: `\\074b\\076`,
		`a\nb\tc`:   `a\\nb\\tc`,
		`\\`:        `\\\\`,
		`\&\<`:      `\\&amp;\\&lt;`, // single pass: entities are not escaped again
		"&amp;":     "&amp;amp;",
	}
	for in, want := range cases {
		if got := escapeMarkup(in); got != want {
			t.Errorf("escapeMarkup(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAboutMarkupHostileBackslashVersion(t *testing.T) {
	got := aboutMarkup(about.New("1<b>x</b>&\"'\\074i\\076"))
	want := "<b>prezilla</b> 1&lt;b&gt;x&lt;/b&gt;&amp;&quot;&apos;\\\\074i\\\\076\n"
	if !strings.HasPrefix(got, want) {
		t.Errorf("markup %q, want prefix %q", got, want)
	}
}

func TestWebsiteErrorNamesURL(t *testing.T) {
	r := &FakeRunner{Out: map[string]string{"zenity --version": "4.2.1"}}
	err := ZenityAbout{Runner: r}.Show(context.Background(), about.New("1"))
	if err == nil || err.Error() != "about: could not open https://github.com/dol/prezilla: no opener configured" {
		t.Errorf("got %v", err)
	}
}

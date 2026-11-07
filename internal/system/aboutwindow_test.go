package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"prezilla/internal/about"
)

// exitErr mimics *exec.ExitError for FakeRunner.
type exitErr int

func (e exitErr) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e exitErr) ExitCode() int { return int(e) }

const wantMarkup = "<b>Prezilla</b> 1.2.3\nPresentation mode switcher\n\nLicense: Apache-2.0\n" +
	`<a href="https://github.com/dol/prezilla">github.com/dol/prezilla</a>`

func zenityKey(iconFlag, markup string) string {
	return strings.Join([]string{"zenity", "--info", "--title=About Prezilla", "--width=380",
		"--ok-label=Close", "--modal", iconFlag, "--text=" + markup}, " ")
}

// newZenity returns a ZenityAbout over a FakeRunner; version is zenity's --version output.
func newZenity(version string) (ZenityAbout, *FakeRunner) {
	r := &FakeRunner{Out: map[string]string{"zenity --version": version}, Err: map[string]error{}}
	return ZenityAbout{Runner: r}, r
}

func TestZenityAboutExactArgv(t *testing.T) {
	z, r := newZenity("4.2.1")
	if err := z.Show(context.Background(), about.New("1.2.3")); err != nil {
		t.Fatal(err)
	}
	want := []string{"zenity --version", zenityKey("--icon=prezilla", wantMarkup)}
	if strings.Join(r.Calls, "\n---\n") != strings.Join(want, "\n---\n") {
		t.Errorf("calls\n%q\nwant\n%q", r.Calls, want)
	}
	if strings.Contains(r.Calls[1], "--cancel-label") || strings.Contains(r.Calls[1], "--question") {
		t.Errorf("window must have only a Close button: %q", r.Calls[1])
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
		z, r := newZenity(ver)
		if err := z.Show(context.Background(), about.New("1.2.3")); err != nil {
			t.Fatalf("%q: %v", ver, err)
		}
		if len(r.Calls) != 2 || r.Calls[1] != zenityKey(flag, wantMarkup) {
			t.Errorf("version %q: calls %q, want flag %s", ver, r.Calls, flag)
		}
	}
}

func TestZenityAboutClosedExitsAreNil(t *testing.T) {
	for _, code := range []int{1, 5, 255} {
		z, r := newZenity("4.2.1")
		r.Err[zenityKey("--icon=prezilla", wantMarkup)] = fmt.Errorf("zenity: %w", exitErr(code))
		if err := z.Show(context.Background(), about.New("1.2.3")); err != nil {
			t.Errorf("exit %d: %v", code, err)
		}
	}
}

func TestZenityAboutRealFailureIsError(t *testing.T) {
	for _, cause := range []error{exitErr(2), exitErr(-1), errors.New("start failed")} {
		z, r := newZenity("4.2.1")
		r.Err[zenityKey("--icon=prezilla", wantMarkup)] = fmt.Errorf("zenity: %w", cause)
		err := z.Show(context.Background(), about.New("1.2.3"))
		if err == nil || errors.Is(err, ErrNoZenity) {
			t.Errorf("%v: got %v, want a non-ErrNoZenity error", cause, err)
		}
	}
}

func TestZenityAboutRunnerTimeoutJustCloses(t *testing.T) {
	z, r := newZenity("4.2.1")
	r.Err[zenityKey("--icon=prezilla", wantMarkup)] = fmt.Errorf("zenity: timed out: %w", context.DeadlineExceeded)
	if err := z.Show(context.Background(), about.New("1.2.3")); err != nil {
		t.Errorf("got %v", err)
	}
}

func TestZenityAboutCancelledContextReturnsCtxErr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	z, r := newZenity("4.2.1")
	r.Err[zenityKey("--icon=prezilla", wantMarkup)] = fmt.Errorf("zenity: %w", exitErr(-1))
	if err := z.Show(ctx, about.New("1.2.3")); !errors.Is(err, context.Canceled) {
		t.Errorf("got %v, want context.Canceled", err)
	}
}

func TestZenityAboutMissingBinaryIsErrNoZenity(t *testing.T) {
	z, r := newZenity("")
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
	z, r := newZenity("")
	r.Err["zenity --version"] = fmt.Errorf("zenity: %w", exitErr(1))
	err := z.Show(context.Background(), about.New("1.2.3"))
	if err == nil || len(r.Calls) != 1 {
		t.Errorf("got %v, calls %q", err, r.Calls)
	}
}

func TestZenityAboutNilRunnerIsErrNoZenity(t *testing.T) {
	if err := (ZenityAbout{}).Show(context.Background(), about.New("1")); !errors.Is(err, ErrNoZenity) {
		t.Errorf("nil runner: %v", err)
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
	if !strings.HasPrefix(got, "<b>Prezilla</b> &quot;1&lt;2&amp;3&quot;\n") {
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
	for _, site := range []string{"http://x.y", "https://x.y"} {
		info := about.New("1")
		info.Website = site
		if got := aboutMarkup(info); strings.Count(got, "<a href") != 1 || strings.Count(got, "</a>") != 1 {
			t.Errorf("website %q: want exactly one link, markup %q", site, got)
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
	want := "<b>Prezilla</b> 1&lt;b&gt;x&lt;/b&gt;&amp;&quot;&apos;\\\\074i\\\\076\n"
	if !strings.HasPrefix(got, want) {
		t.Errorf("markup %q, want prefix %q", got, want)
	}
}

// GTK that cannot start exits 1 like a cancelled dialog; it must fall back.
func TestZenityAboutExit1WithDeadDisplayIsError(t *testing.T) {
	for _, msg := range []string{
		"Gtk-WARNING **: cannot open display: :0",
		"(zenity:1): Gtk-WARNING **: Failed to open display",
	} {
		z, r := newZenity("4.2.1")
		r.Err[zenityKey("--icon=prezilla", wantMarkup)] = fmt.Errorf("zenity: %w: %s", exitErr(1), msg)
		err := z.Show(context.Background(), about.New("1.2.3"))
		if err == nil || errors.Is(err, ErrNoZenity) || !strings.Contains(err.Error(), msg) {
			t.Errorf("%q: got %v", msg, err)
		}
	}
}

// Exit 1 without a start-failure phrase is a normal Close, however fast.
func TestZenityAboutExit1WithOtherStderrIsClose(t *testing.T) {
	z, r := newZenity("4.2.1")
	r.Err[zenityKey("--icon=prezilla", wantMarkup)] = fmt.Errorf("zenity: %w: some harmless warning", exitErr(1))
	if err := z.Show(context.Background(), about.New("1.2.3")); err != nil {
		t.Errorf("got %v", err)
	}
}

func TestZenityAboutIconFileReplacesTheNameFromVersion4(t *testing.T) {
	cases := map[string]string{
		"3.44.0": "--icon-name=prezilla", // zenity 3 has no file form
		"4.2.1":  "--icon=/cache/prezilla.png",
	}
	for ver, flag := range cases {
		z, r := newZenity(ver)
		z.IconFile = "/cache/prezilla.png"
		if err := z.Show(context.Background(), about.New("1.2.3")); err != nil {
			t.Fatalf("%q: %v", ver, err)
		}
		if len(r.Calls) != 2 || r.Calls[1] != zenityKey(flag, wantMarkup) {
			t.Errorf("version %q: calls %q, want flag %s", ver, r.Calls, flag)
		}
	}
}

func TestWriteIconFileWritesOnceAndReplacesChangedContent(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cache", "prezilla")
	path, err := WriteIconFile(dir, "prezilla.png", []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(dir, "prezilla.png") {
		t.Errorf("path %q", path)
	}
	if got, _ := os.ReadFile(path); string(got) != "one" {
		t.Errorf("content %q", got)
	}
	if _, err := WriteIconFile(dir, "prezilla.png", []byte("two")); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != "two" {
		t.Errorf("changed content not replaced: %q", got)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("stray files left in %s: %v", dir, entries)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Errorf("mode %v", fi.Mode().Perm())
	}
}

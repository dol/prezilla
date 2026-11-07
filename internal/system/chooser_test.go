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
)

// exitErr mimics *exec.ExitError for FakeRunner.
type exitErr int

func (e exitErr) Error() string { return fmt.Sprintf("exit status %d", int(e)) }
func (e exitErr) ExitCode() int { return int(e) }

func chooserKey(startDir string) string {
	return "zenity " + strings.Join(chooserArgs(startDir), " ")
}

func TestZenityChooserReturnsTrimmedPath(t *testing.T) {
	want := ` /home/u/My "pics"/bild é ü.png `
	r := &FakeRunner{Out: map[string]string{chooserKey("/home/u"): want + "\n"}}
	got, err := ZenityChooser{Runner: r}.ChooseImage(context.Background(), "/home/u")
	if err != nil || got != want {
		t.Fatalf("got %q, %v; want %q", got, err, want)
	}
}

func TestZenityChooserExit1IsErrCancelled(t *testing.T) {
	for _, code := range []int{1, 5, 255} {
		r := &FakeRunner{Err: map[string]error{chooserKey("/d"): fmt.Errorf("zenity: %w", exitErr(code))}}
		_, err := ZenityChooser{Runner: r}.ChooseImage(context.Background(), "/d")
		if !errors.Is(err, ErrCancelled) {
			t.Errorf("exit %d: %v", code, err)
		}
	}
}

func TestZenityChooserRealFailures(t *testing.T) {
	r := &FakeRunner{Err: map[string]error{chooserKey("/d"): fmt.Errorf("zenity: %w: Gtk-WARNING: cannot open display", exitErr(1))}}
	_, err := ZenityChooser{Runner: r}.ChooseImage(context.Background(), "/d")
	if err == nil || errors.Is(err, ErrCancelled) {
		t.Errorf("display failure: %v", err)
	}
	r = &FakeRunner{Err: map[string]error{chooserKey("/d"): exitErr(2)}}
	_, err = ZenityChooser{Runner: r}.ChooseImage(context.Background(), "/d")
	if err == nil || errors.Is(err, ErrCancelled) {
		t.Errorf("exit 2: %v", err)
	}
}

func TestZenityChooserMissingIsErrNoZenity(t *testing.T) {
	r := &FakeRunner{Err: map[string]error{chooserKey("/d"): fmt.Errorf("run: %w", exec.ErrNotFound)}}
	_, err := ZenityChooser{Runner: r}.ChooseImage(context.Background(), "/d")
	if !errors.Is(err, ErrNoZenity) {
		t.Errorf("got %v", err)
	}
	_, err = ZenityChooser{}.ChooseImage(context.Background(), "/d")
	if !errors.Is(err, ErrNoZenity) {
		t.Errorf("nil runner: %v", err)
	}
}

func TestZenityChooserCtxError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &FakeRunner{Err: map[string]error{chooserKey("/d"): exitErr(1)}}
	_, err := ZenityChooser{Runner: r}.ChooseImage(ctx, "/d")
	if !errors.Is(err, context.Canceled) {
		t.Errorf("got %v", err)
	}
}

func TestZenityChooserArgs(t *testing.T) {
	r := &FakeRunner{}
	if _, err := (ZenityChooser{Runner: r}).ChooseImage(context.Background(), "/home/u/Pictures"); err != nil {
		t.Fatal(err)
	}
	if len(r.Calls) != 1 {
		t.Fatalf("calls %q", r.Calls)
	}
	call := r.Calls[0]
	for _, want := range []string{"zenity --file-selection", "--title=Choose wallpaper", "--filename=/home/u/Pictures/ "} {
		if !strings.Contains(call+" ", want) {
			t.Errorf("missing %q in %q", want, call)
		}
	}
	for _, ext := range []string{"png", "jpg", "jpeg", "webp", "bmp", "gif", "tif", "tiff", "svg"} {
		for _, pat := range []string{"*." + ext, "*." + strings.ToUpper(ext)} {
			if !strings.Contains(call, pat) {
				t.Errorf("filter lacks %s in %q", pat, call)
			}
		}
	}
	if !strings.Contains(call, "--file-filter=Images | ") {
		t.Errorf("filter header: %q", call)
	}
}

func TestFakeChooser(t *testing.T) {
	f := &FakeChooser{Path: "/a.png"}
	got, err := f.ChooseImage(context.Background(), "/x")
	if got != "/a.png" || err != nil || len(f.Calls) != 1 || f.Calls[0] != "/x" {
		t.Errorf("%q %v %v", got, err, f.Calls)
	}
}

func TestValidateImage(t *testing.T) {
	dir := t.TempDir()
	mk := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	sub := filepath.Join(dir, "sub.png")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		path string
		want error
	}{
		{"ok png", mk("a.png"), nil},
		{"uppercase", mk("b.JPG"), nil},
		{"missing", filepath.Join(dir, "none.png"), ErrImageMissing},
		{"directory", sub, ErrImageMissing},
		{"txt", mk("c.txt"), ErrNotImage},
		{"no extension", mk("d"), ErrNotImage},
	}
	for _, c := range cases {
		err := ValidateImage(c.path)
		if !errors.Is(err, c.want) || (c.want == nil) != (err == nil) {
			t.Errorf("%s: %v, want %v", c.name, err, c.want)
		}
		if err != nil && !strings.Contains(err.Error(), c.path) {
			t.Errorf("%s: message lacks path: %v", c.name, err)
		}
	}
}

func TestFallbackChooserPortalMissingUsesSecondary(t *testing.T) {
	p := &FakeChooser{Err: fmt.Errorf("file chooser: portal: %w: x", ErrNoPortal)}
	z := &FakeChooser{Path: "/pics/a.png"}
	got, err := (&FallbackChooser{Primary: p, Secondary: z}).ChooseImage(context.Background(), "/home")
	if err != nil || got != "/pics/a.png" || len(p.Calls) != 1 || len(z.Calls) != 1 || z.Calls[0] != "/home" {
		t.Fatalf("got %q, %v; calls %v %v", got, err, p.Calls, z.Calls)
	}
}

func TestFallbackChooserPrimaryResultsAreFinal(t *testing.T) {
	for name, perr := range map[string]error{
		"cancelled": ErrCancelled,
		"failed":    fmt.Errorf("file chooser: portal: %w: boom", ErrPortalFailed),
		"no zenity": ErrNoZenity, // not a portal error: no fallback by default
	} {
		p := &FakeChooser{Err: perr}
		z := &FakeChooser{Path: "/z.png"}
		_, err := (&FallbackChooser{Primary: p, Secondary: z}).ChooseImage(context.Background(), "/")
		if !errors.Is(err, perr) || len(z.Calls) != 0 {
			t.Errorf("%s: err %v, secondary calls %v", name, err, z.Calls)
		}
	}
	p := &FakeChooser{Path: "/p.png"}
	z := &FakeChooser{}
	if got, err := (&FallbackChooser{Primary: p, Secondary: z}).ChooseImage(context.Background(), "/"); got != "/p.png" || err != nil || len(z.Calls) != 0 {
		t.Errorf("chosen: %q %v %v", got, err, z.Calls)
	}
}

func TestFallbackChooserContextEndIsFinal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p := &FakeChooser{Err: fmt.Errorf("%w: %w", ErrNoPortal, context.Canceled)}
	z := &FakeChooser{Path: "/z.png"}
	if _, err := (&FallbackChooser{Primary: p, Secondary: z}).ChooseImage(ctx, "/"); err == nil || len(z.Calls) != 0 {
		t.Errorf("err %v, secondary calls %v", err, z.Calls)
	}
}

func TestFallbackChooserSecondaryCancelIsFinal(t *testing.T) {
	p := &FakeChooser{Err: ErrNoPortal}
	z := &FakeChooser{Err: ErrCancelled}
	_, err := (&FallbackChooser{Primary: p, Secondary: z}).ChooseImage(context.Background(), "/")
	if !errors.Is(err, ErrCancelled) || errors.Is(err, ErrNoChooser) {
		t.Errorf("err %v", err)
	}
}

func TestFallbackChooserBothMissingIsErrNoChooser(t *testing.T) {
	p := &FakeChooser{Err: fmt.Errorf("%w: no bus", ErrNoPortal)}
	z := &FakeChooser{Err: fmt.Errorf("%w: exec: not found", ErrNoZenity)}
	_, err := (&FallbackChooser{Primary: p, Secondary: z}).ChooseImage(context.Background(), "/")
	if !errors.Is(err, ErrNoChooser) {
		t.Errorf("err %v", err)
	}
	if _, err := (&FallbackChooser{}).ChooseImage(context.Background(), "/"); !errors.Is(err, ErrNoChooser) {
		t.Errorf("empty: %v", err)
	}
}

func TestFallbackChooserCustomShouldFallback(t *testing.T) {
	p := &FakeChooser{Err: ErrPortalFailed}
	z := &FakeChooser{Path: "/z.png"}
	fc := &FallbackChooser{Primary: p, Secondary: z, ShouldFallback: func(err error) bool { return errors.Is(err, ErrPortalFailed) }}
	if got, err := fc.ChooseImage(context.Background(), "/"); err != nil || got != "/z.png" {
		t.Errorf("got %q %v", got, err)
	}
}

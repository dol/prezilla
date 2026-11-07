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

package generic

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

var ctx = context.Background()

func image(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "slides.png")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWallpaperEnableSetsImageAndSavesOriginal(t *testing.T) {
	img := image(t)
	f := &system.FakeWallpaper{Current: "/home/u/orig.jpg"}
	saved, err := NewWallpaper(f, img).Enable(ctx)
	if err != nil || saved != "/home/u/orig.jpg" || f.Current != img {
		t.Fatalf("saved %q, err %v, current %q", saved, err, f.Current)
	}
}

func TestWallpaperDisableRestores(t *testing.T) {
	f := &system.FakeWallpaper{Current: "/slides.png"}
	if err := NewWallpaper(f, "").Disable(ctx, "/home/u/orig.jpg"); err != nil || f.Current != "/home/u/orig.jpg" {
		t.Fatalf("err %v, current %q", err, f.Current)
	}
	if err := NewWallpaper(f, "").Disable(ctx, ""); err == nil {
		t.Error("expected error for empty saved state")
	}
}

func TestWallpaperEnableRefusesWithoutRestorableOriginal(t *testing.T) {
	img := image(t)
	for name, f := range map[string]*system.FakeWallpaper{
		"get fails": {GetErr: errors.New("boom")},
		"unknown":   {},
	} {
		if _, err := NewWallpaper(f, img).Enable(ctx); err == nil || slices.ContainsFunc(f.Calls, func(c string) bool { return c != "Get" }) {
			t.Errorf("%s: err %v, calls %v", name, err, f.Calls)
		}
	}
}

func TestWallpaperEnableRejectsBadImage(t *testing.T) {
	f := &system.FakeWallpaper{Current: "/o.jpg"}
	for _, img := range []string{"", filepath.Join(t.TempDir(), "missing.png"), t.TempDir()} {
		if _, err := NewWallpaper(f, img).Enable(ctx); err == nil {
			t.Errorf("image %q: expected error", img)
		}
	}
	if len(f.Calls) != 0 {
		t.Errorf("calls %v", f.Calls)
	}
}

func TestWallpaperEnableSetFailureRollsBack(t *testing.T) {
	img := image(t)
	f := &system.FakeWallpaper{Current: "/o.jpg", SetErr: errors.New("boom")}
	if _, err := NewWallpaper(f, img).Enable(ctx); err == nil {
		t.Fatal("expected error")
	}
	if want := "Set /o.jpg"; !slices.Contains(f.Calls, want) {
		t.Errorf("no rollback in %v", f.Calls)
	}
}

func TestOnlyWallpaperIsGeneric(t *testing.T) {
	b := system.Backends{Wallpaper: &system.FakeWallpaper{}}
	cfg := core.Config{Switches: []string{core.IDMute, core.IDWallpaper, "bogus"}}
	sws := Switches(b, cfg)
	if len(sws) != 1 || sws[0].ID() != core.IDWallpaper {
		t.Fatalf("got %v", sws)
	}
	if _, ok := New(b, core.IDMute, ""); ok {
		t.Error("mute must not be generic")
	}
	if all := All(b, cfg); len(all) != 1 {
		t.Errorf("All = %v", all)
	}
}

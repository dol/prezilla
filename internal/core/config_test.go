package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestLoadConfigMissingFileGivesDefaults(t *testing.T) {
	got, err := LoadConfig(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, DefaultConfig()) {
		t.Fatalf("got %+v, want defaults", got)
	}
	if got.Timeout() != 30*time.Minute {
		t.Fatalf("timeout = %v", got.Timeout())
	}
	for _, id := range got.Switches {
		if id == IDWallpaper {
			t.Fatal("default must not include wallpaper")
		}
	}
}

func TestLoadConfigInvalidJSONNamesPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte(`{bad`), 0o600)
	if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), p) {
		t.Fatalf("err = %v", err)
	}
}

func TestLoadConfigNegativeTimeoutNamesPath(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte(`{"timeout_minutes":-1}`), 0o600)
	if _, err := LoadConfig(p); err == nil || !strings.Contains(err.Error(), p) {
		t.Fatalf("err = %v", err)
	}
}

func TestDefaultConfigSwitchOrderHasPresentationAfterNotifications(t *testing.T) {
	want := []string{IDIcons, IDNotifications, IDPresentation, IDMute, IDMinimize}
	if got := DefaultConfig().Switches; !reflect.DeepEqual(got, want) {
		t.Fatalf("switches = %v, want %v", got, want)
	}
}

func writeCfg(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadConfigAutoAddsWallpaperOnlyWithoutSwitchesKey(t *testing.T) {
	cases := []struct {
		body string
		want []string
	}{
		{`{"wallpaper":"/a.png"}`, append(DefaultConfig().Switches, IDWallpaper)},
		{`{"switches":["icons"],"wallpaper":"/a.png"}`, []string{IDIcons}},
		{`{"switches":[],"wallpaper":"/a.png"}`, []string{}},
	}
	for _, c := range cases {
		got, err := LoadConfig(writeCfg(t, c.body))
		if err != nil {
			t.Fatal(err)
		}
		if len(got.Switches) != len(c.want) || (len(c.want) > 0 && !reflect.DeepEqual(got.Switches, c.want)) {
			t.Fatalf("%s: switches = %v, want %v", c.body, got.Switches, c.want)
		}
		if got.TimeoutMinutes != 30 {
			t.Fatalf("%s: timeout = %d", c.body, got.TimeoutMinutes)
		}
	}
}

func TestWithKeepsCanonicalOrderAndDoesNotMutate(t *testing.T) {
	c := Config{Switches: []string{IDIcons, IDMute, "zzz"}}
	got := c.With(IDNotifications, true).With(IDWallpaper, true).With("aaa", true)
	want := []string{IDIcons, IDNotifications, IDMute, IDWallpaper, "zzz", "aaa"}
	if !reflect.DeepEqual(got.Switches, want) {
		t.Fatalf("got %v, want %v", got.Switches, want)
	}
	if !reflect.DeepEqual(c.Switches, []string{IDIcons, IDMute, "zzz"}) {
		t.Fatalf("receiver mutated: %v", c.Switches)
	}
	if again := got.With(IDIcons, true); !reflect.DeepEqual(again.Switches, want) {
		t.Fatalf("re-adding changed list: %v", again.Switches)
	}
	off := got.With(IDMute, false)
	if off.Has(IDMute) || !got.Has(IDMute) {
		t.Fatalf("remove: off=%v got=%v", off.Switches, got.Switches)
	}
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestUpdateConfigKeepsUnknownKeys(t *testing.T) {
	p := writeCfg(t, `{"x":{"y":1},"timeout_minutes":10}`)
	cfg, err := UpdateConfig(p, func(c *Config) { *c = c.With(IDMute, false) })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Has(IDMute) {
		t.Fatalf("returned config still has mute: %v", cfg.Switches)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFile(t, p)), &raw); err != nil {
		t.Fatal(err)
	}
	if string(raw["x"]) != `{"y":1}` && !strings.Contains(string(raw["x"]), `"y": 1`) {
		t.Fatalf("x = %s", raw["x"])
	}
	if _, ok := raw["switches"]; !ok {
		t.Fatal("switches not written")
	}
	got, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{IDIcons, IDNotifications, IDPresentation, IDMinimize}
	if !reflect.DeepEqual(got.Switches, want) || got.TimeoutMinutes != 10 {
		t.Fatalf("got %+v", got)
	}
}

func TestUpdateConfigWritesEffectiveListIncludingAutoAddedWallpaper(t *testing.T) {
	p := writeCfg(t, `{"wallpaper":"/a.png"}`)
	if _, err := UpdateConfig(p, func(c *Config) { *c = c.With(IDMute, false) }); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{IDIcons, IDNotifications, IDPresentation, IDMinimize, IDWallpaper}
	if !reflect.DeepEqual(got.Switches, want) || got.Wallpaper != "/a.png" {
		t.Fatalf("got %+v", got)
	}
}

func TestUpdateConfigMissingFileCreatesDirAndFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "dir", "config.json")
	cfg, err := UpdateConfig(p, func(c *Config) { c.Wallpaper = "/b.png" })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Wallpaper != "/b.png" {
		t.Fatalf("cfg = %+v", cfg)
	}
	body := readFile(t, p)
	if !strings.HasSuffix(body, "}\n") || !strings.Contains(body, "\n  \"switches\"") {
		t.Fatalf("body not 2-space indented with newline: %q", body)
	}
	if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o644 {
		t.Fatalf("stat = %v, %v", fi, err)
	}
	got, err := LoadConfig(p)
	if err != nil || got.Wallpaper != "/b.png" || got.TimeoutMinutes != 30 {
		t.Fatalf("got %+v, %v", got, err)
	}
}

func TestUpdateConfigRefusesUnparsableFileAndLeavesItUntouched(t *testing.T) {
	p := writeCfg(t, `{bad`)
	called := false
	_, err := UpdateConfig(p, func(c *Config) { called = true })
	if err == nil || !strings.Contains(err.Error(), "parse config") || !strings.Contains(err.Error(), p) {
		t.Fatalf("err = %v", err)
	}
	if called {
		t.Fatal("fn called on unparsable file")
	}
	if readFile(t, p) != `{bad` {
		t.Fatal("file modified")
	}
	entries, _ := os.ReadDir(filepath.Dir(p))
	if len(entries) != 1 {
		t.Fatalf("stray files: %v", entries)
	}
}

func TestUpdateConfigDropsEmptyWallpaperKey(t *testing.T) {
	p := writeCfg(t, `{"switches":["icons"],"wallpaper":"/a.png"}`)
	if _, err := UpdateConfig(p, func(c *Config) { c.Wallpaper = "" }); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(readFile(t, p), "wallpaper") {
		t.Fatalf("wallpaper key kept: %s", readFile(t, p))
	}
}

func TestUpdateConfigWritesThroughSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "dotfiles", "config.json")
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte(`{"switches":["icons"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "prezilla", "config.json")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateConfig(link, func(c *Config) { *c = c.With(IDMute, true) }); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(link)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("config.json is no longer a symlink (mode %v, err %v)", fi.Mode(), err)
	}
	cfg, err := LoadConfig(target)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Switches, []string{"icons", "mute"}) {
		t.Errorf("target switches = %v, want [icons mute]", cfg.Switches)
	}
}

func TestUpdateConfigKeepsFileMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"switches":["icons"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateConfig(path, func(c *Config) { *c = c.With(IDMute, true) }); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("mode = %v, want 0600", got)
	}
}

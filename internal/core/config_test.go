package core

import (
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

func TestLoadConfigWallpaperImpliesSwitch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	os.WriteFile(p, []byte(`{"switches":["icons"],"wallpaper":"/x.png"}`), 0o600)
	got, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Switches, []string{IDIcons, IDWallpaper}) {
		t.Fatalf("switches = %v", got.Switches)
	}
	if got.TimeoutMinutes != 30 {
		t.Fatalf("omitted timeout should keep 30, got %d", got.TimeoutMinutes)
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

package cli

import (
	"bytes"
	"errors"
	"slices"
	"testing"

	"prezilla/internal/core"
)

func TestResolveConfigFallbackOrFail(t *testing.T) {
	broken := errors.New("parse config /x/config.json: bad")
	custom := core.Config{Switches: []string{core.IDIcons}, TimeoutMinutes: 5}
	cases := []struct {
		cmd      string
		err      error
		fallback bool // want defaults + warning
		fail     bool
	}{
		{"on", nil, false, false},
		{"off", nil, false, false},
		{"on", broken, false, true},
		{"toggle", broken, false, true},
		{"off", broken, true, false},
		{"status", broken, true, false},
		{"watch", broken, true, false},
		{"tray", broken, true, false},
		{"bogus", broken, false, true},
	}
	for _, c := range cases {
		var errOut bytes.Buffer
		cfg, err := ResolveConfig(c.cmd, custom, c.err, &errOut)
		switch {
		case c.fail:
			if err == nil || err != c.err {
				t.Errorf("%s/%v: err = %v, want config error", c.cmd, c.err, err)
			}
		case c.fallback:
			if err != nil || !slices.Equal(cfg.Switches, core.DefaultConfig().Switches) || cfg.TimeoutMinutes != core.DefaultConfig().TimeoutMinutes {
				t.Errorf("%s: cfg=%+v err=%v, want defaults", c.cmd, cfg, err)
			}
			want := "warning: " + broken.Error() + "; using default configuration\n"
			if errOut.String() != want {
				t.Errorf("%s: stderr %q want %q", c.cmd, errOut.String(), want)
			}
		default:
			if err != nil || !slices.Equal(cfg.Switches, custom.Switches) || errOut.Len() != 0 {
				t.Errorf("%s: cfg=%+v err=%v stderr=%q", c.cmd, cfg, err, errOut.String())
			}
		}
	}
}

func TestNeedsDesktopSkipsVersionAndAbout(t *testing.T) {
	for cmd, want := range map[string]bool{"version": false, "about": false, "on": true, "off": true, "status": true, "tray": true, "": true} {
		if got := NeedsDesktop(cmd); got != want {
			t.Errorf("NeedsDesktop(%q) = %v, want %v", cmd, got, want)
		}
	}
}

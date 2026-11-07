package system

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPulseAudioNoServerErrorsFast(t *testing.T) {
	t.Setenv("PULSE_SERVER", "unix:"+filepath.Join(t.TempDir(), "nonexistent"))
	start := time.Now()
	_, err := PulseAudio{}.DefaultSinkName(context.Background())
	if err == nil || !strings.Contains(err.Error(), "pulse: connect:") {
		t.Fatalf("got %v", err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %s", d)
	}
}

func TestPulseAudioHonoursContext(t *testing.T) {
	t.Setenv("PULSE_SERVER", "unix:"+filepath.Join(t.TempDir(), "nonexistent"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	if _, err := (PulseAudio{}).SinkExists(ctx, "x"); err == nil {
		t.Fatal("expected error")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("took %s", d)
	}
}

func TestPulseAudioTimeoutDefault(t *testing.T) {
	if got := (PulseAudio{}).timeout(); got != DefaultTimeout {
		t.Fatalf("got %s", got)
	}
}

func TestFakeAudioRecordsAndUpdates(t *testing.T) {
	f := &FakeAudio{Default: "a", Sinks: []string{"a"}}
	if err := f.SetSinkMute(context.Background(), "a", true); err != nil {
		t.Fatal(err)
	}
	m, err := f.SinkMuted(context.Background(), "a")
	if err != nil || !m {
		t.Fatalf("got (%v, %v)", m, err)
	}
	if ok, _ := f.SinkExists(context.Background(), "b"); ok {
		t.Fatal("b exists")
	}
}

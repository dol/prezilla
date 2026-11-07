package system

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestExecRunnerTrimsStdout(t *testing.T) {
	out, err := ExecRunner{}.Run(context.Background(), "echo", "hi")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "hi" {
		t.Fatalf("got %q, want %q", out, "hi")
	}
}

func TestExecRunnerMissingBinaryErrorNamesCommand(t *testing.T) {
	_, err := ExecRunner{}.Run(context.Background(), "prezilla-no-such-bin")
	if err == nil {
		t.Fatal("expected error for missing binary")
	}
	if !strings.Contains(err.Error(), "prezilla-no-such-bin") {
		t.Fatalf("error %q does not name the command", err)
	}
}

func TestExecRunnerNonZeroIncludesStderr(t *testing.T) {
	_, err := ExecRunner{}.Run(context.Background(), "sh", "-c", "echo boom >&2; exit 3")
	if err == nil {
		t.Fatal("expected error for non-zero exit")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error %q does not include stderr", err)
	}
}

func TestFakeRunnerRecordsAndReplays(t *testing.T) {
	f := &FakeRunner{
		Out: map[string]string{"xfconf-query -c x -p /y": "value"},
		Err: map[string]error{"bad cmd": context.Canceled},
	}

	out, err := f.Run(context.Background(), "xfconf-query", "-c", "x", "-p", "/y")
	if err != nil || out != "value" {
		t.Fatalf("got (%q, %v), want (\"value\", nil)", out, err)
	}

	if _, err := f.Run(context.Background(), "bad", "cmd"); err != context.Canceled {
		t.Fatalf("got err %v, want context.Canceled", err)
	}

	out, err = f.Run(context.Background(), "unknown")
	if err != nil || out != "" {
		t.Fatalf("unknown key: got (%q, %v), want (\"\", nil)", out, err)
	}

	want := []string{"xfconf-query -c x -p /y", "bad cmd", "unknown"}
	if len(f.Calls) != len(want) {
		t.Fatalf("Calls = %v, want %v", f.Calls, want)
	}
	for i := range want {
		if f.Calls[i] != want[i] {
			t.Fatalf("Calls[%d] = %q, want %q", i, f.Calls[i], want[i])
		}
	}
}

func TestExecRunnerTimesOutAndNamesCommand(t *testing.T) {
	start := time.Now()
	_, err := ExecRunner{Timeout: 100 * time.Millisecond}.Run(context.Background(), "sleep", "5")
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("took %s", d)
	}
	if err == nil || !strings.Contains(err.Error(), "sleep") || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err does not wrap DeadlineExceeded: %v", err)
	}
}

func TestExecRunnerDefaultTimeoutIsTenSeconds(t *testing.T) {
	if DefaultTimeout != 10*time.Second {
		t.Errorf("DefaultTimeout = %s", DefaultTimeout)
	}
	if got := (ExecRunner{}).timeout(); got != DefaultTimeout {
		t.Errorf("zero Timeout resolves to %s", got)
	}
	if got := (ExecRunner{Timeout: time.Second}).timeout(); got != time.Second {
		t.Errorf("explicit Timeout resolves to %s", got)
	}
}

func TestExecRunnerParentCancelStillReported(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()
	_, err := ExecRunner{Timeout: 5 * time.Second}.Run(ctx, "sleep", "5")
	if err == nil || strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v", err)
	}
}

func TestExecRunnerLingeringGrandchildYieldsErrWaitDelay(t *testing.T) {
	start := time.Now()
	_, err := ExecRunner{Timeout: 5 * time.Second}.Run(context.Background(), "sh", "-c", "sleep 3 & exit 0")
	if !errors.Is(err, exec.ErrWaitDelay) {
		t.Fatalf("err = %v, want ErrWaitDelay", err)
	}
	if d := time.Since(start); d > 4*time.Second {
		t.Errorf("took %v", d)
	}
}

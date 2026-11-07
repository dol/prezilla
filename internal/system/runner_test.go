package system

import (
	"context"
	"strings"
	"testing"
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

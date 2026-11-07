package system

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Runner executes external commands and returns their trimmed stdout.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// DefaultTimeout bounds one external command when ExecRunner.Timeout is zero.
const DefaultTimeout = 10 * time.Second

// pipeWaitDelay bounds how long Wait lingers for a child that keeps the
// output pipes open after the command was killed.
const pipeWaitDelay = 2 * time.Second

// ExecRunner runs commands via os/exec. The zero value is valid.
type ExecRunner struct {
	// Timeout bounds each command; zero means DefaultTimeout.
	Timeout time.Duration
}

func (r ExecRunner) timeout() time.Duration {
	if r.Timeout > 0 {
		return r.Timeout
	}
	return DefaultTimeout
}

// Run executes name with args. On failure the error includes the command
// name and any stderr output. A command that outlives the timeout is killed
// and reported as timed out (wrapping context.DeadlineExceeded).
func (r ExecRunner) Run(parent context.Context, name string, args ...string) (string, error) {
	timeout := r.timeout()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.WaitDelay = pipeWaitDelay
	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && parent.Err() == nil {
			return "", fmt.Errorf("%s: timed out after %s: %w", name, timeout, context.DeadlineExceeded)
		}
		msg := strings.TrimSpace(stderr.String())
		if msg != "" {
			return "", fmt.Errorf("%s: %w: %s", name, err, msg)
		}
		return "", fmt.Errorf("%s: %w", name, err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// FakeRunner records calls and replays canned output for tests.
type FakeRunner struct {
	Calls []string
	Out   map[string]string
	Err   map[string]error
}

// Run records the command line and returns the canned output and error for it.
// Unknown command lines return ("", nil).
func (f *FakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	key := strings.Join(append([]string{name}, args...), " ")
	f.Calls = append(f.Calls, key)
	return f.Out[key], f.Err[key]
}

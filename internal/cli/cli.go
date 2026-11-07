// Package cli implements the prezilla command line.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"prezilla/internal/core"
)

// Deps are the injected collaborators of the CLI.
type Deps struct {
	Engine         *core.Engine
	Tray           func(ctx context.Context) error
	SpawnWatcher   func() error
	DefaultTimeout time.Duration
	PollInterval   time.Duration
	Version        string
}

const usage = "usage: prezilla <on [--for DURATION] | off | toggle [--for DURATION] | status | tray | version>"

// Run executes the command in args and returns the process exit code.
func Run(ctx context.Context, args []string, out, errOut io.Writer, d Deps) int {
	if len(args) == 0 {
		fmt.Fprintln(errOut, usage)
		return 2
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "version":
		fmt.Fprintln(out, d.Version)
		return 0
	case "on", "toggle":
		return runEnable(ctx, cmd, rest, errOut, d)
	case "off":
		if err := d.Engine.Disable(ctx); err != nil {
			return fail(errOut, err)
		}
		return 0
	case "status":
		return runStatus(ctx, out, errOut, d)
	case "tray":
		if err := d.Tray(ctx); err != nil {
			return fail(errOut, err)
		}
		return 0
	case "watch":
		return runWatch(ctx, errOut, d)
	}
	fmt.Fprintf(errOut, "unknown command %q\n%s\n", cmd, usage)
	return 2
}

func fail(errOut io.Writer, err error) int {
	fmt.Fprintln(errOut, err)
	return 1
}

func runEnable(ctx context.Context, cmd string, args []string, errOut io.Writer, d Deps) int {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	forFlag := fs.String("for", "", "auto-off duration, 0 for none")
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(errOut, usage)
		return 2
	}
	timeout := d.DefaultTimeout
	if *forFlag != "" {
		var err error
		if timeout, err = time.ParseDuration(*forFlag); err != nil || timeout < 0 {
			fmt.Fprintf(errOut, "invalid --for duration %q\n%s\n", *forFlag, usage)
			return 2
		}
	}

	var err error
	if cmd == "on" {
		err = d.Engine.Enable(ctx, timeout)
	} else {
		if _, derr := d.Engine.DisableIfExpired(ctx); derr != nil {
			return fail(errOut, derr)
		}
		_, err = d.Engine.Toggle(ctx, timeout)
	}
	code := 0
	if err != nil {
		fmt.Fprintln(errOut, err)
		code = 1
	}
	if st, serr := d.Engine.Status(); serr != nil {
		fmt.Fprintln(errOut, serr)
		code = 1
	} else if st.Active && !st.ExpiresAt.IsZero() && d.SpawnWatcher != nil {
		if werr := d.SpawnWatcher(); werr != nil {
			fmt.Fprintf(errOut, "start watcher: %v\n", werr)
			code = 1
		}
	}
	return code
}

func runStatus(ctx context.Context, out, errOut io.Writer, d Deps) int {
	if _, err := d.Engine.DisableIfExpired(ctx); err != nil {
		return fail(errOut, err)
	}
	st, err := d.Engine.Status()
	if err != nil {
		return fail(errOut, err)
	}
	switch {
	case !st.Active:
		fmt.Fprintln(out, "inactive")
	case st.ExpiresAt.IsZero():
		fmt.Fprintln(out, "active")
	default:
		fmt.Fprintf(out, "active until %s\n", st.ExpiresAt.Local().Format("15:04"))
	}
	return 0
}

// runWatch polls until the mode is inactive. A failed disable on expiry is
// retried on the next poll; the engine only retries entries still saved.
func runWatch(ctx context.Context, errOut io.Writer, d Deps) int {
	for {
		st, err := d.Engine.Status()
		if err != nil {
			return fail(errOut, err)
		}
		if !st.Active {
			return 0
		}
		if _, err := d.Engine.DisableIfExpired(ctx); err != nil {
			fmt.Fprintln(errOut, err)
		}
		select {
		case <-ctx.Done():
			return 0
		case <-time.After(d.PollInterval):
		}
	}
}

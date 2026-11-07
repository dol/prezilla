package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"prezilla/internal/cli"
	"prezilla/internal/core"
	"prezilla/internal/platform"
	"prezilla/internal/system"
	"prezilla/internal/tray"
)

var version = "dev"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx))
}

func run(ctx context.Context) int {
	args := os.Args[1:]
	d := cli.Deps{
		SpawnWatcher: spawnWatcher,
		PollInterval: 10 * time.Second,
		Version:      version,
	}
	if len(args) > 0 && args[0] == "version" {
		return cli.Run(ctx, args, os.Stdout, os.Stderr, d)
	}

	cfg, err := core.LoadConfig(core.DefaultConfigPath())
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	desktop, err := platform.Detect(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	switches, err := platform.Switches(desktop, system.ExecRunner{}, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	d.Engine = core.NewEngine(switches, core.NewStore(core.DefaultStatePath()), time.Now)
	d.DefaultTimeout = cfg.Timeout()
	d.Tray = func(ctx context.Context) error { return tray.Run(ctx, d.Engine, cfg.Timeout()) }
	return cli.Run(ctx, args, os.Stdout, os.Stderr, d)
}

// spawnWatcher starts a detached "prezilla watch" that enforces the deadline.
func spawnWatcher() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devnull.Close()
	cmd := exec.Command(exe, "watch")
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}

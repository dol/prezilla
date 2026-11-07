package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"prezilla/internal/about"
	"prezilla/internal/cli"
	"prezilla/internal/core"
	"prezilla/internal/platform"
	"prezilla/internal/system"
	"prezilla/internal/tray"
	"prezilla/internal/usererr"
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
	if len(args) > 0 && !cli.NeedsDesktop(args[0]) {
		return cli.Run(ctx, args, os.Stdout, os.Stderr, d)
	}

	cmd := ""
	if len(args) > 0 {
		cmd = args[0]
	}
	cfg, err := core.LoadConfig(core.DefaultConfigPath())
	if cfg, err = cli.ResolveConfig(cmd, cfg, err, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, usererr.Explain(err))
		return 1
	}
	desktop, err := platform.Detect(os.Getenv)
	if err != nil {
		fmt.Fprintln(os.Stderr, usererr.Explain(err))
		return 1
	}
	backends := system.Backends{Runner: system.ExecRunner{}, Audio: system.PulseAudio{}, Windows: system.X11Windows{}}
	switches, err := platform.Switches(desktop, backends, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, usererr.Explain(err))
		return 1
	}
	all, err := platform.AllSwitches(desktop, backends, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, usererr.Explain(err))
		return 1
	}
	d.Engine = core.NewEngine(switches, core.NewStore(core.DefaultStatePath()), time.Now)
	d.Engine.SetRestorers(all)
	d.DefaultTimeout = cfg.Timeout()
	d.Tray = func(ctx context.Context) error {
		// The About window waits until it is closed, so its runner gets a
		// long timeout; Quit still closes it through ctx. zenity 4 reads
		// --icon=prezilla as a file path first, so run it from "/" where no
		// such file can shadow the icon name.
		aboutWindow := system.ZenityAbout{Runner: system.ExecRunner{Timeout: 30 * time.Minute, Dir: "/"}}
		return tray.Run(ctx, tray.Config{
			Engine: d.Engine, Timeout: cfg.Timeout(), Spawn: spawnWatcher,
			About:       about.New(version),
			Notifier:    system.DBusNotifier{},
			AboutWindow: aboutWindow,
			ScreenSaver: system.DBusScreenSaver{},
			Prompter:    system.DBusPrompter{},
		})
	}
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

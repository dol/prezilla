package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"prezilla/internal/about"
	"prezilla/internal/cli"
	"prezilla/internal/core"
	"prezilla/internal/platform"
	"prezilla/internal/system"
	"prezilla/internal/tray"
	"prezilla/internal/tray/icons"
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
	backends := system.Backends{Runner: system.ExecRunner{}, Audio: system.PulseAudio{}, Windows: system.X11Windows{}, Xfconf: system.DBusXfconf{}}
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
		// Without zenity (or when it cannot start) the built-in X11 window
		// takes over; the tray falls back to a notification if that fails.
		// The logo is written to the cache directory so zenity gets a file
		// path: the icon theme has no "prezilla" entry until the application
		// is installed.
		zenity := system.ZenityAbout{Runner: system.ExecRunner{Timeout: 30 * time.Minute, Dir: "/"}, IconFile: iconFile()}
		aboutWindow := &system.FallbackAbout{
			Primary:   zenity,
			Secondary: system.X11About{Icon: icons.App},
			Note: func(error) {
				fmt.Fprintln(os.Stderr, "about: zenity not available; showing the built-in window instead")
			},
		}
		return tray.Run(ctx, tray.Config{
			Engine: d.Engine, Timeout: cfg.Timeout(), Spawn: spawnWatcher,
			About:       about.New(version),
			Notifier:    system.DBusNotifier{},
			AboutWindow: aboutWindow,
			ScreenSaver: system.DBusScreenSaver{},
			Prompter:    system.DBusPrompter{},
			ConfigPath:  core.DefaultConfigPath(),
			// Like the About window, the chooser waits for the user and
			// runs from "/".
			Chooser:  system.ZenityChooser{Runner: system.ExecRunner{Timeout: 30 * time.Minute, Dir: "/"}},
			StartDir: homeDir(),
			SwitchFor: func(id, image string) (core.Switch, error) {
				return platform.Switch(desktop, backends, id, image)
			},
			SwitchesFor: func(c core.Config) ([]core.Switch, error) {
				return platform.Switches(desktop, backends, c)
			},
		})
	}
	return cli.Run(ctx, args, os.Stdout, os.Stderr, d)
}

// homeDir is where the wallpaper chooser opens: the user's home, or "/".
func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		return h
	}
	return "/"
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

// iconFile writes the embedded application icon under the user cache directory
// and returns its path, or "" when that fails (zenity then uses the icon name).
func iconFile() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	path, err := system.WriteIconFile(filepath.Join(dir, "prezilla"), "prezilla.png", icons.App)
	if err != nil {
		return ""
	}
	return path
}

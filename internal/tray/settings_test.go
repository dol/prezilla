package tray

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

// fakeSwitch is a core.Switch built by the fake SwitchFor; it records the
// image it was built with and is never enabled by the fake engine.
type fakeSwitch struct{ id, image string }

func (s fakeSwitch) ID() string                             { return s.id }
func (s fakeSwitch) Enable(context.Context) (string, error) { return "orig-" + s.id, nil }
func (s fakeSwitch) Disable(context.Context, string) error  { return nil }

// fakeSettingsEngine implements settingsEngine. It records each call as
// "enable <id> <image>" or "disable <id>" and, while st.Active, updates
// st.Saved like the real engine.
type fakeSettingsEngine struct {
	st    core.State
	stErr error
	opErr error
	ended bool // reports (false, nil) from EnableOne/DisableOne
	calls []string
	// deadlines records the ctx deadline of each EnableOne/DisableOne call
	// (zero when there is none).
	deadlines []time.Time
}

func (f *fakeSettingsEngine) noteDeadline(ctx context.Context) {
	d, _ := ctx.Deadline()
	f.deadlines = append(f.deadlines, d)
}

func (f *fakeSettingsEngine) Status() (core.State, error) { return f.st, f.stErr }

func (f *fakeSettingsEngine) EnableOne(ctx context.Context, sw core.Switch) (bool, error) {
	f.noteDeadline(ctx)
	img := ""
	if fs, ok := sw.(fakeSwitch); ok {
		img = fs.image
	}
	f.calls = append(f.calls, "enable "+sw.ID()+" "+img)
	if f.ended || !f.st.Active {
		return false, nil
	}
	if f.opErr != nil {
		return true, f.opErr
	}
	if f.st.Saved == nil {
		f.st.Saved = map[string]string{}
	}
	f.st.Saved[sw.ID()] = "orig"
	return true, nil
}

func (f *fakeSettingsEngine) DisableOne(ctx context.Context, id string) (bool, error) {
	f.noteDeadline(ctx)
	f.calls = append(f.calls, "disable "+id)
	if f.ended || !f.st.Active {
		return false, nil
	}
	if f.opErr != nil {
		return true, f.opErr
	}
	delete(f.st.Saved, id)
	return true, nil
}

type notice struct{ summary, body string }

type settingsFixture struct {
	s       *settings
	eng     *fakeSettingsEngine
	chooser *system.FakeChooser
	notes   []notice
	dir     string
}

func newSettingsFixture(t *testing.T) *settingsFixture {
	t.Helper()
	dir := t.TempDir()
	f := &settingsFixture{eng: &fakeSettingsEngine{}, chooser: &system.FakeChooser{}, dir: dir}
	f.s = &settings{
		ConfigPath: filepath.Join(dir, "cfg", "config.json"),
		Engine:     f.eng,
		SwitchFor: func(id, image string) (core.Switch, error) {
			return fakeSwitch{id: id, image: image}, nil
		},
		Chooser: f.chooser,
		Notify: func(_ context.Context, summary, body string) error {
			f.notes = append(f.notes, notice{summary, body})
			return nil
		},
		StartDir: "/home/u/Pictures",
	}
	return f
}

// writeConfig writes raw JSON to the fixture's config path.
func (f *settingsFixture) writeConfig(t *testing.T, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(f.s.ConfigPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.s.ConfigPath, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f *settingsFixture) readConfigText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(f.s.ConfigPath)
	if errors.Is(err, os.ErrNotExist) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (f *settingsFixture) config(t *testing.T) core.Config {
	t.Helper()
	cfg, err := core.LoadConfig(f.s.ConfigPath)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// image creates an image file in the fixture's temp dir and returns its path.
func (f *settingsFixture) image(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(f.dir, name)
	if err := os.WriteFile(p, []byte("img"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func (f *settingsFixture) activate(saved ...string) {
	f.eng.st = core.State{Active: true, Saved: map[string]string{}}
	for _, id := range saved {
		f.eng.st.Saved[id] = "orig"
	}
}

func ticked(m map[string]bool) []string {
	var out []string
	for _, id := range core.SwitchOrder {
		if m[id] {
			out = append(out, id)
		}
	}
	return out
}

func TestTickItemsFollowSwitchOrder(t *testing.T) {
	want := []tickItem{
		{ID: core.IDIcons, Title: "Hide desktop icons"},
		{ID: core.IDNotifications, Title: "Disable notifications"},
		{ID: core.IDPresentation, Title: "Keep screen awake"},
		{ID: core.IDMute, Title: "Mute sound"},
		{ID: core.IDMinimize, Title: "Minimize windows"},
		{ID: core.IDWallpaper, Title: "Change wallpaper"},
	}
	if len(tickItems) != len(want) {
		t.Fatalf("tickItems: got %d, want %d", len(tickItems), len(want))
	}
	for i, w := range want {
		if tickItems[i].ID != w.ID || tickItems[i].Title != w.Title || tickItems[i].Tooltip == "" {
			t.Errorf("tickItems[%d] = %+v, want ID %q title %q and a tooltip", i, tickItems[i], w.ID, w.Title)
		}
	}
	if chooseWallpaperTitle != "Choose wallpaper…" {
		t.Errorf("chooseWallpaperTitle = %q", chooseWallpaperTitle)
	}
}

func TestTicksInactiveReadConfig(t *testing.T) {
	f := newSettingsFixture(t)
	got, err := f.s.Ticks()
	if err != nil {
		t.Fatal(err)
	}
	if want := core.DefaultConfig().Switches; !slices.Equal(ticked(got), want) {
		t.Errorf("missing file: got %v, want defaults %v", ticked(got), want)
	}
	f.writeConfig(t, `{"switches": ["mute", "icons"]}`)
	got, err = f.s.Ticks()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"icons", "mute"}; !slices.Equal(ticked(got), want) {
		t.Errorf("got %v, want %v", ticked(got), want)
	}
	for _, id := range core.SwitchOrder {
		if _, ok := got[id]; !ok {
			t.Errorf("Ticks lacks key %q", id)
		}
	}
}

func TestTicksActiveReadSaved(t *testing.T) {
	f := newSettingsFixture(t)
	f.writeConfig(t, `{"switches": ["icons", "mute"]}`)
	f.activate("icons", "notifications")
	got, err := f.s.Ticks()
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"icons", "notifications"}; !slices.Equal(ticked(got), want) {
		t.Errorf("got %v, want %v (mute is in the config but not saved)", ticked(got), want)
	}
}

func TestTicksBrokenConfigReturnsError(t *testing.T) {
	f := newSettingsFixture(t)
	f.writeConfig(t, `{"switches": [`)
	if _, err := f.s.Ticks(); err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Errorf("got %v, want a parse config error", err)
	}
	f.activate("icons")
	if _, err := f.s.Ticks(); err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Errorf("active: got %v, want a parse config error", err)
	}
	if err := f.s.SetTick(context.Background(), core.IDMute, true); err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Errorf("SetTick: got %v, want a parse config error", err)
	}
	if len(f.eng.calls) != 0 {
		t.Errorf("engine called with a broken config: %v", f.eng.calls)
	}
}

func TestSetTickInactiveWritesConfigOnly(t *testing.T) {
	f := newSettingsFixture(t)
	if err := f.s.SetTick(context.Background(), core.IDMute, false); err != nil {
		t.Fatal(err)
	}
	if err := f.s.SetTick(context.Background(), core.IDIcons, false); err != nil {
		t.Fatal(err)
	}
	if want := []string{"notifications", "presentation", "minimize"}; !slices.Equal(f.config(t).Switches, want) {
		t.Errorf("config switches %v, want %v", f.config(t).Switches, want)
	}
	if err := f.s.SetTick(context.Background(), core.IDIcons, true); err != nil {
		t.Fatal(err)
	}
	if want := []string{"icons", "notifications", "presentation", "minimize"}; !slices.Equal(f.config(t).Switches, want) {
		t.Errorf("config switches %v, want %v", f.config(t).Switches, want)
	}
	// The engine is asked (it reports "not active" under its lock), but
	// nothing is applied or saved.
	if f.eng.st.Active || len(f.eng.st.Saved) != 0 {
		t.Errorf("engine state changed: %+v", f.eng.st)
	}
}

func TestSetTickActiveEnablesThenWrites(t *testing.T) {
	f := newSettingsFixture(t)
	img := f.image(t, "pres.png")
	f.writeConfig(t, `{"switches": ["icons"], "wallpaper": "`+img+`"}`)
	f.activate("icons")
	if err := f.s.SetTick(context.Background(), core.IDMute, true); err != nil {
		t.Fatal(err)
	}
	if want := []string{"enable mute " + img}; !slices.Equal(f.eng.calls, want) {
		t.Errorf("calls %v, want %v", f.eng.calls, want)
	}
	if want := []string{"icons", "mute"}; !slices.Equal(f.config(t).Switches, want) {
		t.Errorf("config switches %v, want %v", f.config(t).Switches, want)
	}
	got, _ := f.s.Ticks()
	if !got[core.IDMute] {
		t.Errorf("mute not ticked after enable: %v", got)
	}
}

func TestSetTickActiveUntickRestoresThenWrites(t *testing.T) {
	f := newSettingsFixture(t)
	f.writeConfig(t, `{"switches": ["icons", "mute"]}`)
	f.activate("icons", "mute")
	if err := f.s.SetTick(context.Background(), core.IDMute, false); err != nil {
		t.Fatal(err)
	}
	if want := []string{"disable mute"}; !slices.Equal(f.eng.calls, want) {
		t.Errorf("calls %v, want %v", f.eng.calls, want)
	}
	if want := []string{"icons"}; !slices.Equal(f.config(t).Switches, want) {
		t.Errorf("config switches %v, want %v", f.config(t).Switches, want)
	}
	if _, ok := f.eng.st.Saved[core.IDMute]; ok {
		t.Error("mute still saved")
	}
}

func TestSetTickEngineFailureWritesNothing(t *testing.T) {
	for _, on := range []bool{true, false} {
		f := newSettingsFixture(t)
		const raw = `{"switches": ["icons", "mute"]}`
		f.writeConfig(t, raw)
		f.activate("icons")
		if !on {
			f.eng.st.Saved[core.IDMute] = "orig"
		}
		f.eng.opErr = errors.New("mute: pactl failed")
		id := core.IDMute
		if on {
			id = core.IDNotifications
		}
		err := f.s.SetTick(context.Background(), id, on)
		if err == nil || !strings.Contains(err.Error(), "pactl failed") {
			t.Errorf("on=%v: got %v, want the engine error", on, err)
		}
		if got := f.readConfigText(t); got != raw {
			t.Errorf("on=%v: config changed to %q", on, got)
		}
	}
}

func TestSetTickConfigWriteFailureReturnsSaveError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	f := newSettingsFixture(t)
	f.writeConfig(t, `{"switches": ["icons"]}`)
	dir := filepath.Dir(f.s.ConfigPath)
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(dir, 0o755) })
	f.activate("icons")
	err := f.s.SetTick(context.Background(), core.IDMute, true)
	if err == nil || !strings.Contains(err.Error(), "write config") {
		t.Fatalf("got %v, want a write config error", err)
	}
	if _, ok := f.eng.st.Saved[core.IDMute]; !ok {
		t.Error("engine change was rolled back; the real state should win")
	}
	got, _ := f.s.Ticks()
	if !got[core.IDMute] {
		t.Errorf("tick should show the applied state: %v", got)
	}
}

func TestSetTickNotActiveAnymoreFallsBackToConfigOnly(t *testing.T) {
	f := newSettingsFixture(t)
	f.writeConfig(t, `{"switches": ["icons"]}`)
	f.activate()
	f.eng.ended = true // the mode ended between Status and the engine call
	if err := f.s.SetTick(context.Background(), core.IDMute, true); err != nil {
		t.Fatal(err)
	}
	if want := []string{"icons", "mute"}; !slices.Equal(f.config(t).Switches, want) {
		t.Errorf("config switches %v, want %v", f.config(t).Switches, want)
	}
	if err := f.s.SetTick(context.Background(), core.IDIcons, false); err != nil {
		t.Fatal(err)
	}
	if want := []string{"mute"}; !slices.Equal(f.config(t).Switches, want) {
		t.Errorf("config switches %v, want %v", f.config(t).Switches, want)
	}
}

func TestSetTickWallpaperWithoutImageOpensChooser(t *testing.T) {
	f := newSettingsFixture(t)
	f.chooser.Err = system.ErrCancelled
	if err := f.s.SetTick(context.Background(), core.IDWallpaper, true); err != nil {
		t.Fatalf("cancel: got %v, want nil", err)
	}
	if len(f.chooser.Calls) != 1 {
		t.Fatalf("chooser calls %v, want one", f.chooser.Calls)
	}
	if f.readConfigText(t) != "" {
		t.Error("cancel wrote the config")
	}
	img := f.image(t, "pres.jpg")
	f.chooser.Err, f.chooser.Path = nil, img
	if err := f.s.SetTick(context.Background(), core.IDWallpaper, true); err != nil {
		t.Fatal(err)
	}
	cfg := f.config(t)
	if cfg.Wallpaper != img || !cfg.Has(core.IDWallpaper) {
		t.Errorf("config %+v, want wallpaper %q ticked", cfg, img)
	}
}

func TestSetTickWallpaperStaleImage(t *testing.T) {
	f := newSettingsFixture(t)
	missing := filepath.Join(f.dir, "gone.png")
	for _, path := range []string{missing, f.dir} {
		raw := `{"switches": ["icons"], "wallpaper": "` + path + `"}`
		f.writeConfig(t, raw)
		for _, active := range []bool{false, true} {
			f.eng.calls = nil
			f.eng.st = core.State{}
			if active {
				f.activate("icons")
			}
			err := f.s.SetTick(context.Background(), core.IDWallpaper, true)
			if err == nil || err.Error() != "wallpaper: image not found: "+path {
				t.Errorf("%s active=%v: got %v, want the stale-image error", path, active, err)
			}
			if len(f.eng.calls) != 0 || len(f.chooser.Calls) != 0 {
				t.Errorf("%s active=%v: engine %v chooser %v, want no calls", path, active, f.eng.calls, f.chooser.Calls)
			}
			if got := f.readConfigText(t); got != raw {
				t.Errorf("%s active=%v: config changed to %q", path, active, got)
			}
		}
	}
}

func TestChooseWallpaperCancelled(t *testing.T) {
	f := newSettingsFixture(t)
	f.activate("wallpaper")
	f.chooser.Err = system.ErrCancelled
	if err := f.s.ChooseWallpaper(context.Background()); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
	if want := []string{"/home/u/Pictures"}; !slices.Equal(f.chooser.Calls, want) {
		t.Errorf("chooser start dirs %v, want %v", f.chooser.Calls, want)
	}
	if len(f.eng.calls) != 0 || f.readConfigText(t) != "" || len(f.notes) != 0 {
		t.Errorf("cancel changed something: engine %v notes %v", f.eng.calls, f.notes)
	}
}

func TestChooseWallpaperNoZenityNotifies(t *testing.T) {
	f := newSettingsFixture(t)
	f.chooser.Err = errors.Join(system.ErrNoZenity, errors.New("exec: not found"))
	if err := f.s.ChooseWallpaper(context.Background()); err != nil {
		t.Fatalf("got %v, want nil", err)
	}
	want := []notice{{"Choose wallpaper", `Prezilla needs zenity to browse for an image. Install zenity, or set "wallpaper" in config.json.`}}
	if !slices.Equal(f.notes, want) {
		t.Errorf("notes %q, want %q", f.notes, want)
	}
	if len(f.eng.calls) != 0 || f.readConfigText(t) != "" {
		t.Errorf("no zenity changed something: engine %v", f.eng.calls)
	}
	// Ticking Change wallpaper without an image shows the same message.
	f.notes = nil
	if err := f.s.SetTick(context.Background(), core.IDWallpaper, true); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(f.notes, want) || f.readConfigText(t) != "" {
		t.Errorf("tick: notes %q, config %q", f.notes, f.readConfigText(t))
	}
}

func TestChooseWallpaperRejectsNonImage(t *testing.T) {
	f := newSettingsFixture(t)
	f.activate("wallpaper")
	f.chooser.Path = f.image(t, "notes.txt")
	err := f.s.ChooseWallpaper(context.Background())
	if !errors.Is(err, system.ErrNotImage) {
		t.Fatalf("got %v, want ErrNotImage", err)
	}
	if len(f.eng.calls) != 0 || f.readConfigText(t) != "" {
		t.Errorf("rejected choice changed something: engine %v", f.eng.calls)
	}
	f.chooser.Path = filepath.Join(f.dir, "gone.png")
	if err := f.s.ChooseWallpaper(context.Background()); !errors.Is(err, system.ErrImageMissing) {
		t.Errorf("missing file: got %v, want ErrImageMissing", err)
	}
}

func TestChooseWallpaperAppliesAtOnceWhenActive(t *testing.T) {
	f := newSettingsFixture(t)
	old := f.image(t, "old.png")
	f.writeConfig(t, `{"switches": ["icons", "wallpaper"], "wallpaper": "`+old+`", "extra": 1}`)
	f.activate("icons", "wallpaper")
	img := f.image(t, "new.png")
	f.chooser.Path = img
	if err := f.s.ChooseWallpaper(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"disable wallpaper", "enable wallpaper " + img}; !slices.Equal(f.eng.calls, want) {
		t.Errorf("calls %v, want %v", f.eng.calls, want)
	}
	cfg := f.config(t)
	if cfg.Wallpaper != img || !cfg.Has(core.IDWallpaper) {
		t.Errorf("config %+v, want wallpaper %q ticked", cfg, img)
	}
	if !strings.Contains(f.readConfigText(t), `"extra"`) {
		t.Error("unknown key dropped")
	}
	// Not yet applied (wallpaper unticked while active): applied now too.
	f.eng.calls = nil
	f.activate("icons")
	if err := f.s.ChooseWallpaper(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"disable wallpaper", "enable wallpaper " + img}; !slices.Equal(f.eng.calls, want) {
		t.Errorf("calls %v, want %v", f.eng.calls, want)
	}
	if _, ok := f.eng.st.Saved[core.IDWallpaper]; !ok {
		t.Error("wallpaper not applied")
	}
}

func TestChooseWallpaperInactiveOnlySaves(t *testing.T) {
	f := newSettingsFixture(t)
	f.writeConfig(t, `{"switches": ["icons"]}`)
	img := f.image(t, "new.png")
	f.chooser.Path = img
	if err := f.s.ChooseWallpaper(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, c := range f.eng.calls {
		if strings.HasPrefix(c, "enable") {
			t.Errorf("inactive choice enabled a switch: %v", f.eng.calls)
		}
	}
	cfg := f.config(t)
	if want := []string{"icons", "wallpaper"}; cfg.Wallpaper != img || !slices.Equal(cfg.Switches, want) {
		t.Errorf("config %+v, want wallpaper %q and switches %v", cfg, img, want)
	}
}

func TestChooseWallpaperEngineFailureWritesNothing(t *testing.T) {
	f := newSettingsFixture(t)
	const raw = `{"switches": ["icons"]}`
	f.writeConfig(t, raw)
	f.activate("icons", "wallpaper")
	f.eng.opErr = errors.New("wallpaper: xfconf failed")
	f.chooser.Path = f.image(t, "new.png")
	if err := f.s.ChooseWallpaper(context.Background()); err == nil || !strings.Contains(err.Error(), "xfconf failed") {
		t.Errorf("got %v, want the engine error", err)
	}
	if got := f.readConfigText(t); got != raw {
		t.Errorf("config changed to %q", got)
	}
}

func TestChooseWallpaperEngineCallsBoundedByOperationTimeout(t *testing.T) {
	f := newSettingsFixture(t)
	f.writeConfig(t, `{"switches": ["icons", "wallpaper"]}`)
	f.activate("icons", "wallpaper")
	f.chooser.Path = f.image(t, "new.png")
	long, cancel := context.WithTimeout(context.Background(), time.Hour)
	defer cancel()
	for name, ctx := range map[string]context.Context{
		"no deadline":   context.Background(),
		"long deadline": long,
	} {
		f.eng.deadlines = nil
		start := time.Now()
		if err := f.s.ChooseWallpaper(ctx); err != nil {
			t.Fatal(err)
		}
		if len(f.eng.deadlines) != 2 {
			t.Fatalf("%s: %d engine calls, want 2", name, len(f.eng.deadlines))
		}
		for i, d := range f.eng.deadlines {
			if d.IsZero() || d.After(time.Now().Add(core.OperationTimeout)) || d.Before(start) {
				t.Errorf("%s: call %d deadline %v, want within OperationTimeout of now", name, i, d)
			}
		}
	}
}

// gateChooser is a FileChooser that reports when its dialog is open and
// returns path only once release is closed.
type gateChooser struct {
	path    string
	opened  chan struct{}
	release chan struct{}
}

func (g *gateChooser) ChooseImage(context.Context, string) (string, error) {
	close(g.opened)
	<-g.release
	return g.path, nil
}

func TestSetTickAndChooseWallpaperConcurrentKeepBothChanges(t *testing.T) {
	f := newSettingsFixture(t)
	f.writeConfig(t, `{"switches": ["icons"]}`)
	img := f.image(t, "new.png")
	g := &gateChooser{path: img, opened: make(chan struct{}), release: make(chan struct{})}
	f.s.Chooser = g
	done := make(chan error, 1)
	go func() { done <- f.s.ChooseWallpaper(context.Background()) }()
	<-g.opened
	tickDone := make(chan error, 1)
	go func() { tickDone <- f.s.SetTick(context.Background(), core.IDMute, true) }()
	if err := <-tickDone; err != nil {
		t.Fatal(err)
	}
	close(g.release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	cfg := f.config(t)
	if want := []string{"icons", "mute", "wallpaper"}; cfg.Wallpaper != img || !slices.Equal(cfg.Switches, want) {
		t.Errorf("config %+v, want wallpaper %q and switches %v", cfg, img, want)
	}
}

func TestSetTickNotBlockedWhileChooserOpen(t *testing.T) {
	f := newSettingsFixture(t)
	f.writeConfig(t, `{"switches": ["icons"]}`)
	g := &gateChooser{opened: make(chan struct{}), release: make(chan struct{})}
	f.s.Chooser = g
	// Ticking Change wallpaper without an image opens the dialog via SetTick.
	done := make(chan error, 1)
	go func() { done <- f.s.SetTick(context.Background(), core.IDWallpaper, true) }()
	<-g.opened
	tickDone := make(chan error, 1)
	go func() { tickDone <- f.s.SetTick(context.Background(), core.IDMute, true) }()
	select {
	case err := <-tickDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SetTick blocked while the chooser was open")
	}
	close(g.release) // empty path: rejected, nothing saved for the wallpaper
	<-done
	if cfg := f.config(t); !cfg.Has(core.IDMute) {
		t.Errorf("mute not saved: %+v", cfg)
	}
}

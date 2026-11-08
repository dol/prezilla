package system

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Desktop wallpaper locations, adapted from github.com/reujab/wallpaper
// (Unlicense).
const (
	gnomeBackgroundSchema = "org.gnome.desktop.background"
	cinnamonPictureKey    = "/org/cinnamon/desktop/background/picture-uri"
	matePictureKey        = "/org/mate/desktop/background/picture-filename"
	deepinPictureKey      = "/com/deepin/wrap/gnome/desktop/background/picture-uri"
	kdeAppletsrc          = "plasma-org.kde.plasma.desktop-appletsrc"
	kdeWallpaperSection   = "[Wallpaper][org.kde.image][General]"
)

// DesktopWallpaper reads and sets the wallpaper of the Linux desktops that
// have a command or a config file for it: GNOME (and Unity, Pantheon),
// Cinnamon, MATE, Deepin, KDE Plasma and LXDE. XFCE has its own switch under
// internal/platform. Any other desktop yields ErrUnsupportedDesktop. The
// zero value is not valid: set Runner.
type DesktopWallpaper struct {
	Runner Runner
	// Getenv and ReadFile default to os.Getenv and os.ReadFile.
	Getenv   func(string) string
	ReadFile func(string) ([]byte, error)
	// EvalPlasma runs a script in Plasma's shell; it defaults to
	// plasmaEvaluate over the session bus.
	EvalPlasma func(ctx context.Context, script string) error
}

func (d DesktopWallpaper) getenv(k string) string {
	if d.Getenv != nil {
		return d.Getenv(k)
	}
	return os.Getenv(k)
}

func (d DesktopWallpaper) readFile(p string) ([]byte, error) {
	if d.ReadFile != nil {
		return d.ReadFile(p)
	}
	return os.ReadFile(p)
}

// configDir is $XDG_CONFIG_HOME, or ~/.config.
func (d DesktopWallpaper) configDir() (string, error) {
	if dir := d.getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir, nil
	}
	home := d.getenv("HOME")
	if home == "" {
		return "", errors.New("wallpaper: neither XDG_CONFIG_HOME nor HOME is set")
	}
	return filepath.Join(home, ".config"), nil
}

// currentDesktop names the desktop among the colon-separated
// XDG_CURRENT_DESKTOP entries ("ubuntu:GNOME"), or "" when none is known.
func (d DesktopWallpaper) currentDesktop() string {
	for _, e := range strings.Split(d.getenv("XDG_CURRENT_DESKTOP"), ":") {
		e = strings.ToLower(strings.TrimSpace(e))
		switch {
		case strings.Contains(e, "gnome"), e == "unity", e == "pantheon":
			return "gnome"
		case e == "kde", e == "x-cinnamon", e == "mate", e == "lxde", e == "deepin":
			return e
		}
	}
	return ""
}

// Get implements Wallpaper.
func (d DesktopWallpaper) Get(ctx context.Context) (string, error) {
	switch desktop := d.currentDesktop(); desktop {
	case "gnome":
		key, err := d.gnomeKey(ctx)
		if err != nil {
			return "", err
		}
		out, err := d.Runner.Run(ctx, "gsettings", "get", gnomeBackgroundSchema, key)
		return pathFromURI(gvariantUnquote(out)), err
	case "x-cinnamon":
		return d.dconfRead(ctx, cinnamonPictureKey)
	case "mate":
		return d.dconfRead(ctx, matePictureKey)
	case "deepin":
		return d.dconfRead(ctx, deepinPictureKey)
	case "kde":
		return d.kdeGet()
	case "lxde":
		return d.lxdeGet()
	}
	return "", ErrUnsupportedDesktop
}

// Set implements Wallpaper.
func (d DesktopWallpaper) Set(ctx context.Context, path string) error {
	var err error
	switch desktop := d.currentDesktop(); desktop {
	case "gnome":
		var key string
		if key, err = d.gnomeKey(ctx); err == nil {
			_, err = d.Runner.Run(ctx, "gsettings", "set", gnomeBackgroundSchema, key, gvariantQuote(fileURI(path)))
		}
	case "x-cinnamon":
		_, err = d.Runner.Run(ctx, "dconf", "write", cinnamonPictureKey, gvariantQuote(fileURI(path)))
	case "mate":
		_, err = d.Runner.Run(ctx, "dconf", "write", matePictureKey, gvariantQuote(path))
	case "deepin":
		_, err = d.Runner.Run(ctx, "dconf", "write", deepinPictureKey, gvariantQuote(fileURI(path)))
	case "kde":
		err = d.kdeSet(ctx, path)
	case "lxde":
		_, err = d.Runner.Run(ctx, "pcmanfm", "-w", path)
	default:
		return ErrUnsupportedDesktop
	}
	if err != nil {
		return fmt.Errorf("set wallpaper: %w", err)
	}
	return nil
}

// gnomeKey is the picture key in effect: GNOME shows picture-uri-dark when
// the color scheme prefers dark. An unreadable scheme counts as light.
func (d DesktopWallpaper) gnomeKey(ctx context.Context) (string, error) {
	out, err := d.Runner.Run(ctx, "gsettings", "get", "org.gnome.desktop.interface", "color-scheme")
	if err == nil && gvariantUnquote(out) == "prefer-dark" {
		return "picture-uri-dark", nil
	}
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	return "picture-uri", nil
}

func (d DesktopWallpaper) dconfRead(ctx context.Context, key string) (string, error) {
	out, err := d.Runner.Run(ctx, "dconf", "read", key)
	if err != nil {
		return "", err
	}
	return pathFromURI(gvariantUnquote(out)), nil
}

// kdeGet reads the Image of the first org.kde.image wallpaper in Plasma's
// applet config.
func (d DesktopWallpaper) kdeGet() (string, error) {
	dir, err := d.configDir()
	if err != nil {
		return "", err
	}
	data, err := d.readFile(filepath.Join(dir, kdeAppletsrc))
	if err != nil {
		return "", err
	}
	inSection := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inSection = strings.HasSuffix(line, kdeWallpaperSection)
			continue
		}
		if v, ok := strings.CutPrefix(line, "Image="); ok && inSection {
			return pathFromURI(strings.TrimSpace(v)), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", errors.New("wallpaper: KDE image not found in " + kdeAppletsrc)
}

// kdeSet sets the image on every desktop through Plasma's scripting API.
func (d DesktopWallpaper) kdeSet(ctx context.Context, path string) error {
	uri, err := json.Marshal(fileURI(path))
	if err != nil {
		return err
	}
	script := `for (const desktop of desktops()) {
	desktop.currentConfigGroup = ["Wallpaper", "org.kde.image", "General"]
	desktop.writeConfig("Image", ` + string(uri) + `)
}`
	eval := d.EvalPlasma
	if eval == nil {
		eval = plasmaEvaluate
	}
	return eval(ctx, script)
}

// plasmaTimeout bounds connecting to the session bus and the script call.
const plasmaTimeout = 10 * time.Second

// plasmaEvaluate calls org.kde.PlasmaShell.evaluateScript on the session bus.
func plasmaEvaluate(ctx context.Context, script string) error {
	conn, err := connectSession(ctx, plasmaTimeout, "wallpaper: plasma")
	if err != nil {
		return err
	}
	defer conn.Close()
	cctx, cancel := context.WithTimeout(ctx, plasmaTimeout)
	defer cancel()
	call := conn.Object("org.kde.plasmashell", "/PlasmaShell").
		CallWithContext(cctx, "org.kde.PlasmaShell.evaluateScript", 0, script)
	if call.Err != nil {
		return fmt.Errorf("wallpaper: plasma evaluateScript: %w", call.Err)
	}
	return nil
}

// lxdeGet reads the wallpaper key of the [*] section of pcmanfm's desktop
// config.
func (d DesktopWallpaper) lxdeGet() (string, error) {
	dir, err := d.configDir()
	if err != nil {
		return "", err
	}
	session := d.getenv("DESKTOP_SESSION")
	if session == "" {
		session = "LXDE"
	}
	data, err := d.readFile(filepath.Join(dir, "pcmanfm", session, "desktop-items-0.conf"))
	if err != nil {
		return "", err
	}
	inSection := false
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if strings.HasPrefix(line, "[") {
			inSection = line == "[*]"
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok && inSection && strings.TrimSpace(k) == "wallpaper" {
			return strings.TrimSpace(v), nil
		}
	}
	if err := sc.Err(); err != nil {
		return "", err
	}
	return "", errors.New("wallpaper: LXDE wallpaper not found")
}

// gvariantQuote renders s as a single-quoted GVariant string, the form
// gsettings and dconf take.
func gvariantQuote(s string) string {
	return "'" + strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(s) + "'"
}

// gvariantUnquote undoes gvariantQuote on the output of gsettings or dconf;
// text that is not quoted is returned trimmed.
func gvariantUnquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && (s[0] == '\'' || s[0] == '"') && s[len(s)-1] == s[0] {
		return strings.NewReplacer(`\\`, `\`, `\'`, `'`, `\"`, `"`).Replace(s[1 : len(s)-1])
	}
	return s
}

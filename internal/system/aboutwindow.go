package system

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"prezilla/internal/about"
)

// AboutWindow shows the program's About information.
type AboutWindow interface {
	Show(ctx context.Context, info about.Info) error
}

// ErrNoZenity means zenity is not installed (or no runner is configured);
// callers fall back to the About notification.
var ErrNoZenity = errors.New("about: zenity not found")

// ZenityAbout shows the About window with zenity (an optional runtime tool):
// the text with the project link and one Close button. Clicking the link is
// left to the toolkit's own link handling (the default browser). The window
// blocks until it is closed, so Runner needs a long timeout; cancelling ctx
// closes it.
type ZenityAbout struct {
	Runner Runner
	// IconFile is an absolute path to the logo. zenity 4 otherwise looks the
	// icon up by name in the icon theme, which has no entry until the
	// application is installed. Empty means use the name.
	IconFile string
}

// zenityStartFailure reports whether an exit-1 error means zenity never
// showed a window: GTK says on stderr that it cannot open the display.
func zenityStartFailure(err error) bool {
	text := err.Error()
	return strings.Contains(text, "cannot open display") ||
		strings.Contains(text, "Failed to open display")
}

// errZenityStart marks zenity failing to show its window.
var errZenityStart = errors.New("zenity could not start its window")

// zenity --info exit codes besides 0 that mean "closed, nothing to do": 1
// (Esc or the title-bar X on zenity 4.2.1 under Xvfb; Enter exits 0), 5
// (--timeout) and 255 (title-bar X or Esc on other zenity versions). Exit 1 is
// also what GTK reports when it cannot start (dead display); see
// zenityStartFailure.
var zenityClosed = map[int]bool{1: true, 5: true, 255: true}

// Show implements AboutWindow. It returns nil once the window was closed;
// ErrNoZenity when zenity is missing; ctx's error when ctx ended; any other
// error is a real zenity failure.
func (z ZenityAbout) Show(ctx context.Context, info about.Info) error {
	if z.Runner == nil {
		return fmt.Errorf("%w: no runner configured", ErrNoZenity)
	}
	ver, err := z.Runner.Run(ctx, "zenity", "--version")
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, exec.ErrNotFound) {
			return fmt.Errorf("%w: %w", ErrNoZenity, err)
		}
		return fmt.Errorf("about: zenity --version: %w", err)
	}
	_, err = z.Runner.Run(ctx, "zenity", zenityArgs(zenityMajor(ver), info, z.IconFile)...)
	if err != nil {
		var ec interface{ ExitCode() int }
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.As(err, &ec) && zenityClosed[ec.ExitCode()]:
			if ec.ExitCode() == 1 && zenityStartFailure(err) {
				return fmt.Errorf("about: %w: %w", errZenityStart, err)
			}
			return nil
		case errors.Is(err, context.DeadlineExceeded):
			// The runner's own timeout killed a window nobody answered.
			return nil
		}
		return fmt.Errorf("about: %w", err)
	}
	return nil
}

// FallbackAbout shows Primary and, when that cannot show a window (zenity
// missing or unable to start), Secondary. Anything else Primary returns,
// including a user closing the window and ctx errors, is final. If Secondary
// fails too its error is returned, so the caller can fall back to a
// notification.
type FallbackAbout struct {
	Primary, Secondary AboutWindow
	// Note, when set, is called once (not per click) with Primary's error
	// the first time Secondary is used.
	Note func(primary error)

	once sync.Once
}

// noWindow reports whether err means the window never appeared: zenity is
// missing, or it could not start (see zenityStartFailure).
func noWindow(err error) bool {
	return errors.Is(err, ErrNoZenity) || errors.Is(err, errZenityStart)
}

// Show implements AboutWindow.
func (f *FallbackAbout) Show(ctx context.Context, info about.Info) error {
	err := f.Primary.Show(ctx, info)
	if err == nil || ctx.Err() != nil || !noWindow(err) {
		return err
	}
	if f.Note != nil {
		f.once.Do(func() { f.Note(err) })
	}
	return f.Secondary.Show(ctx, info)
}

// zenityMajor parses the major version from `zenity --version` output such
// as "4.2.1"; anything unparsable counts as 4.
func zenityMajor(out string) int {
	fields := strings.Fields(out)
	if len(fields) == 0 {
		return 4
	}
	major, _, _ := strings.Cut(fields[0], ".")
	n, err := strconv.Atoi(major)
	if err != nil {
		return 4
	}
	return n
}

// zenityArgs builds the argv (after "zenity") for the About window.
// zenity 3 names the icon with --icon-name, later versions with --icon.
func zenityArgs(major int, info about.Info, iconFile string) []string {
	icon := "--icon=" + info.IconName
	switch {
	case major == 3:
		icon = "--icon-name=" + info.IconName
	case iconFile != "":
		icon = "--icon=" + iconFile
	}
	return []string{"--info", "--title=About " + info.Name, "--width=380",
		"--ok-label=Close", "--modal", icon,
		"--text=" + aboutMarkup(info)}
}

// markupEscaper escapes in one pass, so the entities it writes are never
// escaped again. zenity runs --text through g_strcompress after this, which
// would decode \n, \t or \074 (<) and undo the Pango escaping; doubling the
// backslash makes g_strcompress turn it back into one literal backslash.
var markupEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;",
	`\`, `\\`)

// escapeMarkup escapes s for Pango markup (text and attribute values) as
// zenity's --text needs it: g_strcompress first, then Pango.
func escapeMarkup(s string) string { return markupEscaper.Replace(s) }

// aboutMarkup renders the window text. Every value is escaped; the website
// becomes the clickable project link only for http(s) URLs, otherwise its
// label is plain text, so an About value can never inject a link scheme.
func aboutMarkup(info about.Info) string {
	label := info.WebsiteLabel
	if label == "" {
		label = info.Website
	}
	link := escapeMarkup(label)
	if strings.HasPrefix(info.Website, "http://") || strings.HasPrefix(info.Website, "https://") {
		link = `<a href="` + escapeMarkup(info.Website) + `">` + link + "</a>"
	}
	return "<b>" + escapeMarkup(info.Name) + "</b> " + escapeMarkup(info.Version) + "\n" +
		escapeMarkup(info.Comments) + "\n\nLicense: " + escapeMarkup(info.License) + "\n" + link
}

// WriteIconFile writes data to dir/name unless that file already has exactly
// this content, and returns its path. The directory is created if needed.
func WriteIconFile(dir, name string, data []byte) (string, error) {
	path := filepath.Join(dir, name)
	if old, err := os.ReadFile(path); err == nil && bytes.Equal(old, data) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, ".icon-*.tmp")
	if err != nil {
		return "", err
	}
	_, werr := tmp.Write(data)
	cerr := tmp.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return path, nil
}

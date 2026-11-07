package system

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"prezilla/internal/about"
)

// AboutWindow shows the program's About information.
type AboutWindow interface {
	Show(ctx context.Context, info about.Info) error
}

// ErrNoZenity means zenity is not installed (or no runner is configured);
// callers fall back to the About notification.
var ErrNoZenity = errors.New("about: zenity not found")

// WebsiteError reports that the user chose "Open website" but the opener
// failed. The window itself worked, so callers show the error instead of
// falling back to the notification. The message names the URL, so the user
// can still copy the address when no opener works.
type WebsiteError struct {
	URL string
	Err error
}

func (e *WebsiteError) Error() string {
	return "about: could not open " + e.URL + ": " + e.Err.Error()
}
func (e *WebsiteError) Unwrap() error { return e.Err }

// ZenityAbout shows the About window with zenity (an optional runtime tool).
// The window blocks until the user answers, so Runner needs a long timeout;
// cancelling ctx closes it.
type ZenityAbout struct {
	Runner Runner
	Opener Opener
}

// zenity --question exit codes that mean "closed, nothing to do": Cancel
// (the Close button), --timeout, and the title-bar X or Esc.
var zenityClosed = map[int]bool{1: true, 5: true, 255: true}

// Show implements AboutWindow. It returns nil when the window was closed or
// the website opened; ErrNoZenity when zenity is missing; a *WebsiteError
// when the opener failed; ctx's error when ctx ended; any other error is a
// real zenity failure.
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
	_, err = z.Runner.Run(ctx, "zenity", zenityArgs(zenityMajor(ver), info)...)
	if err != nil {
		var ec interface{ ExitCode() int }
		switch {
		case ctx.Err() != nil:
			return ctx.Err()
		case errors.As(err, &ec) && zenityClosed[ec.ExitCode()]:
			return nil
		case errors.Is(err, context.DeadlineExceeded):
			// The runner's own timeout killed a window nobody answered.
			return nil
		}
		return fmt.Errorf("about: %w", err)
	}
	if z.Opener == nil {
		return &WebsiteError{URL: info.Website, Err: errors.New("no opener configured")}
	}
	if err := z.Opener.Open(ctx, info.Website); err != nil {
		return &WebsiteError{URL: info.Website, Err: err}
	}
	return nil
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
func zenityArgs(major int, info about.Info) []string {
	icon := "--icon=" + info.IconName
	if major == 3 {
		icon = "--icon-name=" + info.IconName
	}
	return []string{"--question", "--title=About " + info.Name, "--width=380",
		"--ok-label=Open website", "--cancel-label=Close", "--modal", icon,
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
// becomes a link only for http(s) URLs, otherwise its label is plain text.
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

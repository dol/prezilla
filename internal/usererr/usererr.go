// Package usererr turns the technical errors of Prezilla into plain sentences
// that say what went wrong and what to do next.
//
// Explain is for stderr: the sentence followed by the original technical text
// in parentheses. Short is for tooltips: the sentence only. An error without a
// matching rule passes through unchanged, so nothing is ever lost or invented.
package usererr

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/godbus/dbus/v5"

	"prezilla/internal/core"
	"prezilla/internal/system"
)

// rule is one row of the matcher table. match inspects one (non-joined)
// error and returns the plain sentence when the row applies.
type rule struct {
	name  string
	match func(err error, text string) (string, bool)
}

// switch ids as they prefix engine errors ("<id>: ...").
const (
	idIcons         = "icons"
	idNotifications = "notifications"
	idWallpaper     = "wallpaper"
	idMute          = "mute"
	idMinimize      = "minimize"
	idPresentation  = "presentation"
)

// nouns names what a switch changes, for the sentences.
var nouns = map[string]string{
	idIcons:         "desktop icons",
	idNotifications: "notifications",
	idWallpaper:     "the wallpaper",
	idMute:          "the sound",
	idMinimize:      "the desktop",
	idPresentation:  "the power manager's presentation mode",
}

var (
	saveStateRE       = regexp.MustCompile(`^save state: write state (.+?): `)
	parseStateRE      = regexp.MustCompile(`(?:^|: )parse state (.+?): `)
	parseConfigRE     = regexp.MustCompile(`(?:^|: )parse config (.+?): `)
	saveConfigRE      = regexp.MustCompile(`(?:^|: )(?:save|write) config (.+?): `)
	createConfigDirRE = regexp.MustCompile(`(?:^|: )create config dir (.+?): `)
	unsupportedRE     = regexp.MustCompile(`(?:^|: )unsupported desktop "([^"]*)"`)
)

const saveConfigSentence = "Couldn't save your choice to %s. Check that the folder is writable, then try again."

// chosenImagePath returns the path a system.ValidateImage error names: the
// stat error's own path, else the text after the sentinel's "<sentinel>: "
// (minus " is not a regular file").
func chosenImagePath(err error, text string, sentinel error) string {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Path
	}
	prefix := sentinel.Error() + ": "
	i := strings.Index(text, prefix)
	if i < 0 {
		return ""
	}
	return strings.TrimSuffix(text[i+len(prefix):], " is not a regular file")
}

// hasSwitchPrefix reports whether text starts with "<id>: ".
func hasSwitchPrefix(text, id string) bool { return strings.HasPrefix(text, id+": ") }

func hasAnyPrefix(text string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(text, p) {
			return true
		}
	}
	return false
}

// xfconfSwitch returns the id of the Xfconf-backed switch the error text
// starts with, when the text mentions the settings service at all.
func xfconfSwitch(text string) (string, bool) {
	if !strings.Contains(text, "xfconf: ") {
		return "", false
	}
	for _, id := range []string{idIcons, idNotifications, idWallpaper, idPresentation} {
		if hasSwitchPrefix(text, id) {
			return id, true
		}
	}
	return "", false
}

// xfconfRestoring reports whether the Xfconf switch id failed while restoring
// (its restore errors read "<id>: restore ...").
func xfconfRestoring(text, id string) bool {
	return strings.HasPrefix(text, id+": restore ")
}

func xfconfTimedOut(text string) bool {
	return strings.Contains(text, "xfconf: ") && strings.Contains(text, "timed out after")
}

// xfconfUnreachable reports a failure to connect to the session bus or to
// reach (or start) xfconfd on it.
func xfconfUnreachable(err error, text string) bool {
	if strings.Contains(text, "xfconf: connect:") {
		return true
	}
	var de dbus.Error
	if errors.As(err, &de) {
		return de.Name == "org.freedesktop.DBus.Error.ServiceUnknown" || de.Name == "org.freedesktop.DBus.Error.NameHasNoOwner"
	}
	return false
}

// rules is the single matcher table: one row per case. Only the listed
// prefix/cause combinations are rewritten; everything else is left alone.
var rules = []rule{
	{"minimize restore: no X display", func(_ error, text string) (string, bool) {
		if hasSwitchPrefix(text, idMinimize) && strings.Contains(text, "restore desktop: ") && strings.Contains(text, "x11: connect") {
			return `Couldn't bring your windows back: no X display is reachable (Prezilla needs an X11 session). Run "prezilla off" again in an X11 session.`, true
		}
		return "", false
	}},
	{"mute restore: no pulse server", func(_ error, text string) (string, bool) {
		if hasSwitchPrefix(text, idMute) && strings.Contains(text, "unmute sink: ") && strings.Contains(text, "pulse: connect") {
			return `Couldn't restore the sound: no PulseAudio or PipeWire server is running. Start your sound server, then run "prezilla off" again.`, true
		}
		return "", false
	}},
	{"mute enable: server timed out", func(_ error, text string) (string, bool) {
		if hasAnyPrefix(text, "mute: read sink mute: ", "mute: mute sink: ") && strings.Contains(text, "pulse: timed out") {
			return `Couldn't change the sound: the sound server didn't answer in time. Run "prezilla off", then try again.`, true
		}
		return "", false
	}},
	{"mute restore: server timed out", func(_ error, text string) (string, bool) {
		if strings.HasPrefix(text, "mute: unmute sink: ") && strings.Contains(text, "pulse: timed out") {
			return `Couldn't restore the sound: the sound server didn't answer in time. Run "prezilla off" again.`, true
		}
		return "", false
	}},
	{"minimize enable: X server timed out", func(_ error, text string) (string, bool) {
		if hasAnyPrefix(text, "minimize: read showing desktop: ", "minimize: show desktop: ") && strings.Contains(text, "x11: timed out") {
			return `Couldn't show the desktop: the X server didn't answer in time. Run "prezilla off", then try again.`, true
		}
		return "", false
	}},
	{"minimize restore: X server timed out", func(_ error, text string) (string, bool) {
		if strings.HasPrefix(text, "minimize: restore desktop: ") && strings.Contains(text, "x11: timed out") {
			return `Couldn't bring your windows back: the X server didn't answer in time. Run "prezilla off" again.`, true
		}
		return "", false
	}},
	{"state cannot be saved", func(_ error, text string) (string, bool) {
		if m := saveStateRE.FindStringSubmatch(text); m != nil {
			return fmt.Sprintf(`Couldn't save Prezilla's state (%s). Check free space and permissions, then try again.`, m[1]), true
		}
		return "", false
	}},
	{"timer watcher not started", func(_ error, text string) (string, bool) {
		if strings.HasPrefix(text, "start watcher: ") {
			return `Couldn't start the background timer, so presentation mode might not end by itself. Run "prezilla off" when you are done.`, true
		}
		return "", false
	}},
	{"notification server has no actions", func(_ error, text string) (string, bool) {
		if strings.HasPrefix(text, "notify: server does not support actions") {
			return `Your notification server can't show buttons. Use "Keep presenting" or "Stop presenting" in the tray menu.`, true
		}
		return "", false
	}},
	{"mute: no pulse server", func(_ error, text string) (string, bool) {
		if hasSwitchPrefix(text, idMute) && (strings.HasPrefix(text, "mute: read sink mute: ") || strings.HasPrefix(text, "mute: mute sink: ")) &&
			strings.Contains(text, "pulse: connect") {
			return `Couldn't change the sound: no PulseAudio or PipeWire server is running. Start your sound server, or remove "mute" from "switches" in config.json.`, true
		}
		return "", false
	}},
	{"minimize: no X display", func(_ error, text string) (string, bool) {
		if hasSwitchPrefix(text, idMinimize) && (strings.HasPrefix(text, "minimize: read showing desktop: ") || strings.HasPrefix(text, "minimize: show desktop: ")) &&
			strings.Contains(text, "x11: connect") {
			return `Couldn't show the desktop: no X display is reachable (Prezilla needs an X11 session). Log in to an X11 session, or remove "minimize" from "switches" in config.json.`, true
		}
		return "", false
	}},
	{"xfconf restore: settings service timed out", func(_ error, text string) (string, bool) {
		if id, ok := xfconfSwitch(text); ok && xfconfRestoring(text, id) && xfconfTimedOut(text) {
			return fmt.Sprintf(`Couldn't restore %s: XFCE's settings service (xfconfd) didn't answer in time. Run "prezilla off" again.`, nouns[id]), true
		}
		return "", false
	}},
	{"xfconf enable: settings service timed out", func(_ error, text string) (string, bool) {
		if id, ok := xfconfSwitch(text); ok && xfconfTimedOut(text) {
			return fmt.Sprintf(`Couldn't change %s: XFCE's settings service (xfconfd) didn't answer in time. Run "prezilla off", then try again.`, nouns[id]), true
		}
		return "", false
	}},
	{"xfconf restore: settings service unreachable", func(err error, text string) (string, bool) {
		if id, ok := xfconfSwitch(text); ok && xfconfRestoring(text, id) && xfconfUnreachable(err, text) {
			return fmt.Sprintf(`Couldn't restore %s: XFCE's settings service (xfconfd) isn't reachable. Log in to your XFCE session again, then run "prezilla off" again.`, nouns[id]), true
		}
		return "", false
	}},
	{"xfconf enable: settings service unreachable", func(err error, text string) (string, bool) {
		if id, ok := xfconfSwitch(text); ok && xfconfUnreachable(err, text) {
			return fmt.Sprintf(`Couldn't change %s: XFCE's settings service (xfconfd) isn't reachable. Run Prezilla inside an XFCE session, or remove "%s" from "switches" in config.json.`, nouns[id], id), true
		}
		return "", false
	}},
	{"presentation: unexpected value", func(_ error, text string) (string, bool) {
		if strings.HasPrefix(text, "presentation: read presentation mode: unexpected value") {
			return `Couldn't change the power manager's presentation mode: its setting isn't a yes/no value. Reset it in the power manager's settings, or remove "presentation" from "switches" in config.json.`, true
		}
		return "", false
	}},
	{"wallpaper: no image", func(_ error, text string) (string, bool) {
		if hasSwitchPrefix(text, idWallpaper) && strings.Contains(text, "no image configured") {
			return `Couldn't change the wallpaper: no image is set. Set "wallpaper" to an image path in config.json, or remove "wallpaper" from "switches".`, true
		}
		return "", false
	}},
	{"wallpaper: image gone", func(_ error, text string) (string, bool) {
		if i := strings.Index(text, "wallpaper: image not found: "); i >= 0 && (i == 0 || strings.HasSuffix(text[:i], ": ")) {
			path := strings.TrimSpace(text[i+len("wallpaper: image not found: "):])
			return fmt.Sprintf(`Couldn't change the wallpaper: the image %s no longer exists. Choose another image from the tray menu, or fix "wallpaper" in config.json.`, path), true
		}
		return "", false
	}},
	{"chosen file is not an image", func(err error, text string) (string, bool) {
		if errors.Is(err, system.ErrNotImage) {
			return fmt.Sprintf("Couldn't use %s as the wallpaper: it isn't an image file (png, jpg, jpeg, webp, bmp, gif, tif, tiff or svg). Choose another image.",
				chosenImagePath(err, text, system.ErrNotImage)), true
		}
		return "", false
	}},
	{"chosen file missing", func(err error, text string) (string, bool) {
		if errors.Is(err, system.ErrImageMissing) {
			return fmt.Sprintf("Couldn't use %s as the wallpaper: the file doesn't exist. Choose another image.",
				chosenImagePath(err, text, system.ErrImageMissing)), true
		}
		return "", false
	}},
	{"file chooser could not start", func(_ error, text string) (string, bool) {
		// The sentinel is unexported in system, so match its text.
		if strings.Contains(text, "file chooser: zenity could not start its window") {
			return `Couldn't open the file chooser: zenity could not start its window. Check that your desktop session is running, or set "wallpaper" in config.json.`, true
		}
		return "", false
	}},
	{"file chooser portal failed", func(err error, _ string) (string, bool) {
		if errors.Is(err, system.ErrPortalFailed) {
			return `Couldn't open the file chooser: the desktop's file chooser portal failed. Set "wallpaper" in config.json instead.`, true
		}
		return "", false
	}},
	{"window gone", func(err error, _ string) (string, bool) {
		if errors.Is(err, system.ErrNoWindow) {
			return "That window is gone. Pick another one from the list.", true
		}
		return "", false
	}},
	{"no switch ticked", func(err error, _ string) (string, bool) {
		if errors.Is(err, core.ErrNoSwitches) {
			return `No switch is ticked, so presentation mode would change nothing. Tick at least one switch in the tray menu, or add one to "switches" in config.json.`, true
		}
		return "", false
	}},
	{"config cannot be saved", func(_ error, text string) (string, bool) {
		if m := createConfigDirRE.FindStringSubmatch(text); m != nil {
			return fmt.Sprintf(saveConfigSentence, filepath.Join(m[1], "config.json")), true
		}
		if m := saveConfigRE.FindStringSubmatch(text); m != nil {
			return fmt.Sprintf(saveConfigSentence, m[1]), true
		}
		return "", false
	}},
	{"lock timed out", func(_ error, text string) (string, bool) {
		if strings.HasPrefix(text, "lock ") && strings.Contains(text, "timed out after") &&
			strings.Contains(text, "waiting for another Prezilla process") {
			return "Another Prezilla action is still running. Wait a moment and try again.", true
		}
		return "", false
	}},
	{"state unreadable", func(_ error, text string) (string, bool) {
		if m := parseStateRE.FindStringSubmatch(text); m != nil {
			return fmt.Sprintf("Prezilla's saved state (%s) is unreadable, so it can't restore your settings automatically. "+
				"Turn desktop icons, notifications, sound and windows back on and set your wallpaper again by hand (the old wallpaper path may still be readable in that file), then delete that file.", m[1]), true
		}
		return "", false
	}},
	{"config not valid JSON", func(_ error, text string) (string, bool) {
		if m := parseConfigRE.FindStringSubmatch(text); m != nil {
			return fmt.Sprintf("The configuration file (%s) isn't valid JSON. Fix it, or delete it to use the defaults.", m[1]), true
		}
		return "", false
	}},
	{"unsupported desktop", func(_ error, text string) (string, bool) {
		if m := unsupportedRE.FindStringSubmatch(text); m != nil {
			if m[1] == "" {
				return "Prezilla supports XFCE only (this session reports no desktop). Run it inside an XFCE session.", true
			}
			return fmt.Sprintf(`Prezilla supports XFCE only (this session reports "%s"). Run it inside an XFCE session.`, m[1]), true
		}
		return "", false
	}},
}

// leaves flattens errors.Join trees into the individual errors, in order.
// Only a join is split (its text is its parts' texts, one per line); an
// fmt.Errorf with several %w is one error with its own text and stays whole.
func leaves(err error) []error {
	j, ok := err.(interface{ Unwrap() []error })
	if !ok {
		return []error{err}
	}
	var parts []error
	var texts []string
	for _, e := range j.Unwrap() {
		if e != nil {
			parts = append(parts, e)
			texts = append(texts, e.Error())
		}
	}
	if err.Error() != strings.Join(texts, "\n") {
		return []error{err}
	}
	var out []error
	for _, e := range parts {
		out = append(out, leaves(e)...)
	}
	return out
}

// sentence returns the plain sentence for one error, if a rule matches.
func sentenceFor(err error) (string, bool) {
	text := err.Error()
	for _, r := range rules {
		if s, ok := r.match(err, text); ok {
			return s, true
		}
	}
	return "", false
}

// Explain describes err for stderr: the plain sentence with a next step,
// followed by " (details: <technical text>)". Several joined errors give one
// line each; an error without a rule keeps its original text. nil gives "".
func Explain(err error) string {
	if err == nil {
		return ""
	}
	var lines []string
	for _, e := range leaves(err) {
		if s, ok := sentenceFor(e); ok {
			lines = append(lines, s+" (details: "+e.Error()+")")
		} else {
			lines = append(lines, e.Error())
		}
	}
	return strings.Join(lines, "\n")
}

// JoinSentences joins non-empty parts with a single space after
// sentence-ending punctuation (. ! ?) and with "; " otherwise.
func JoinSentences(parts ...string) string {
	var b strings.Builder
	last := ""
	for _, p := range parts {
		if p == "" {
			continue
		}
		if last != "" {
			if strings.HasSuffix(last, ".") || strings.HasSuffix(last, "!") || strings.HasSuffix(last, "?") {
				b.WriteString(" ")
			} else {
				b.WriteString("; ")
			}
		}
		b.WriteString(p)
		last = p
	}
	return b.String()
}

// Short is the sentence only, for tooltips: no details, several joined
// (see JoinSentences). An error without a rule keeps its original text. nil
// gives "".
func Short(err error) string {
	sentence, _ := Split(err)
	return sentence
}

// Split returns the Short text and the technical text of the errors that were
// rewritten, joined with "; ". details is empty when no rule matched, so the
// sentence is then the original text.
func Split(err error) (sentence, details string) {
	if err == nil {
		return "", ""
	}
	var parts, techs []string
	for _, e := range leaves(err) {
		if s, ok := sentenceFor(e); ok {
			parts = append(parts, s)
			techs = append(techs, e.Error())
		} else {
			parts = append(parts, e.Error())
		}
	}
	return JoinSentences(parts...), strings.Join(techs, "; ")
}

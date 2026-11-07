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
	"os/exec"
	"regexp"
	"strings"
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
)

// nouns names what a switch changes, for the sentences.
var nouns = map[string]string{
	idIcons:         "desktop icons",
	idNotifications: "notifications",
	idWallpaper:     "the wallpaper",
	idMute:          "the sound",
	idMinimize:      "the desktop",
}

var (
	saveStateRE   = regexp.MustCompile(`^save state: write state (.+?): `)
	parseStateRE  = regexp.MustCompile(`(?:^|: )parse state (.+?): `)
	parseConfigRE = regexp.MustCompile(`(?:^|: )parse config (.+?): `)
	unsupportedRE = regexp.MustCompile(`(?:^|: )unsupported desktop "([^"]*)"`)
)

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
			return fmt.Sprintf(`Couldn't save Prezilla's state (%s). Check free space and permissions, then run "prezilla off".`, m[1]), true
		}
		return "", false
	}},
	{"timer watcher not started", func(_ error, text string) (string, bool) {
		if strings.HasPrefix(text, "start watcher: ") {
			return `Couldn't start the background timer, so presentation mode won't end by itself. Run "prezilla off" when you are done.`, true
		}
		return "", false
	}},
	{"notification server has no actions", func(_ error, text string) (string, bool) {
		if strings.HasPrefix(text, "notify: server does not support actions") {
			return "Your notification server can't show buttons. Use Keep presenting and Stop presenting in the tray menu.", true
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
	{"xfconf-query missing", func(err error, text string) (string, bool) {
		for _, id := range []string{idIcons, idNotifications, idWallpaper} {
			if hasSwitchPrefix(text, id) &&
				(errors.Is(err, exec.ErrNotFound) || strings.Contains(text, "executable file not found")) {
				return fmt.Sprintf("Couldn't change %s: xfconf-query was not found. Prezilla needs an XFCE session.", nouns[id]), true
			}
		}
		return "", false
	}},
	{"wallpaper: no image", func(_ error, text string) (string, bool) {
		if hasSwitchPrefix(text, idWallpaper) && strings.Contains(text, "no image configured") {
			return `Couldn't change the wallpaper: no image is set. Set "wallpaper" to an image path in config.json, or remove "wallpaper" from "switches".`, true
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

// leaves flattens errors.Join trees (anything with Unwrap() []error) into the
// individual errors, in order.
func leaves(err error) []error {
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		var out []error
		for _, e := range j.Unwrap() {
			if e != nil {
				out = append(out, leaves(e)...)
			}
		}
		return out
	}
	return []error{err}
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

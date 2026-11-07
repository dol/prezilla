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
	parseStateRE  = regexp.MustCompile(`(?:^|: )parse state (.+?): `)
	parseConfigRE = regexp.MustCompile(`(?:^|: )parse config (.+?): `)
	unsupportedRE = regexp.MustCompile(`(?:^|: )unsupported desktop "([^"]*)"`)
)

// hasSwitchPrefix reports whether text starts with "<id>: ".
func hasSwitchPrefix(text, id string) bool { return strings.HasPrefix(text, id+": ") }

// rules is the single matcher table: one row per case. Only the listed
// prefix/cause combinations are rewritten; everything else is left alone.
var rules = []rule{
	{"mute: no pulse server", func(_ error, text string) (string, bool) {
		if hasSwitchPrefix(text, idMute) && strings.Contains(text, "pulse: connect") {
			return `Couldn't change the sound: no PulseAudio or PipeWire server is running. Start your sound server, or remove "mute" from "switches" in config.json.`, true
		}
		return "", false
	}},
	{"minimize: no X display", func(_ error, text string) (string, bool) {
		if hasSwitchPrefix(text, idMinimize) && strings.Contains(text, "x11: connect") {
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
				"Turn desktop icons, notifications, sound and windows back on by hand, then delete that file.", m[1]), true
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
func sentence(err error) (string, bool) {
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
		if s, ok := sentence(e); ok {
			lines = append(lines, s+" (details: "+e.Error()+")")
		} else {
			lines = append(lines, e.Error())
		}
	}
	return strings.Join(lines, "\n")
}

// Short is the sentence only, for tooltips: no details, several joined with
// "; ". An error without a rule keeps its original text. nil gives "".
func Short(err error) string {
	if err == nil {
		return ""
	}
	var parts []string
	for _, e := range leaves(err) {
		if s, ok := sentence(e); ok {
			parts = append(parts, s)
		} else {
			parts = append(parts, e.Error())
		}
	}
	return strings.Join(parts, "; ")
}

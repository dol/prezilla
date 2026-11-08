package tray

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"
	"unicode/utf8"

	"prezilla/internal/system"
)

const (
	// maxWindowSlots is how many windows the submenu can show; menu items
	// cannot be added after the menu is built, so they are created up front.
	maxWindowSlots = 15
	// maxWindowTitle is the longest title shown, in characters.
	maxWindowTitle = 60

	// windowListTimeout bounds one refresh of the window list.
	windowListTimeout = 3 * time.Second

	windowsTitle   = "Bring window to front"
	windowsTooltip = "Brings a window to the front, also one that is minimized."
)

// slotTitle is the menu text of w: its title, shortened to fit a menu.
func slotTitle(w system.Window) string {
	t := w.Title
	if utf8.RuneCountInString(t) <= maxWindowTitle {
		return t
	}
	r := []rune(t)
	return string(r[:maxWindowTitle-1]) + "…"
}

// windowList lists the windows for the submenu and focuses the one clicked.
// It remembers the last list so a click on a slot means the window shown
// there. A session that cannot list windows is asked only once.
type windowList struct {
	src system.WindowFocus

	mu          sync.Mutex
	ids         []string
	unsupported bool
	lastErr     string
}

// supported reports whether the submenu can ever show anything.
func (l *windowList) supported() bool {
	if l == nil || l.src == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return !l.unsupported
}

// refresh lists the windows and returns the slot titles, at most
// maxWindowSlots. A failure returns no titles; the same failure is printed
// to stderr only once until the list works again, and a session without
// window support is given up on.
func (l *windowList) refresh(ctx context.Context) []string {
	if !l.supported() {
		return nil
	}
	ws, err := l.src.List(ctx)
	l.mu.Lock()
	defer l.mu.Unlock()
	if err != nil {
		if errors.Is(err, system.ErrNoWindowFocus) {
			l.unsupported = true
		}
		note := noteExpiry(l.lastErr, err)
		l.lastErr = note.explained
		if note.print {
			fmt.Fprintln(os.Stderr, note.explained)
		}
		l.ids = nil
		return nil
	}
	l.lastErr = ""
	if len(ws) > maxWindowSlots {
		ws = ws[:maxWindowSlots]
	}
	l.ids = make([]string, len(ws))
	titles := make([]string, len(ws))
	for i, w := range ws {
		l.ids[i] = w.ID
		titles[i] = slotTitle(w)
	}
	return titles
}

// focus brings the window shown in slot to the front.
func (l *windowList) focus(ctx context.Context, slot int) error {
	l.mu.Lock()
	var id string
	ok := slot >= 0 && slot < len(l.ids)
	if ok {
		id = l.ids[slot]
	}
	l.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: slot %d", system.ErrNoWindow, slot)
	}
	return l.src.Focus(ctx, id)
}

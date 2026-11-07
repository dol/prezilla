package system

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

// PromptAction is one button of a Prompt.
type PromptAction struct{ Key, Label string }

// Prompt is a sticky question shown as a desktop notification.
type Prompt struct {
	Summary, Body, IconName string
	Actions                 []PromptAction
}

// Prompter asks a question and waits for the answer.
type Prompter interface {
	// Ask blocks until an action is invoked (its key is returned), the user
	// dismissed the notification (returns "", nil), the notification server
	// closed it for any other reason (returns "", ErrNotificationGone: the
	// question is unanswered) or ctx ends (the notification is closed and the
	// wrapped ctx error returned).
	Ask(ctx context.Context, p Prompt) (key string, err error)
}

// ErrNotificationGone reports that the notification server closed a prompt
// (expired, suppressed, closed by a call or for an undefined reason) without
// the user answering or dismissing it. The question is still open.
var ErrNotificationGone = errors.New("notify: notification closed by the server")

// closedByUser is the NotificationClosed reason "dismissed by the user".
const closedByUser = uint32(2)

const (
	notifyIface        = "org.freedesktop.Notifications"
	notifyPath         = dbus.ObjectPath("/org/freedesktop/Notifications")
	promptCloseTimeout = 2 * time.Second
)

// DBusPrompter shows a Prompt through org.freedesktop.Notifications as a
// critical, resident notification that never expires, and waits for the
// ActionInvoked or NotificationClosed signal of that notification. Each call
// uses its own private connection. The zero value is valid.
type DBusPrompter struct {
	// Timeout bounds connecting and each D-Bus call; zero means 5 seconds.
	Timeout time.Duration
}

func (d DBusPrompter) timeout() time.Duration {
	if d.Timeout > 0 {
		return d.Timeout
	}
	return defaultNotifyTimeout
}

// Ask implements Prompter.
func (d DBusPrompter) Ask(ctx context.Context, p Prompt) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("notify: %w", err)
	}
	timeout := d.timeout()
	conn, err := connectSession(ctx, timeout, "notify")
	if err != nil {
		return "", err
	}
	defer conn.Close()
	obj := conn.Object(notifyIface, notifyPath)

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var caps []string
	if err := obj.CallWithContext(callCtx, notifyIface+".GetCapabilities", 0).Store(&caps); err != nil {
		if cerr := boundErr("notify: capabilities", callCtx, ctx, timeout); cerr != nil {
			return "", cerr
		}
		return "", fmt.Errorf("notify: capabilities: %w", err)
	}
	if !hasCapability(caps, "actions") {
		return "", errors.New("notify: server does not support actions")
	}

	// Subscribe before Notify so an immediate answer cannot be missed.
	ch := make(chan *dbus.Signal, 64)
	conn.Signal(ch)
	defer conn.RemoveSignal(ch)
	for _, member := range []string{"ActionInvoked", "NotificationClosed"} {
		err := conn.AddMatchSignalContext(callCtx, dbus.WithMatchInterface(notifyIface),
			dbus.WithMatchMember(member), dbus.WithMatchObjectPath(notifyPath))
		if err != nil {
			if cerr := boundErr("notify: subscribe", callCtx, ctx, timeout); cerr != nil {
				return "", cerr
			}
			return "", fmt.Errorf("notify: subscribe: %w", err)
		}
	}

	// Never show the prompt for a caller that has already given up.
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("notify: %w", err)
	}
	// The Notify call itself is not cut by ctx, so the id is always known
	// and the notification can be closed again below.
	sendCtx, sendCancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer sendCancel()
	var id uint32
	err = obj.CallWithContext(sendCtx, notifyIface+".Notify", 0,
		"Prezilla", uint32(0), p.IconName, p.Summary, p.Body,
		promptActions(p), promptHints(), int32(0)).Store(&id)
	if err != nil {
		if sendCtx.Err() != nil {
			return "", fmt.Errorf("notify: send: timed out after %s: %w", timeout, context.DeadlineExceeded)
		}
		return "", fmt.Errorf("notify: send: %w", err)
	}

	closeIt := func() {
		cctx, ccancel := context.WithTimeout(context.WithoutCancel(ctx), promptCloseTimeout)
		defer ccancel()
		_ = obj.CallWithContext(cctx, notifyIface+".CloseNotification", 0, id).Err
	}
	for {
		select {
		case <-ctx.Done():
			closeIt()
			return "", fmt.Errorf("notify: %w", ctx.Err())
		case sig, ok := <-ch:
			if !ok {
				return "", errors.New("notify: connection closed")
			}
			key, done, err := promptSignal(sig, id)
			if !done {
				continue
			}
			if err != nil {
				return "", err
			}
			if key != "" {
				// Resident notifications stay after an action; remove it.
				closeIt()
			}
			return key, nil
		}
	}
}

// promptActions flattens the actions to [key, label, ...].
func promptActions(p Prompt) []string {
	out := make([]string, 0, 2*len(p.Actions))
	for _, a := range p.Actions {
		out = append(out, a.Key, a.Label)
	}
	return out
}

// promptHints marks the notification critical and resident.
func promptHints() map[string]dbus.Variant {
	return map[string]dbus.Variant{
		"urgency":  dbus.MakeVariant(byte(2)),
		"resident": dbus.MakeVariant(true),
	}
}

func hasCapability(caps []string, want string) bool {
	for _, c := range caps {
		if c == want {
			return true
		}
	}
	return false
}

// promptSignal interprets a notification signal for notification id: done is
// true for ActionInvoked (key is the action) and NotificationClosed (key "").
// A close for any reason other than "dismissed by the user" (including a
// malformed reason) yields ErrNotificationGone.
func promptSignal(sig *dbus.Signal, id uint32) (key string, done bool, err error) {
	if sig == nil || len(sig.Body) < 2 {
		return "", false, nil
	}
	if got, ok := sig.Body[0].(uint32); !ok || got != id {
		return "", false, nil
	}
	switch sig.Name {
	case notifyIface + ".ActionInvoked":
		k, ok := sig.Body[1].(string)
		return k, ok, nil
	case notifyIface + ".NotificationClosed":
		if reason, ok := sig.Body[1].(uint32); ok && reason == closedByUser {
			return "", true, nil
		}
		return "", true, ErrNotificationGone
	}
	return "", false, nil
}

// FakePrompter records prompts. Ask returns Err if set (use
// ErrNotificationGone to simulate a server-closed notification); otherwise,
// with Answer non-nil, it blocks for a key from Answer or the end of ctx;
// else it returns Key.
type FakePrompter struct {
	Key    string
	Err    error
	Answer chan string

	mu        sync.Mutex
	prompts   []Prompt
	cancelled int
}

// Ask implements Prompter.
func (f *FakePrompter) Ask(ctx context.Context, p Prompt) (string, error) {
	f.mu.Lock()
	f.prompts = append(f.prompts, p)
	f.mu.Unlock()
	if f.Err != nil {
		return "", f.Err
	}
	if f.Answer == nil {
		return f.Key, nil
	}
	select {
	case k := <-f.Answer:
		return k, nil
	case <-ctx.Done():
		f.mu.Lock()
		f.cancelled++
		f.mu.Unlock()
		return "", fmt.Errorf("notify: %w", ctx.Err())
	}
}

// Prompts returns the prompts asked so far.
func (f *FakePrompter) Prompts() []Prompt {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Prompt(nil), f.prompts...)
}

// Cancelled counts the Ask calls that ended because ctx did.
func (f *FakePrompter) Cancelled() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelled
}

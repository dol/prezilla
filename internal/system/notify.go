package system

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/godbus/dbus/v5"
)

// Notifier shows a desktop notification.
type Notifier interface {
	Notify(ctx context.Context, summary, body, iconName string) error
}

// DBusNotifier sends notifications through org.freedesktop.Notifications on
// the session bus. Each call opens its own private connection and closes it
// again. The zero value is valid.
type DBusNotifier struct {
	// Timeout bounds each call; zero means 5 seconds.
	Timeout time.Duration
}

const (
	defaultNotifyTimeout = 5 * time.Second
	notifyExpireMillis   = 10000
)

func (n DBusNotifier) timeout() time.Duration {
	if n.Timeout > 0 {
		return n.Timeout
	}
	return defaultNotifyTimeout
}

// Notify implements Notifier.
func (n DBusNotifier) Notify(parent context.Context, summary, body, iconName string) error {
	if err := parent.Err(); err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	timeout := n.timeout()
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	ch := make(chan error, 1)
	go func() {
		conn, err := dbus.ConnectSessionBus()
		if err != nil {
			ch <- fmt.Errorf("notify: connect: %w", err)
			return
		}
		defer conn.Close()
		// The context may have fired while connecting; never send then.
		if err := ctx.Err(); err != nil {
			ch <- fmt.Errorf("notify: %w", err)
			return
		}
		call := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").
			CallWithContext(ctx, "org.freedesktop.Notifications.Notify", 0,
				"prezilla", uint32(0), iconName, summary, body,
				[]string{}, map[string]dbus.Variant{}, int32(notifyExpireMillis))
		if call.Err != nil {
			ch <- fmt.Errorf("notify: send: %w", call.Err)
			return
		}
		ch <- nil
	}()
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && parent.Err() == nil {
			return fmt.Errorf("notify: timed out after %s: %w", timeout, context.DeadlineExceeded)
		}
		return fmt.Errorf("notify: %w", parent.Err())
	}
}

// NotifyCall is one recorded FakeNotifier call.
type NotifyCall struct{ Summary, Body, Icon string }

// FakeNotifier records calls for tests and returns Err.
type FakeNotifier struct {
	Calls []NotifyCall
	Err   error
}

// Notify implements Notifier.
func (f *FakeNotifier) Notify(_ context.Context, summary, body, iconName string) error {
	f.Calls = append(f.Calls, NotifyCall{summary, body, iconName})
	return f.Err
}

package system

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Opener opens a URL in the user's default browser.
type Opener interface {
	Open(ctx context.Context, url string) error
}

// ExecOpener tries xdg-open, then exo-open (shipped with XFCE).
type ExecOpener struct{ Runner Runner }

// Open implements Opener. Only http and https URLs are accepted.
func (o ExecOpener) Open(ctx context.Context, url string) error {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return errors.New("open: refusing non-http(s) URL")
	}
	_, xdgErr := o.Runner.Run(ctx, "xdg-open", url)
	if xdgErr == nil {
		return nil
	}
	_, exoErr := o.Runner.Run(ctx, "exo-open", url)
	if exoErr == nil {
		return nil
	}
	return fmt.Errorf("open: xdg-open failed (%v); exo-open failed (%v)", xdgErr, exoErr)
}

// FakeOpener records opened URLs for tests and returns Err.
type FakeOpener struct {
	Calls []string
	Err   error
}

// Open implements Opener.
func (f *FakeOpener) Open(_ context.Context, url string) error {
	f.Calls = append(f.Calls, url)
	return f.Err
}

package system

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"sync"
)

// Wallpaper reads and sets the desktop wallpaper of the whole session.
type Wallpaper interface {
	// Get returns the path of the current wallpaper.
	Get(ctx context.Context) (string, error)
	// Set makes the image at path the wallpaper.
	Set(ctx context.Context, path string) error
}

// ErrUnsupportedDesktop means the session's desktop has no wallpaper
// implementation here; callers explain that the wallpaper switch is not
// available.
var ErrUnsupportedDesktop = errors.New("wallpaper: desktop not supported")

// fileURI returns the file: URI of path.
func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}

// pathFromURI returns the path of a file: URI; anything else is returned
// unchanged, since some desktops store plain paths.
func pathFromURI(s string) string {
	if !strings.HasPrefix(s, "file://") {
		return s
	}
	if u, err := url.Parse(s); err == nil && u.Path != "" {
		return u.Path
	}
	return strings.TrimPrefix(s, "file://")
}

// FakeWallpaper is an in-memory Wallpaper for tests. Calls records "Get" and
// "Set <path>".
type FakeWallpaper struct {
	mu sync.Mutex

	Current string
	GetErr  error
	SetErr  error
	Calls   []string
}

// Get implements Wallpaper.
func (f *FakeWallpaper) Get(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "Get")
	return f.Current, f.GetErr
}

// Set implements Wallpaper.
func (f *FakeWallpaper) Set(_ context.Context, path string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, "Set "+path)
	if f.SetErr != nil {
		return f.SetErr
	}
	f.Current = path
	return nil
}

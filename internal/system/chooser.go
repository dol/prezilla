package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// FileChooser lets the user pick an image file.
type FileChooser interface {
	ChooseImage(ctx context.Context, startDir string) (string, error)
}

// ErrCancelled means the user closed the chooser without picking a file.
var ErrCancelled = errors.New("file chooser: cancelled")

// ErrImageMissing means the path does not name an existing regular file.
var ErrImageMissing = errors.New("image file missing")

// ErrNotImage means the file's extension is not an accepted image type.
var ErrNotImage = errors.New("not an image file")

// imageExts are the accepted image extensions, matched case-insensitively.
var imageExts = []string{"png", "jpg", "jpeg", "webp", "bmp", "gif", "tif", "tiff", "svg"}

// ValidateImage checks that path is an existing regular file with an
// accepted image extension.
func ValidateImage(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrImageMissing, path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: %s is not a regular file", ErrImageMissing, path)
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	for _, e := range imageExts {
		if ext == e {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrNotImage, path)
}

// ErrNoZenity means zenity is not installed (or no runner is configured);
// callers explain how to set the wallpaper in config.json instead.
var ErrNoZenity = errors.New("zenity not found")

// zenityStartFailure reports whether an exit-1 error means zenity never
// showed a window: GTK says on stderr that it cannot open the display.
func zenityStartFailure(err error) bool {
	text := err.Error()
	return strings.Contains(text, "cannot open display") ||
		strings.Contains(text, "Failed to open display")
}

// errZenityStart marks zenity failing to show its window.
var errZenityStart = errors.New("zenity could not start its window")

// zenity --file-selection exit codes besides 0 that mean "closed, nothing to
// do": 1 (Esc or the title-bar X), 5 (--timeout) and 255 (title-bar X or Esc
// on other zenity versions). Exit 1 is also what GTK reports when it cannot
// start (dead display); see zenityStartFailure.
var zenityClosed = map[int]bool{1: true, 5: true, 255: true}

// ZenityChooser asks for an image with zenity's file dialog. A missing
// zenity yields an error wrapping ErrNoZenity.
type ZenityChooser struct {
	Runner Runner
}

// chooserArgs builds the argv (after "zenity") for the file dialog. The
// --filename must end with "/" so the dialog starts inside the directory.
func chooserArgs(startDir string) []string {
	var pats []string
	for _, e := range imageExts {
		pats = append(pats, "*."+e, "*."+strings.ToUpper(e))
	}
	return []string{"--file-selection", "--title=Choose wallpaper",
		"--filename=" + strings.TrimSuffix(startDir, "/") + "/",
		"--file-filter=Images | " + strings.Join(pats, " ")}
}

// ChooseImage implements FileChooser. It returns the chosen path, ErrCancelled
// when the dialog was dismissed, ErrNoZenity when zenity is missing, ctx's
// error when ctx ended, or a wrapped zenity failure.
func (z ZenityChooser) ChooseImage(ctx context.Context, startDir string) (string, error) {
	if z.Runner == nil {
		return "", fmt.Errorf("%w: no runner configured", ErrNoZenity)
	}
	out, err := z.Runner.Run(ctx, "zenity", chooserArgs(startDir)...)
	if err != nil {
		var ec interface{ ExitCode() int }
		switch {
		case ctx.Err() != nil:
			return "", ctx.Err()
		case errors.Is(err, exec.ErrNotFound):
			return "", fmt.Errorf("%w: %w", ErrNoZenity, err)
		case errors.As(err, &ec) && zenityClosed[ec.ExitCode()]:
			if ec.ExitCode() == 1 && zenityStartFailure(err) {
				return "", fmt.Errorf("file chooser: %w: %w", errZenityStart, err)
			}
			return "", ErrCancelled
		}
		return "", fmt.Errorf("file chooser: %w", err)
	}
	return strings.TrimSuffix(out, "\n"), nil
}

// FakeChooser is a FileChooser for tests; it records each startDir.
type FakeChooser struct {
	Path  string
	Err   error
	Calls []string
}

// ChooseImage implements FileChooser.
func (f *FakeChooser) ChooseImage(_ context.Context, startDir string) (string, error) {
	f.Calls = append(f.Calls, startDir)
	return f.Path, f.Err
}

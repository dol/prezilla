package system

import (
	"context"

	"prezilla/internal/about"
)

// AboutWindow shows the program's About information.
type AboutWindow interface {
	Show(ctx context.Context, info about.Info) error
}

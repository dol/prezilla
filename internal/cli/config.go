package cli

import (
	"fmt"
	"io"

	"prezilla/internal/core"
)

// ResolveConfig decides what to do when loading the config for cmd failed
// with loadErr. Commands that only restore or report (off, status, watch,
// tray) warn on errOut and continue with the defaults, so a broken config can
// never block restoring; every other command fails with loadErr.
func ResolveConfig(cmd string, cfg core.Config, loadErr error, errOut io.Writer) (core.Config, error) {
	if loadErr == nil {
		return cfg, nil
	}
	switch cmd {
	case "off", "status", "watch", "tray":
		fmt.Fprintf(errOut, "warning: %v; using default configuration\n", loadErr)
		return core.DefaultConfig(), nil
	}
	return core.Config{}, loadErr
}

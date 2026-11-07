// Package icons embeds the 64x64 tray icons for the idle and active states.
package icons

import _ "embed"

// Idle is the tray icon shown while presentation mode is off.
//
//go:embed idle.png
var Idle []byte

// Active is the tray icon shown while presentation mode is on.
//
//go:embed active.png
var Active []byte

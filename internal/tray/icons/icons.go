// Package icons embeds the 64x64 tray icons for the idle and active states and
// the 128x128 application icon.
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

// App is the application icon shown in the About window (a copy of the
// 128x128 icon in packaging/icons).
//
//go:embed app.png
var App []byte

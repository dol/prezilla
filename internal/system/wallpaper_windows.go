package system

import (
	"context"
	"errors"
	"syscall"
	"unsafe"
)

// SystemParametersInfo actions and flags, see
// https://learn.microsoft.com/windows/win32/api/winuser/nf-winuser-systemparametersinfow
const (
	spiGetDeskWallpaper = 0x0073
	spiSetDeskWallpaper = 0x0014
	spifUpdateINIFile   = 0x01
	spifSendChange      = 0x02
	maxPath             = 260
)

var systemParametersInfo = syscall.NewLazyDLL("user32.dll").NewProc("SystemParametersInfoW")

// WindowsWallpaper reads and sets the wallpaper with SystemParametersInfo.
type WindowsWallpaper struct{}

// DefaultWallpaper is the Wallpaper of this operating system.
func DefaultWallpaper() Wallpaper { return WindowsWallpaper{} }

// Get implements Wallpaper.
func (WindowsWallpaper) Get(context.Context) (string, error) {
	var buf [maxPath]uint16
	r, _, err := systemParametersInfo.Call(spiGetDeskWallpaper, maxPath, uintptr(unsafe.Pointer(&buf[0])), 0)
	if r == 0 {
		return "", errors.New("wallpaper: SystemParametersInfo: " + err.Error())
	}
	return syscall.UTF16ToString(buf[:]), nil
}

// Set implements Wallpaper.
func (WindowsWallpaper) Set(_ context.Context, path string) error {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	r, _, err := systemParametersInfo.Call(spiSetDeskWallpaper, 0, uintptr(unsafe.Pointer(p)), spifUpdateINIFile|spifSendChange)
	if r == 0 {
		return errors.New("wallpaper: SystemParametersInfo: " + err.Error())
	}
	return nil
}

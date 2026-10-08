// SPDX-License-Identifier: Unlicense OR MIT

//go:build !haiku && !windows && !((linux && !android) || freebsd)

package tray

// Tray is unavailable here; Start always fails.
type Tray struct{}

func Start(Options) (*Tray, error) { return nil, ErrUnsupported }
func (*Tray) Available() bool      { return false }
func (*Tray) Close()               {}

func (*Tray) Notify(string, string, bool) error { return ErrUnsupported }

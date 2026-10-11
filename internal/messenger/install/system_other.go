// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package install

import "errors"

// InstallSystem is for Windows, where an administrator's copy of the
// program installs it for every user.
func InstallSystem(string) error { return errors.ErrUnsupported }

// UninstallSystem is for Windows, as InstallSystem is.
func UninstallSystem(string) error { return errors.ErrUnsupported }

// Relocate reports false: a running program can be removed here.
func Relocate(...string) (bool, error) { return false, nil }

// RemoveLater has nothing to remove: Relocate made no copy.
func RemoveLater() error { return nil }

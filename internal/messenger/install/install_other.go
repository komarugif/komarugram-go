// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows && !linux

package install

import (
	"context"
	"errors"
)

const supported = false

func userDir() string { return "" }

func install(context.Context, string, string, Options) (string, error) {
	return "", errors.ErrUnsupported
}

func uninstall(context.Context, Installation) error { return errors.ErrUnsupported }

// Find finds nothing where the program does not install itself.
func Find() (Installation, bool) { return Installation{}, false }

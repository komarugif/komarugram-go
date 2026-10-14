//go:build !linux || android

// SPDX-License-Identifier: Unlicense OR MIT
package shortcuts

import "context"

func CheckSystem(context.Context, []Chord) SystemReport { return SystemReport{} }

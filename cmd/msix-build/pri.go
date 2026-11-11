// SPDX-License-Identifier: Unlicense OR MIT

package main

import (
	_ "embed"
)

// defaultName is the package's identity name, and the name of the resource
// map of resourcesPRI.
const defaultName = "KomaruGram"

// resourcesPRI is the package resource index: it tells the system which of
// the pictures of logoNames to take for a scale, for a size of the taskbar's
// button, and whether to draw the accent color's plate behind. Without it
// the system takes the one file the manifest names, scales it and puts the
// plate behind: the taskbar's icon is small, blurred and on a blue square.
//
// It was made by Microsoft's makepri from the files of logoNames, whose
// contents it does not hold, and from the manifest, which gives it the name
// of the resource map: it is for the identity name defaultName only. See
// docs/BUILD_MSIX.md, "The package's icons", for making it again.
//
//go:embed resources.pri
var resourcesPRI []byte

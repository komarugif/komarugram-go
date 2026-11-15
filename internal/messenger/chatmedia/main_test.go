// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import (
	"os"
	"testing"

	"komarugram/pkg/sandbox"
)

// TestMain gives the decoders the limit of a machine that draws on the CPU
// (sandbox.FrameSlow): the tests run close together, and often in a virtual
// machine, where at the limit of a real one a sticker's frame failed now and
// then.
func TestMain(m *testing.M) {
	sandbox.SetSoftwareRendered(true)
	os.Exit(m.Run())
}

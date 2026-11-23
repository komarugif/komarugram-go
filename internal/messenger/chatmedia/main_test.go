// SPDX-License-Identifier: Unlicense OR MIT

package chatmedia

import (
	"os"
	"testing"

	"komarugram/pkg/hwvideo"
	"komarugram/pkg/sandbox"
)

// TestMain handles the hardware decoder helper subprocess and gives the
// decoders the limit of a machine that draws on the CPU (sandbox.FrameSlow):
// the tests run close together, and often in a virtual machine, where at the
// limit of a real one a sticker's frame failed now and then.
func TestMain(m *testing.M) {
	if os.Getenv(hardwareHelperEnv) != "" {
		_ = hwvideo.Serve(os.Stdin, os.Stdout)
		os.Exit(0)
	}
	sandbox.SetSoftwareRendered(true)
	os.Exit(m.Run())
}

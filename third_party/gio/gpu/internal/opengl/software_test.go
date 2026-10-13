// SPDX-License-Identifier: Unlicense OR MIT

package opengl

import "testing"

func TestSoftwareRenderer(t *testing.T) {
	for renderer, want := range map[string]bool{
		"llvmpipe (LLVM 19.1.1, 256 bits)": true,
		"softpipe":                         true,
		"Software Rasterizer":              true,
		"zink Vulkan 1.3(llvmpipe (LLVM 17.0.6, 256 bits) (MESA_LLVMPIPE))": true,
		"Apple Software Renderer": true,
		"GDI Generic":             true,
		"Google SwiftShader":      true,
		"AMD Radeon RX 6600 (radeonsi, navi23, LLVM 19.1.1, DRM 3.61, 6.14.0)": false,
		"Mesa Intel(R) UHD Graphics 620 (KBL GT2)":                             false,
		"NVIDIA GeForce RTX 3060/PCIe/SSE2":                                    false,
	} {
		if got := softwareRenderer(renderer); got != want {
			t.Errorf("softwareRenderer(%q) = %v, want %v", renderer, got, want)
		}
	}
}

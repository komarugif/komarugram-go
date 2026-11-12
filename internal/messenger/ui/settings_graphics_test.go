// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/app"
	"gioui.org/gpu"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/preferences"
)

var (
	testIntel  = app.VulkanDevice{Driver: "intel_icd.x86_64.json", VendorID: 0x8086, DeviceID: 0x4626, Name: "Intel(R) Graphics (ADL GT2)", Kind: app.VulkanDeviceIntegrated, API: "Vulkan 1.4.354", Version: "26.1.6"}
	testNVIDIA = app.VulkanDevice{Driver: "nvidia_icd.json", VendorID: 0x10de, DeviceID: 0x25a2, Name: "NVIDIA GeForce RTX 3050 Laptop GPU", Kind: app.VulkanDeviceDiscrete, API: "Vulkan 1.4.312", Version: "610.43.3"}
)

// newTestGraphicsView is a section of hardware acceleration on Wayland with
// two GPUs, keeping the setting in g and counting the listings of devices,
// which wake GPUs up.
func newTestGraphicsView(g *preferences.Graphics, listings *atomic.Int32) *graphicsView {
	v := newGraphicsView(func() {})
	v.info = func() gpu.DeviceInfo {
		return gpu.DeviceInfo{API: "OpenGL ES 3.2", Device: "Mesa Intel(R) Graphics (ADL GT2)", Driver: "Mesa 26.1.6"}
	}
	v.choice = func() bool { return true }
	v.devices = func() ([]app.VulkanDevice, error) {
		listings.Add(1)
		return []app.VulkanDevice{testIntel, testNVIDIA}, nil
	}
	v.get = func() preferences.Graphics { return *g }
	v.set = func(n preferences.Graphics) { *g = n }
	return v
}

func layoutGraphics(v *graphicsView, l localization.Catalog) {
	gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(600, 900)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	heights := map[string]*heightTransition{}
	v.Layout(gtx, l, func(key string) *heightTransition {
		if heights[key] == nil {
			heights[key] = new(heightTransition)
		}
		return heights[key]
	})
}

// Listing the Vulkan devices wakes every GPU up: the section lists them
// only once Vulkan is chosen, and once.
func TestGraphicsListsDevicesForVulkanOnly(t *testing.T) {
	l := localization.For("en")
	g := preferences.Graphics{API: preferences.GraphicsOpenGL}
	var listings atomic.Int32
	v := newTestGraphicsView(&g, &listings)
	for range 3 {
		layoutGraphics(v, l)
	}
	if n := listings.Load(); n != 0 {
		t.Fatalf("listed the devices %d times with OpenGL chosen", n)
	}
	v.chooseAPI(preferences.GraphicsVulkan)
	if g.API != preferences.GraphicsVulkan {
		t.Fatalf("API kept is %q", g.API)
	}
	for range 50 {
		layoutGraphics(v, l)
		if v.deviceKeys != nil && len(v.deviceKeys) == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := listings.Load(); n != 1 {
		t.Fatalf("listed the devices %d times", n)
	}
	if want := []string{"", vulkanDeviceKey(testIntel.Driver, testIntel.VendorID, testIntel.DeviceID), vulkanDeviceKey(testNVIDIA.Driver, testNVIDIA.VendorID, testNVIDIA.DeviceID)}; strings.Join(v.deviceKeys, ",") != strings.Join(want, ",") {
		t.Fatalf("devices offered %q, want %q", v.deviceKeys, want)
	}
	v.chooseDevice(testIntel)
	want := preferences.VulkanDevice{Driver: "intel_icd.x86_64.json", VendorID: 0x8086, DeviceID: 0x4626, Name: "Intel(R) Graphics (ADL GT2)"}
	if g.Vulkan != want {
		t.Fatalf("device kept %+v, want %+v", g.Vulkan, want)
	}
	layoutGraphics(v, l)
	if got := v.device.GetValue(); got != vulkanDeviceKey(want.Driver, want.VendorID, want.DeviceID) {
		t.Fatalf("device shown chosen %q", got)
	}
}

// The setting reaches Gio as it is kept.
func TestApplyGraphics(t *testing.T) {
	defer app.SetGraphics(app.Graphics{})
	ApplyGraphics(preferences.Graphics{API: preferences.GraphicsVulkan, Vulkan: preferences.VulkanDevice{Driver: "intel_icd.x86_64.json", VendorID: 0x8086, DeviceID: 0x4626, Name: "Intel"}})
	got := app.CurrentGraphics()
	want := app.Graphics{API: app.GraphicsVulkan, Vulkan: app.VulkanDevice{Driver: "intel_icd.x86_64.json", VendorID: 0x8086, DeviceID: 0x4626, Name: "Intel"}}
	if got != want {
		t.Fatalf("Gio has %+v, want %+v", got, want)
	}
}

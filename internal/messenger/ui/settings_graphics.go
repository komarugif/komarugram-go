// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"

	"gioui.org/app"
	"gioui.org/gpu"
	"gioui.org/layout"

	"gio-mw/token"
	"gio-mw/widget/radio"
	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/preferences"
)

// ApplyGraphics hands the setting of how windows are drawn to Gio, for the
// GPU each window makes from now on: at start, and before app.ResetGPUs
// when it changes. The Vulkan device is the one chosen, or the one the
// automatic choice drew with before, so that its driver is loaded alone.
func ApplyGraphics(g preferences.Graphics) {
	api := app.GraphicsAuto
	switch g.API {
	case preferences.GraphicsOpenGL:
		api = app.GraphicsOpenGL
	case preferences.GraphicsVulkan:
		api = app.GraphicsVulkan
	}
	d := g.Effective()
	app.SetGraphics(app.Graphics{API: api, Vulkan: app.VulkanDevice{
		Driver:   d.Driver,
		VendorID: d.VendorID,
		DeviceID: d.DeviceID,
		Name:     d.Name,
	}})
}

// graphicsChanged reports whether what windows are drawn with changes from
// before to after: the API, or the Vulkan device while Vulkan is chosen.
// The windows then make their GPU anew (app.ResetGPUs).
func graphicsChanged(before, after preferences.Graphics) bool {
	if before.API != after.API {
		return true
	}
	return after.API == preferences.GraphicsVulkan && !before.Effective().Same(after.Effective())
}

// toVulkanDevice keeps a device Gio listed.
func toVulkanDevice(d app.VulkanDevice) preferences.VulkanDevice {
	return preferences.VulkanDevice{
		Driver: d.Driver, VendorID: d.VendorID, DeviceID: d.DeviceID, Name: d.Name,
		Kind: uint8(d.Kind), API: d.API, Version: d.Version,
	}
}

func toVulkanDevices(devices []app.VulkanDevice) []preferences.VulkanDevice {
	kept := make([]preferences.VulkanDevice, len(devices))
	for i, d := range devices {
		kept[i] = toVulkanDevice(d)
	}
	return kept
}

// rememberedVulkan is g with Auto the device that info tells windows are
// drawn with, where the automatic choice drew with another than the one
// remembered: on the first start, or where the one remembered could not
// draw. ok is false where there is nothing to remember; known false where
// the device is not among g.Known, which must be listed anew first.
func rememberedVulkan(g preferences.Graphics, info gpu.DeviceInfo) (remembered preferences.Graphics, ok, known bool) {
	if !strings.HasPrefix(info.API, "Vulkan") || info.VendorID == 0 || !g.Vulkan.IsZero() {
		return g, false, true
	}
	if g.Auto.VendorID == info.VendorID && g.Auto.DeviceID == info.DeviceID {
		return g, false, true
	}
	for _, d := range g.Known {
		if d.Driver != "" && d.VendorID == info.VendorID && d.DeviceID == info.DeviceID {
			g.Auto = d
			return g, true, true
		}
	}
	return g, false, false
}

// graphicsStore is where the setting is kept: preferences.Store.
type graphicsStore interface {
	Global() preferences.Global
	SetGraphics(preferences.Graphics) error
}

// vulkanMemory keeps one listing of devices going for rememberVulkan, of
// the process: every window remembers the same device.
var vulkanMemory struct {
	sync.Mutex
	listing bool
}

// rememberVulkan remembers the device the automatic choice drew with, if
// it is another than the one remembered (rememberedVulkan), listing the
// devices first in the background if it is not among those known: the
// GPUs are awake then, every driver loaded. The windows then make their
// GPU anew with that device's driver alone, and the instance of every
// driver goes, letting the others sleep. reset is app.ResetGPUs.
func rememberVulkan(store graphicsStore, info gpu.DeviceInfo, devices func() ([]app.VulkanDevice, error), reset func()) {
	vulkanMemory.Lock()
	defer vulkanMemory.Unlock()
	if vulkanMemory.listing {
		return
	}
	g := store.Global().Graphics
	remembered, ok, known := rememberedVulkan(g, info)
	save := func(g preferences.Graphics) {
		if err := store.SetGraphics(g); err != nil {
			log.Printf("save settings: %v", err)
		}
		ApplyGraphics(g)
	}
	switch {
	case ok:
		save(remembered)
		reset()
	case !known:
		vulkanMemory.listing = true
		go func() {
			defer crash.Recover("remember vulkan device", func(*crash.Panic) {
				vulkanMemory.Lock()
				vulkanMemory.listing = false
				vulkanMemory.Unlock()
			})
			listed, err := devices()
			vulkanMemory.Lock()
			defer vulkanMemory.Unlock()
			vulkanMemory.listing = false
			if err != nil {
				log.Printf("vulkan devices: %v", err)
				return
			}
			g := store.Global().Graphics
			g.Known = toVulkanDevices(listed)
			remembered, ok, _ := rememberedVulkan(g, info)
			save(remembered)
			if ok {
				reset()
			}
		}()
	}
}

// graphicsView is the settings' section of hardware acceleration: what the
// windows are drawn with and, where there is a choice (Wayland), the
// graphics API and the Vulkan device to draw with.
type graphicsView struct {
	// get and set are the setting; the section shows no choice without them.
	get func() preferences.Graphics
	set func(preferences.Graphics)
	// info, choice and devices are Gio's (app.GPUInfo, GraphicsChoice,
	// VulkanDevices), replaced in tests.
	info       func() gpu.DeviceInfo
	choice     func() bool
	devices    func() ([]app.VulkanDevice, error)
	invalidate func()

	api *radio.Radios[string]
	// device chooses among "" (the first that can draw) and the keys of
	// the listed devices; deviceKeys is what it was made with.
	device     *radio.Radios[string]
	deviceKeys []string

	mu      sync.Mutex
	listing bool
	listed  []app.VulkanDevice
	listErr error
	done    bool
	// kept is whether the devices listed are kept as the known ones.
	kept bool
}

func newGraphicsView(invalidate func()) *graphicsView {
	return &graphicsView{
		info:       app.GPUInfo,
		choice:     app.GraphicsChoice,
		devices:    app.VulkanDevices,
		invalidate: invalidate,
	}
}

// vulkanDeviceKey names a device among the others: its driver and ids.
func vulkanDeviceKey(driver string, vendorID, deviceID uint32) string {
	if driver == "" && vendorID == 0 && deviceID == 0 {
		return ""
	}
	return fmt.Sprintf("%s/%04x:%04x", driver, vendorID, deviceID)
}

// subtitle is what the main page tells of the section: the API and the
// device windows are drawn with.
func (v *graphicsView) subtitle(l localization.Catalog) string {
	info := v.info()
	switch {
	case info.API != "" && info.Device != "":
		return info.API + " · " + info.Device
	case info.Device != "":
		return info.Device
	}
	return l.T("graphics.unknown")
}

// list asks for the Vulkan devices in the background, once: listing them
// loads every driver and wakes every GPU up.
func (v *graphicsView) list() {
	v.mu.Lock()
	if v.listing || v.done {
		v.mu.Unlock()
		return
	}
	v.listing = true
	v.mu.Unlock()
	go func() {
		finish := func(devices []app.VulkanDevice, err error) {
			v.mu.Lock()
			v.listed, v.listErr, v.listing, v.done = devices, err, false, true
			v.mu.Unlock()
			v.invalidate()
		}
		defer crash.Recover("vulkan devices", func(p *crash.Panic) { finish(nil, p) })
		finish(v.devices())
	}()
}

func (v *graphicsView) Layout(gtx layout.Context, l localization.Catalog, height func(string) *heightTransition) layout.Dimensions {
	cards := []layout.FlexChild{layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return v.layoutInfo(gtx, l, height("graphics-info"))
	})}
	if v.get != nil && v.choice() {
		g := v.get()
		if v.api == nil {
			v.api = radio.NewRadios([]string{preferences.GraphicsAuto, preferences.GraphicsOpenGL, preferences.GraphicsVulkan}, g.API, v.chooseAPI)
		}
		if v.api.GetValue() != g.API {
			// The setting is the truth: it may change without the radio.
			v.api.SetValue(g.API)
		}
		v.api.Update(gtx)
		cards = append(cards, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return settingsChoiceCard(gtx, height("graphics-api"), l.T("graphics.api"), l.T("graphics.api_hint"), func(gtx layout.Context) layout.Dimensions {
				return v.api.Layout(gtx, radio.LeadingKind, map[string]string{
					preferences.GraphicsAuto:   l.T("graphics.api_auto"),
					preferences.GraphicsOpenGL: "OpenGL",
					preferences.GraphicsVulkan: "Vulkan",
				})
			})
		}))
		if v.api.GetValue() == preferences.GraphicsVulkan {
			cards = append(cards, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return v.layoutDevices(gtx, l, height("graphics-device"))
			}))
		}
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, cards...)
}

// layoutInfo draws the card of what windows are drawn with.
func (v *graphicsView) layoutInfo(gtx layout.Context, l localization.Catalog, height *heightTransition) layout.Dimensions {
	info := v.info()
	value := func(s string) string {
		if s == "" {
			return l.T("graphics.unknown")
		}
		return s
	}
	rows := []struct{ name, value string }{
		{l.T("graphics.info_api"), value(info.API)},
		{l.T("graphics.info_device"), value(info.Device)},
		{l.T("graphics.info_driver"), value(info.Driver)},
	}
	sc := scheme(gtx)
	return settingsChoiceCard(gtx, height, l.T("graphics.info"), "", func(gtx layout.Context) layout.Dimensions {
		var children []layout.FlexChild
		for i, r := range rows {
			if i > 0 {
				children = append(children, vspace(8))
			}
			children = append(children,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, r.name, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 1)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, r.value, token.TypestyleBodyLarge, sc.Surface.OnColor, 0)
				}),
			)
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

// layoutDevices draws the card of the Vulkan device: the devices known
// from the last listing at once, those of a listing anew, made the first
// time the card is shown, once it is done, and kept.
func (v *graphicsView) layoutDevices(gtx layout.Context, l localization.Catalog, height *heightTransition) layout.Dimensions {
	v.list()
	v.mu.Lock()
	listed, listErr, done, kept := v.listed, v.listErr, v.done, v.kept
	v.kept = v.kept || done && listErr == nil
	v.mu.Unlock()
	g := v.get()
	shown := g.Known
	if done && listErr == nil {
		shown = toVulkanDevices(listed)
		if !kept {
			g.Known = shown
			v.set(g)
		}
	}
	chosen := vulkanDeviceKey(g.Vulkan.Driver, g.Vulkan.VendorID, g.Vulkan.DeviceID)
	keys := []string{""}
	auto := l.T("graphics.device_auto")
	if !g.Auto.IsZero() {
		auto = l.Format("graphics.device_auto_remembered", map[string]string{"name": g.Auto.Name})
	}
	labels := map[string]string{"": auto}
	devices := map[string]preferences.VulkanDevice{}
	for _, d := range shown {
		key := vulkanDeviceKey(d.Driver, d.VendorID, d.DeviceID)
		if _, ok := labels[key]; ok {
			continue
		}
		keys = append(keys, key)
		labels[key] = vulkanDeviceLabel(d, l)
		devices[key] = d
	}
	if _, ok := labels[chosen]; !ok {
		// A device chosen before and not listed now keeps its place.
		keys = append(keys, chosen)
		labels[chosen] = g.Vulkan.Name
		if done {
			labels[chosen] = l.Format("graphics.device_missing", map[string]string{"name": g.Vulkan.Name})
		}
	}
	if v.device == nil || !slices.Equal(v.deviceKeys, keys) {
		v.deviceKeys = keys
		v.device = radio.NewRadios(keys, chosen, func(key string) {
			if d, ok := devices[key]; ok || key == "" {
				v.chooseDevice(d)
			}
		})
	}
	if v.device.GetValue() != chosen {
		v.device.SetValue(chosen)
	}
	v.device.Update(gtx)
	hint := l.T("graphics.device_hint")
	switch {
	case !done && len(shown) > 0:
		hint = l.T("graphics.device_refreshing") + "\n" + hint
	case !done:
		hint = l.T("graphics.device_listing") + "\n" + hint
	case listErr != nil:
		hint = l.Format("graphics.device_error", map[string]string{"error": listErr.Error()}) + "\n" + hint
	}
	return settingsChoiceCard(gtx, height, l.T("graphics.device"), hint, func(gtx layout.Context) layout.Dimensions {
		return v.device.Layout(gtx, radio.LeadingKind, labels)
	})
}

// chooseAPI keeps the graphics API chosen.
func (v *graphicsView) chooseAPI(api string) {
	g := v.get()
	g.API = api
	v.set(g)
}

// chooseDevice keeps the Vulkan device chosen; the zero device is the
// automatic choice.
func (v *graphicsView) chooseDevice(d preferences.VulkanDevice) {
	g := v.get()
	g.Vulkan = preferences.VulkanDevice{Driver: d.Driver, VendorID: d.VendorID, DeviceID: d.DeviceID, Name: d.Name}
	v.set(g)
}

// vulkanDeviceLabel names a device with its kind and its driver's version.
func vulkanDeviceLabel(d preferences.VulkanDevice, l localization.Catalog) string {
	kind := map[app.VulkanDeviceKind]string{
		app.VulkanDeviceIntegrated: l.T("graphics.kind_integrated"),
		app.VulkanDeviceDiscrete:   l.T("graphics.kind_discrete"),
		app.VulkanDeviceVirtual:    l.T("graphics.kind_virtual"),
	}[app.VulkanDeviceKind(d.Kind)]
	label := d.Name
	if kind != "" {
		label += " · " + kind
	}
	if d.Version != "" {
		label += " · " + l.Format("graphics.driver_version", map[string]string{"version": d.Version})
	}
	return label
}

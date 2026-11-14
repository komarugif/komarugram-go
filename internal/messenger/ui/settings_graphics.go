// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gioui.org/app"
	"gioui.org/gpu"
	"gioui.org/layout"

	"gio-mw/token"
	"gio-mw/widget/radio"
	"komarugram/internal/appwindow"
	"komarugram/internal/crash"
	"komarugram/internal/diagnostics"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/preferences"
	"komarugram/pkg/hwvideo"
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
	}, VulkanFailed: g.VulkanFailed, D3D11: app.D3D11Adapter{
		VendorID: g.D3D11.VendorID,
		DeviceID: g.D3D11.DeviceID,
		Name:     g.D3D11.Name,
	}})
}

// graphicsChanged reports whether what windows are drawn with changes from
// before to after: the API, the Direct3D 11 adapter, or the Vulkan device
// while Vulkan may draw them, chosen or automatically. The windows then
// make their GPU anew (app.ResetGPUs).
func graphicsChanged(before, after preferences.Graphics) bool {
	if before.API != after.API || !before.D3D11.Same(after.D3D11) {
		return true
	}
	return after.API != preferences.GraphicsOpenGL && !before.Effective().Same(after.Effective())
}

// autoAPILabel is the automatic choice of the API, with the API it chose
// while it is the choice: Vulkan, OpenGL where Vulkan fails, as info, the
// GPU made last for a window, tells.
func autoAPILabel(l localization.Catalog, g preferences.Graphics, info gpu.DeviceInfo) string {
	api := ""
	switch {
	case g.API != preferences.GraphicsAuto:
	case strings.HasPrefix(info.API, "Vulkan"):
		api = "Vulkan"
	case strings.HasPrefix(info.API, "OpenGL"):
		api = "OpenGL"
	}
	if api == "" {
		return l.T("graphics.api_auto")
	}
	return l.Format("graphics.api_auto_chosen", map[string]string{"api": api})
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
// draw. Its driver is that of g.Known, else driver: the one Gio loaded
// alone (app.VulkanDriver), the compositor's. ok is false where there is
// nothing to remember; known false where the device is not among g.Known
// and Gio loaded every driver: the devices must be listed anew first.
func rememberedVulkan(g preferences.Graphics, info gpu.DeviceInfo, driver string) (remembered preferences.Graphics, ok, known bool) {
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
	if driver != "" {
		g.Auto = preferences.VulkanDevice{
			Driver: driver, VendorID: info.VendorID, DeviceID: info.DeviceID,
			Name: strings.TrimSuffix(info.Device, fmt.Sprintf(" (%04x:%04x)", info.VendorID, info.DeviceID)),
			API:  info.API, Version: info.Driver,
		}
		return g, true, true
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
// devices first in the background if it is not among those known and Gio
// loaded every driver (driver, app.VulkanDriver, is empty): the GPUs are
// awake then. The windows drawn with every driver then make their GPU anew
// with that device's driver alone, and the instance of every driver goes,
// letting the others sleep; those drawn with the driver alone keep it.
// reset is app.ResetGPUs.
func rememberVulkan(store graphicsStore, info gpu.DeviceInfo, driver string, devices func() ([]app.VulkanDevice, error), reset func()) {
	vulkanMemory.Lock()
	defer vulkanMemory.Unlock()
	if vulkanMemory.listing {
		return
	}
	g := store.Global().Graphics
	remembered, ok, known := rememberedVulkan(g, info, driver)
	save := func(g preferences.Graphics) {
		if err := store.SetGraphics(g); err != nil {
			log.Printf("save settings: %v", err)
		}
		ApplyGraphics(g)
	}
	switch {
	case ok:
		save(remembered)
		if driver == "" {
			reset()
		}
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
			remembered, ok, _ := rememberedVulkan(g, info, "")
			save(remembered)
			if ok {
				reset()
			}
		}()
	}
}

// rememberVulkanFailed keeps that Vulkan could not draw a window, as Gio
// told (app.CurrentGraphics().VulkanFailed), so that the next starts do
// not try it.
func rememberVulkanFailed(store graphicsStore, failed bool) {
	vulkanMemory.Lock()
	defer vulkanMemory.Unlock()
	g := store.Global().Graphics
	if !failed || g.VulkanFailed {
		return
	}
	g.VulkanFailed = true
	if err := store.SetGraphics(g); err != nil {
		log.Printf("save settings: %v", err)
	}
}

// graphicsView is the settings' section of hardware acceleration: what the
// windows are drawn with and, where there is a choice, the graphics API and
// the Vulkan device to draw with (Wayland), or the Direct3D 11 adapter
// (Windows).
type graphicsView struct {
	// get and set are the setting; the section shows no choice without them.
	get func() preferences.Graphics
	set func(preferences.Graphics)
	// info, choice, devices, adapterChoice, adapters and adapterFailed are
	// Gio's (app.GPUInfo, GraphicsChoice, VulkanDevices, D3D11AdapterChoice,
	// D3D11Adapters, D3D11AdapterFailed), replaced in tests.
	info          func() gpu.DeviceInfo
	choice        func() bool
	devices       func() ([]app.VulkanDevice, error)
	adapterChoice func() bool
	adapters      func() ([]app.D3D11Adapter, error)
	adapterFailed func() bool
	invalidate    func()

	// adapter chooses among "" (the system's default) and the keys of the
	// adapters listed; adapterKeys is what it was made with. The adapters
	// are listed once, the first time the card is shown: listing them wakes
	// no GPU up.
	adapter       *radio.Radios[string]
	adapterKeys   []string
	adaptersDone  bool
	adaptersShown []app.D3D11Adapter
	adaptersErr   error

	api *radio.Radios[string]
	// gifs chooses what decodes the saved GIFs, "" for software and
	// "hardware"; hardware tells whether the choice is there, and
	// hardwareFailed why the graphics card decodes none, both replaced in
	// tests.
	gifs           *radio.Radios[string]
	hardware       func() bool
	hardwareFailed func() error
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
		info:          app.GPUInfo,
		choice:        app.GraphicsChoice,
		devices:       app.VulkanDevices,
		adapterChoice: app.D3D11AdapterChoice,
		adapters:      app.D3D11Adapters,
		adapterFailed: app.D3D11AdapterFailed,
		invalidate:    invalidate,
		hardware:      hwvideo.Available,
		hardwareFailed: func() error {
			if c := hardwareVideo.Load(); c != nil {
				return c.Failed()
			}
			return nil
		},
	}
}

// d3d11AdapterKey names an adapter among the others: its ids.
func d3d11AdapterKey(vendorID, deviceID uint32) string {
	if vendorID == 0 && deviceID == 0 {
		return ""
	}
	return fmt.Sprintf("%04x:%04x", vendorID, deviceID)
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
			return settingsChoiceCard(gtx, height("graphics-api"), l.T("graphics.api"), "", func(gtx layout.Context) layout.Dimensions {
				return v.api.Layout(gtx, radio.LeadingKind, map[string]string{
					preferences.GraphicsAuto:   autoAPILabel(l, g, v.info()),
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
	if v.get != nil && v.adapterChoice() {
		cards = append(cards, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return v.layoutAdapters(gtx, l, height("graphics-adapter"))
		}))
	}
	if v.get != nil && v.hardware != nil && v.hardware() {
		g := v.get()
		value := ""
		if g.HardwareGIFs {
			value = "hardware"
		}
		if v.gifs == nil {
			v.gifs = radio.NewRadios([]string{"", "hardware"}, value, func(chosen string) {
				g := v.get()
				g.HardwareGIFs = chosen == "hardware"
				if g.HardwareGIFs {
					// A choice tries the graphics card again.
					resetHardwareVideo()
				}
				v.set(g)
			})
		}
		if v.gifs.GetValue() != value {
			v.gifs.SetValue(value)
		}
		v.gifs.Update(gtx)
		hint := l.T("graphics.gifs_hint")
		if g.HardwareGIFs && v.hardwareFailed != nil && v.hardwareFailed() != nil {
			hint = l.T("graphics.gifs_failed")
		}
		cards = append(cards, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return settingsChoiceCard(gtx, height("graphics-gifs"), l.T("graphics.gifs"), hint, func(gtx layout.Context) layout.Dimensions {
				return v.gifs.Layout(gtx, radio.LeadingKind, map[string]string{
					"":         l.T("graphics.gifs_software"),
					"hardware": l.T("graphics.gifs_hardware"),
				})
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, cards...)
}

// hardwareVideo is the client of the helper that decodes on the graphics
// card, of the process: made when the setting first asks for it.
var hardwareVideo atomic.Pointer[hwvideo.Client]

func init() {
	// Every window hidden draws nothing: the helpers that decode GIFs for
	// them are stopped, not left to stop on their own, and start again with
	// the next GIF a window shown plays.
	appwindow.OnAllHidden(func() {
		if c := hardwareVideo.Load(); c != nil {
			c.Close()
		}
	})
	// The profiler records the helpers that decode on the graphics card
	// with the process: their CPU goes there, not here.
	diagnostics.SetChildProfiler("hwvideo", func(ctx context.Context, kind string, d time.Duration) []diagnostics.ChildProfile {
		c := hardwareVideo.Load()
		if c == nil {
			return nil
		}
		var out []diagnostics.ChildProfile
		for _, p := range c.Profile(ctx, kind, d) {
			out = append(out, diagnostics.ChildProfile{Name: p.Name, PID: p.PID, Data: p.Data, Err: p.Err})
		}
		return out
	})
}

// hardwareVideoFor is the client that decodes the saved GIFs on the
// graphics card, nil where the setting is off or the system has no
// decoder.
// It is called on every frame.
func hardwareVideoFor(g preferences.Graphics) *hwvideo.Client {
	if !g.HardwareGIFs || !hwvideo.Available() {
		return nil
	}
	gpu := windowGPU(g)
	hardwareVideoMu.Lock()
	defer hardwareVideoMu.Unlock()
	if c := hardwareVideo.Load(); c != nil && hardwareVideoGPU == gpu {
		return c
	}
	// A client of the GPU the windows drew with before is not closed: its
	// streams end as the GIFs go to the new one (chatmedia's SetHardware),
	// and its helpers stop on their own once they have none.
	c := hwvideo.NewClientFor(gpu.driver, gpu.vendorID, gpu.deviceID)
	hardwareVideo.Store(c)
	hardwareVideoGPU = gpu
	return c
}

var (
	// hardwareVideoMu guards making the client and hardwareVideoGPU, the
	// GPU of the windows hardwareVideo was made for.
	hardwareVideoMu  sync.Mutex
	hardwareVideoGPU decoderGPU
)

// decoderGPU is the GPU the windows draw with, which the helpers decode on
// first: the manifest of its Vulkan driver and its ids on Linux, the ids of
// the Direct3D 11 adapter on Windows, zero for the system's default.
type decoderGPU struct {
	driver             string
	vendorID, deviceID uint32
}

// windowGPU is the GPU the windows draw with as the preferences tell it.
func windowGPU(g preferences.Graphics) decoderGPU {
	if runtime.GOOS == "windows" {
		// The adapter chosen, unless it could not draw and the system's
		// default draws instead.
		if g.D3D11.IsZero() || app.D3D11AdapterFailed() {
			return decoderGPU{}
		}
		return decoderGPU{vendorID: g.D3D11.VendorID, deviceID: g.D3D11.DeviceID}
	}
	e := g.Effective()
	return decoderGPU{driver: e.Driver, vendorID: e.VendorID, deviceID: e.DeviceID}
}

// resetHardwareVideo forgets a client that failed, a helper that ended or
// a machine that had no decoder, for the next one to try again.
func resetHardwareVideo() {
	hardwareVideoMu.Lock()
	defer hardwareVideoMu.Unlock()
	if c := hardwareVideo.Load(); c != nil && c.Failed() != nil {
		c.Close()
		hardwareVideo.CompareAndSwap(c, nil)
	}
}

// layoutAdapters draws the card of the Direct3D 11 adapter: the system's
// default, named as the first adapter listed, and the adapters listed.
func (v *graphicsView) layoutAdapters(gtx layout.Context, l localization.Catalog, height *heightTransition) layout.Dimensions {
	if !v.adaptersDone {
		v.adaptersDone = true
		v.adaptersShown, v.adaptersErr = v.adapters()
	}
	g := v.get()
	shown := v.adaptersShown
	chosen := d3d11AdapterKey(g.D3D11.VendorID, g.D3D11.DeviceID)
	keys := []string{""}
	auto := l.T("graphics.adapter_auto")
	if len(shown) > 0 {
		auto = l.Format("graphics.device_auto_remembered", map[string]string{"name": shown[0].Name})
	}
	labels := map[string]string{"": auto}
	adapters := map[string]preferences.D3D11Adapter{}
	for _, a := range shown {
		key := d3d11AdapterKey(a.VendorID, a.DeviceID)
		if _, ok := labels[key]; ok {
			continue
		}
		keys = append(keys, key)
		labels[key] = deviceLabel(l, a.Name, uint8(a.Kind), a.Version)
		adapters[key] = preferences.D3D11Adapter{VendorID: a.VendorID, DeviceID: a.DeviceID, Name: a.Name, Kind: uint8(a.Kind)}
	}
	if _, ok := labels[chosen]; !ok {
		// An adapter chosen before and not listed now keeps its place:
		// windows are drawn with the default one meanwhile.
		keys = append(keys, chosen)
		labels[chosen] = l.Format("graphics.device_missing", map[string]string{"name": g.D3D11.Name})
	}
	if v.adapter == nil || !slices.Equal(v.adapterKeys, keys) {
		v.adapterKeys = keys
		v.adapter = radio.NewRadios(keys, chosen, func(key string) {
			if a, ok := adapters[key]; ok || key == "" {
				v.chooseAdapter(a)
			}
		})
	}
	if v.adapter.GetValue() != chosen {
		v.adapter.SetValue(chosen)
	}
	v.adapter.Update(gtx)
	notes := adapterNotes(l, g, shown, v.adaptersErr, !g.D3D11.IsZero() && v.adapterFailed())
	return settingsChoiceCard(gtx, height, l.T("graphics.adapter"), strings.Join(notes, "\n"), func(gtx layout.Context) layout.Dimensions {
		return v.adapter.Layout(gtx, radio.LeadingKind, labels)
	})
}

// adapterNotes is what the card of the adapter tells under it: the error of
// the listing, that the adapter chosen failed and the default one draws
// instead, and that a start takes longer while a discrete GPU that draws
// sleeps.
func adapterNotes(l localization.Catalog, g preferences.Graphics, shown []app.D3D11Adapter, listErr error, failed bool) []string {
	var notes []string
	if listErr != nil {
		notes = append(notes, l.Format("graphics.device_error", map[string]string{"error": listErr.Error()}))
	}
	if failed {
		notes = append(notes, l.T("graphics.adapter_failed"))
		g.D3D11 = preferences.D3D11Adapter{}
	}
	if drawsOnDiscreteAdapter(g, shown) {
		notes = append(notes, l.T("graphics.discrete_note"))
	}
	return notes
}

// drawsOnDiscreteAdapter reports whether Direct3D 11 draws on a discrete
// GPU: the one chosen, as the adapters shown tell it, else as it was kept;
// with none chosen, the system's default, the first shown.
func drawsOnDiscreteAdapter(g preferences.Graphics, shown []app.D3D11Adapter) bool {
	if g.D3D11.IsZero() {
		return len(shown) > 0 && shown[0].Kind == app.VulkanDeviceDiscrete
	}
	for _, a := range shown {
		if a.VendorID == g.D3D11.VendorID && a.DeviceID == g.D3D11.DeviceID {
			return a.Kind == app.VulkanDeviceDiscrete
		}
	}
	return app.VulkanDeviceKind(g.D3D11.Kind) == app.VulkanDeviceDiscrete
}

// chooseAdapter keeps the Direct3D 11 adapter chosen; the zero adapter is
// the system's default.
func (v *graphicsView) chooseAdapter(a preferences.D3D11Adapter) {
	g := v.get()
	g.D3D11 = a
	v.set(g)
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
	var notes []string
	switch {
	case !done && len(shown) == 0:
		notes = append(notes, l.T("graphics.device_listing"))
	case done && listErr != nil:
		notes = append(notes, l.Format("graphics.device_error", map[string]string{"error": listErr.Error()}))
	}
	if drawsOnDiscrete(g, shown) {
		notes = append(notes, l.T("graphics.discrete_note"))
	}
	return settingsChoiceCard(gtx, height, l.T("graphics.device"), strings.Join(notes, "\n"), func(gtx layout.Context) layout.Dimensions {
		return v.device.Layout(gtx, radio.LeadingKind, labels)
	})
}

// drawsOnDiscrete reports whether Vulkan draws on a discrete GPU, chosen
// or remembered by the automatic choice: its kind as the devices shown
// tell it, else as it was kept.
func drawsOnDiscrete(g preferences.Graphics, shown []preferences.VulkanDevice) bool {
	d := g.Effective()
	if d.IsZero() {
		return false
	}
	for _, s := range shown {
		if s.Same(d) {
			return app.VulkanDeviceKind(s.Kind) == app.VulkanDeviceDiscrete
		}
	}
	return app.VulkanDeviceKind(d.Kind) == app.VulkanDeviceDiscrete
}

// chooseAPI keeps the graphics API chosen. A choice tries Vulkan again
// where it failed.
func (v *graphicsView) chooseAPI(api string) {
	g := v.get()
	g.API = api
	g.VulkanFailed = false
	v.set(g)
}

// chooseDevice keeps the Vulkan device chosen; the zero device is the
// automatic choice. A choice tries Vulkan again where it failed.
func (v *graphicsView) chooseDevice(d preferences.VulkanDevice) {
	g := v.get()
	g.Vulkan = preferences.VulkanDevice{Driver: d.Driver, VendorID: d.VendorID, DeviceID: d.DeviceID, Name: d.Name, Kind: d.Kind}
	g.VulkanFailed = false
	v.set(g)
}

// vulkanDeviceLabel names a device with its kind and its driver's version.
func vulkanDeviceLabel(d preferences.VulkanDevice, l localization.Catalog) string {
	return deviceLabel(l, d.Name, d.Kind, d.Version)
}

// deviceLabel names a GPU with its kind, Gio's app.VulkanDeviceKind, and
// its driver's version.
func deviceLabel(l localization.Catalog, name string, kind uint8, version string) string {
	kindName := map[app.VulkanDeviceKind]string{
		app.VulkanDeviceIntegrated: l.T("graphics.kind_integrated"),
		app.VulkanDeviceDiscrete:   l.T("graphics.kind_discrete"),
		app.VulkanDeviceVirtual:    l.T("graphics.kind_virtual"),
	}[app.VulkanDeviceKind(kind)]
	label := name
	if kindName != "" {
		label += " · " + kindName
	}
	if version != "" {
		label += " · " + l.Format("graphics.driver_version", map[string]string{"version": version})
	}
	return label
}

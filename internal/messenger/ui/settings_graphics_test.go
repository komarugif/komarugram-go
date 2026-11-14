// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"runtime"
	"slices"
	"strings"
	"sync"
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
	"komarugram/pkg/hwvideo"
)

var (
	testIntel  = app.VulkanDevice{Driver: "intel_icd.x86_64.json", VendorID: 0x8086, DeviceID: 0x4626, Name: "Intel(R) Graphics (ADL GT2)", Kind: app.VulkanDeviceIntegrated, API: "Vulkan 1.4.354", Version: "26.1.6"}
	testNVIDIA = app.VulkanDevice{Driver: "nvidia_icd.json", VendorID: 0x10de, DeviceID: 0x25a2, Name: "NVIDIA GeForce RTX 3050 Laptop GPU", Kind: app.VulkanDeviceDiscrete, API: "Vulkan 1.4.312", Version: "610.43.3"}
)

var (
	testD3D11Intel  = app.D3D11Adapter{VendorID: 0x8086, DeviceID: 0x46a6, Name: "Intel(R) Iris(R) Xe Graphics", Kind: app.VulkanDeviceIntegrated, Version: "32.0.101.6979"}
	testD3D11NVIDIA = app.D3D11Adapter{VendorID: 0x10de, DeviceID: 0x25a5, Name: "NVIDIA GeForce RTX 3050 Laptop GPU", Kind: app.VulkanDeviceDiscrete, Version: "32.0.15.6109"}
)

// newTestAdapterView is a section of hardware acceleration on Windows with
// two GPUs, Intel's the default, keeping the setting in g and counting the
// listings of adapters.
func newTestAdapterView(g *preferences.Graphics, listings *atomic.Int32) *graphicsView {
	v := newGraphicsView(func() {})
	v.info = func() gpu.DeviceInfo {
		return gpu.DeviceInfo{API: "Direct3D 11", Device: "Intel(R) Iris(R) Xe Graphics (8086:46a6)", Driver: "32.0.101.6979"}
	}
	v.choice = func() bool { return false }
	v.devices = func() ([]app.VulkanDevice, error) {
		panic("listed the Vulkan devices on Windows")
	}
	v.adapterChoice = func() bool { return true }
	v.adapters = func() ([]app.D3D11Adapter, error) {
		listings.Add(1)
		return []app.D3D11Adapter{testD3D11Intel, testD3D11NVIDIA}, nil
	}
	v.adapterFailed = func() bool { return false }
	v.get = func() preferences.Graphics { return *g }
	v.set = func(n preferences.Graphics) { *g = n }
	return v
}

// On Windows the section offers the Direct3D 11 adapters, listed once, and
// no graphics API; the adapter chosen is kept, and makes the windows' GPUs
// anew.
func TestGraphicsD3D11Adapters(t *testing.T) {
	l := localization.For("ru")
	var g preferences.Graphics
	var listings atomic.Int32
	v := newTestAdapterView(&g, &listings)
	for range 3 {
		layoutGraphics(v, l)
	}
	if v.api != nil || v.device != nil {
		t.Fatal("offered the graphics API or a Vulkan device on Windows")
	}
	if n := listings.Load(); n != 1 {
		t.Fatalf("listed the adapters %d times", n)
	}
	intelKey := d3d11AdapterKey(testD3D11Intel.VendorID, testD3D11Intel.DeviceID)
	nvidiaKey := d3d11AdapterKey(testD3D11NVIDIA.VendorID, testD3D11NVIDIA.DeviceID)
	if want := []string{"", intelKey, nvidiaKey}; !slices.Equal(v.adapterKeys, want) {
		t.Fatalf("adapters offered %q, want %q", v.adapterKeys, want)
	}
	if v.adapter.GetValue() != "" {
		t.Fatalf("shown chosen %q, not the default", v.adapter.GetValue())
	}
	before := g
	v.chooseAdapter(preferences.D3D11Adapter{VendorID: testD3D11NVIDIA.VendorID, DeviceID: testD3D11NVIDIA.DeviceID, Name: testD3D11NVIDIA.Name, Kind: uint8(testD3D11NVIDIA.Kind)})
	if g.D3D11.DeviceID != testD3D11NVIDIA.DeviceID || g.API != preferences.GraphicsAuto {
		t.Fatalf("kept %+v", g)
	}
	if !graphicsChanged(before, g) {
		t.Fatal("another adapter does not make the windows' GPUs anew")
	}
	layoutGraphics(v, l)
	if got := v.adapter.GetValue(); got != nvidiaKey {
		t.Fatalf("shown chosen %q", got)
	}
	if want := "Intel(R) Iris(R) Xe Graphics · встроенная · драйвер 32.0.101.6979"; deviceLabel(l, testD3D11Intel.Name, uint8(testD3D11Intel.Kind), testD3D11Intel.Version) != want {
		t.Fatalf("label %q", deviceLabel(l, testD3D11Intel.Name, uint8(testD3D11Intel.Kind), testD3D11Intel.Version))
	}
}

// An adapter chosen and gone, as an external GPU unplugged, keeps its place
// as not found.
func TestGraphicsD3D11AdapterMissing(t *testing.T) {
	l := localization.For("en")
	amd := preferences.D3D11Adapter{VendorID: 0x1002, DeviceID: 0x73df, Name: "AMD Radeon RX 6700 XT", Kind: uint8(app.VulkanDeviceOther)}
	g := preferences.Graphics{D3D11: amd}
	v := newTestAdapterView(&g, new(atomic.Int32))
	layoutGraphics(v, l)
	key := d3d11AdapterKey(amd.VendorID, amd.DeviceID)
	if len(v.adapterKeys) != 4 || v.adapterKeys[3] != key || v.adapter.GetValue() != key {
		t.Fatalf("offered %q, chosen %q", v.adapterKeys, v.adapter.GetValue())
	}
}

// The card of the adapter tells that a start takes longer while a discrete
// GPU sleeps, when it draws: chosen, or the system's default.
func TestDrawsOnDiscreteAdapter(t *testing.T) {
	shown := []app.D3D11Adapter{testD3D11Intel, testD3D11NVIDIA}
	nvidia := preferences.D3D11Adapter{VendorID: testD3D11NVIDIA.VendorID, DeviceID: testD3D11NVIDIA.DeviceID}
	intel := preferences.D3D11Adapter{VendorID: testD3D11Intel.VendorID, DeviceID: testD3D11Intel.DeviceID}
	for _, c := range []struct {
		name  string
		g     preferences.Graphics
		shown []app.D3D11Adapter
		want  bool
	}{
		{"nvidia chosen", preferences.Graphics{D3D11: nvidia}, shown, true},
		{"intel chosen", preferences.Graphics{D3D11: intel}, shown, false},
		{"intel the default", preferences.Graphics{}, shown, false},
		{"nvidia the default", preferences.Graphics{}, []app.D3D11Adapter{testD3D11NVIDIA, testD3D11Intel}, true},
		{"nvidia kept, not listed", preferences.Graphics{D3D11: preferences.D3D11Adapter{VendorID: 0x10de, DeviceID: 0x25a5, Kind: uint8(app.VulkanDeviceDiscrete)}}, nil, true},
	} {
		if got := drawsOnDiscreteAdapter(c.g, c.shown); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}

// Where the adapter chosen could not draw, the card tells that the default
// one draws, and the note of a discrete GPU asleep follows the default.
func TestAdapterNotesFailed(t *testing.T) {
	l := localization.For("en")
	nvidia := preferences.Graphics{D3D11: preferences.D3D11Adapter{VendorID: testD3D11NVIDIA.VendorID, DeviceID: testD3D11NVIDIA.DeviceID}}
	shown := []app.D3D11Adapter{testD3D11Intel, testD3D11NVIDIA}
	if got := adapterNotes(l, nvidia, shown, nil, false); !slices.Equal(got, []string{l.T("graphics.discrete_note")}) {
		t.Fatalf("NVIDIA drawing: %q", got)
	}
	if got := adapterNotes(l, nvidia, shown, nil, true); !slices.Equal(got, []string{l.T("graphics.adapter_failed")}) {
		t.Fatalf("NVIDIA failed, Intel the default drawing: %q", got)
	}
	if !strings.Contains(localization.For("ru").T("graphics.adapter_failed"), "по умолчанию") {
		t.Fatal("the note is not in the Russian language file")
	}
}

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
	v.adapterChoice = func() bool { return false }
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
	v.chooseDevice(toVulkanDevice(testIntel))
	want := preferences.VulkanDevice{Driver: "intel_icd.x86_64.json", VendorID: 0x8086, DeviceID: 0x4626, Name: "Intel(R) Graphics (ADL GT2)", Kind: uint8(app.VulkanDeviceIntegrated)}
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
	ApplyGraphics(preferences.Graphics{D3D11: preferences.D3D11Adapter{VendorID: 0x10de, DeviceID: 0x25a5, Name: "NVIDIA", Kind: uint8(app.VulkanDeviceDiscrete)}})
	got = app.CurrentGraphics()
	want = app.Graphics{D3D11: app.D3D11Adapter{VendorID: 0x10de, DeviceID: 0x25a5, Name: "NVIDIA"}}
	if got != want {
		t.Fatalf("Gio has %+v, want %+v", got, want)
	}
}

// The windows make their GPU anew when the API changes, or the Vulkan
// device while Vulkan draws them; not for a device chosen under OpenGL.
func TestGraphicsChanged(t *testing.T) {
	intel := preferences.VulkanDevice{Driver: "intel_icd.x86_64.json", VendorID: 0x8086, DeviceID: 0x4626, Name: "Intel"}
	nvidia := preferences.VulkanDevice{Driver: "nvidia_icd.json", VendorID: 0x10de, DeviceID: 0x25a2, Name: "NVIDIA"}
	renamed := intel
	renamed.Name = "Intel(R) Graphics"
	for _, c := range []struct {
		before, after preferences.Graphics
		want          bool
	}{
		{preferences.Graphics{}, preferences.Graphics{API: preferences.GraphicsVulkan}, true},
		{preferences.Graphics{API: preferences.GraphicsVulkan}, preferences.Graphics{API: preferences.GraphicsOpenGL}, true},
		{preferences.Graphics{API: preferences.GraphicsVulkan, Vulkan: intel}, preferences.Graphics{API: preferences.GraphicsVulkan, Vulkan: nvidia}, true},
		{preferences.Graphics{API: preferences.GraphicsVulkan, Vulkan: intel}, preferences.Graphics{API: preferences.GraphicsVulkan}, true},
		{preferences.Graphics{API: preferences.GraphicsVulkan, Vulkan: intel}, preferences.Graphics{API: preferences.GraphicsVulkan, Vulkan: renamed}, false},
		{preferences.Graphics{API: preferences.GraphicsOpenGL, Vulkan: intel}, preferences.Graphics{API: preferences.GraphicsOpenGL, Vulkan: nvidia}, false},
		// The automatic choice draws with Vulkan.
		{preferences.Graphics{Auto: intel}, preferences.Graphics{Auto: nvidia}, true},
		{preferences.Graphics{API: preferences.GraphicsVulkan}, preferences.Graphics{API: preferences.GraphicsVulkan}, false},
	} {
		if got := graphicsChanged(c.before, c.after); got != c.want {
			t.Errorf("graphicsChanged(%+v, %+v) = %v", c.before, c.after, got)
		}
	}
}

// The devices known from the last listing are offered at once; a listing
// anew replaces them once it is done, and is kept.
func TestGraphicsShowsKnownDevicesAtOnce(t *testing.T) {
	l := localization.For("en")
	g := preferences.Graphics{API: preferences.GraphicsVulkan, Known: []preferences.VulkanDevice{toVulkanDevice(testIntel), toVulkanDevice(testNVIDIA)}}
	var listings atomic.Int32
	v := newTestGraphicsView(&g, &listings)
	release := make(chan struct{})
	amd := app.VulkanDevice{Driver: "radeon_icd.x86_64.json", VendorID: 0x1002, DeviceID: 0x73df, Name: "AMD Radeon RX 6700 XT", Kind: app.VulkanDeviceDiscrete}
	v.devices = func() ([]app.VulkanDevice, error) {
		<-release
		return []app.VulkanDevice{testIntel, testNVIDIA, amd}, nil
	}
	layoutGraphics(v, l)
	if len(v.deviceKeys) != 3 {
		t.Fatalf("offered %q before the listing", v.deviceKeys)
	}
	close(release)
	for range 100 {
		layoutGraphics(v, l)
		if len(v.deviceKeys) == 4 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if len(v.deviceKeys) != 4 || len(g.Known) != 3 || g.Known[2].Driver != amd.Driver {
		t.Fatalf("after the listing: offered %q, known %+v", v.deviceKeys, g.Known)
	}
}

// memoryStore keeps the setting for rememberVulkan.
type memoryStore struct {
	mu sync.Mutex
	g  preferences.Graphics
}

func (s *memoryStore) Global() preferences.Global {
	s.mu.Lock()
	defer s.mu.Unlock()
	return preferences.Global{Graphics: s.g}
}

func (s *memoryStore) SetGraphics(g preferences.Graphics) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.g = g
	return nil
}

// The device the automatic choice drew with is remembered, and the windows
// make their GPU anew with its driver alone: from the devices known, or
// listed first where it is not among them. A device chosen is not replaced.
func TestRememberVulkan(t *testing.T) {
	defer app.SetGraphics(app.Graphics{})
	intelInfo := gpu.DeviceInfo{API: "Vulkan 1.4.354", Device: "Intel(R) Graphics (ADL GT2) (8086:4626)", Driver: "26.1.6", VendorID: 0x8086, DeviceID: 0x4626}
	var resets atomic.Int32
	reset := func() { resets.Add(1) }
	noListing := func() ([]app.VulkanDevice, error) { t.Fatal("listed the devices"); return nil, nil }

	store := &memoryStore{g: preferences.Graphics{API: preferences.GraphicsVulkan, Known: []preferences.VulkanDevice{toVulkanDevice(testNVIDIA), toVulkanDevice(testIntel)}}}
	rememberVulkan(store, intelInfo, "", noListing, reset)
	if g := store.Global().Graphics; g.Auto.Driver != testIntel.Driver || resets.Load() != 1 {
		t.Fatalf("remembered %+v, %d resets", g.Auto, resets.Load())
	}
	if got := app.CurrentGraphics().Vulkan; got.Driver != testIntel.Driver {
		t.Fatalf("Gio draws with %+v", got)
	}
	rememberVulkan(store, intelInfo, "", noListing, reset)
	if resets.Load() != 1 {
		t.Fatal("the device remembered was remembered again")
	}

	store = &memoryStore{g: preferences.Graphics{API: preferences.GraphicsVulkan}}
	listed := make(chan struct{})
	rememberVulkan(store, intelInfo, "", func() ([]app.VulkanDevice, error) {
		defer close(listed)
		return []app.VulkanDevice{testNVIDIA, testIntel}, nil
	}, reset)
	<-listed
	for range 100 {
		if store.Global().Graphics.Auto.Driver != "" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if g := store.Global().Graphics; g.Auto.Driver != testIntel.Driver || len(g.Known) != 2 || resets.Load() != 2 {
		t.Fatalf("after listing: auto %+v, known %d, %d resets", g.Auto, len(g.Known), resets.Load())
	}

	chosen := toVulkanDevice(testNVIDIA)
	store = &memoryStore{g: preferences.Graphics{API: preferences.GraphicsVulkan, Vulkan: chosen, Known: []preferences.VulkanDevice{toVulkanDevice(testIntel)}}}
	rememberVulkan(store, intelInfo, "", noListing, reset)
	if g := store.Global().Graphics; !g.Auto.IsZero() || resets.Load() != 2 {
		t.Fatalf("with a device chosen, remembered %+v", g.Auto)
	}
}

// On the first start Gio draws with the compositor's driver alone: its
// device is remembered without listing the devices, which would wake every
// GPU, and the windows keep their GPU.
func TestRememberVulkanCompositorDriver(t *testing.T) {
	defer app.SetGraphics(app.Graphics{})
	intelInfo := gpu.DeviceInfo{API: "Vulkan 1.4.354", Device: "Intel(R) Graphics (ADL GT2) (8086:4626)", Driver: "26.1.6", VendorID: 0x8086, DeviceID: 0x4626}
	noListing := func() ([]app.VulkanDevice, error) { t.Fatal("listed the devices"); return nil, nil }
	store := &memoryStore{g: preferences.Graphics{}}
	rememberVulkan(store, intelInfo, testIntel.Driver, noListing, func() { t.Fatal("the windows made their GPU anew") })
	auto := store.Global().Graphics.Auto
	if auto.Driver != testIntel.Driver || auto.DeviceID != 0x4626 || auto.Name != "Intel(R) Graphics (ADL GT2)" {
		t.Fatalf("remembered %+v", auto)
	}
	if got := app.CurrentGraphics().Vulkan; got.Driver != testIntel.Driver {
		t.Fatalf("Gio draws with %+v", got)
	}
}

// The automatic choice draws with the device remembered: choosing it
// explicitly changes nothing, and remembering another changes the device.
func TestGraphicsChangedWithAuto(t *testing.T) {
	intel, nvidia := toVulkanDevice(testIntel), toVulkanDevice(testNVIDIA)
	auto := preferences.Graphics{API: preferences.GraphicsVulkan, Auto: intel}
	if graphicsChanged(auto, preferences.Graphics{API: preferences.GraphicsVulkan, Vulkan: intel, Auto: intel}) {
		t.Error("choosing the device remembered changed the device")
	}
	if !graphicsChanged(auto, preferences.Graphics{API: preferences.GraphicsVulkan, Vulkan: nvidia, Auto: intel}) {
		t.Error("choosing another device changed nothing")
	}
}

// The card of the device tells that a start takes longer while a discrete
// GPU sleeps, when it draws: chosen, or remembered by the automatic choice.
func TestDrawsOnDiscrete(t *testing.T) {
	intel, nvidia := toVulkanDevice(testIntel), toVulkanDevice(testNVIDIA)
	bare := nvidia
	bare.Kind = 0
	known := []preferences.VulkanDevice{intel, nvidia}
	for _, c := range []struct {
		name string
		g    preferences.Graphics
		want bool
	}{
		{"nvidia chosen", preferences.Graphics{Vulkan: nvidia}, true},
		{"nvidia remembered", preferences.Graphics{Auto: nvidia}, true},
		{"nvidia kept without its kind", preferences.Graphics{Vulkan: bare}, true},
		{"intel chosen", preferences.Graphics{Vulkan: intel, Auto: nvidia}, false},
		{"nothing remembered", preferences.Graphics{}, false},
	} {
		if got := drawsOnDiscrete(c.g, known); got != c.want {
			t.Errorf("%s: %v", c.name, got)
		}
	}
	l := localization.For("ru")
	g := preferences.Graphics{API: preferences.GraphicsVulkan, Vulkan: nvidia, Known: known}
	var listings atomic.Int32
	v := newTestGraphicsView(&g, &listings)
	v.devices = func() ([]app.VulkanDevice, error) { select {} }
	layoutGraphics(v, l)
	if !strings.Contains(l.T("graphics.discrete_note"), "дискретная") {
		t.Fatal("the note is not in the language file")
	}
}

// That Vulkan could not draw a window is kept and handed to Gio at the
// next start, which then draws with OpenGL at once; a choice made in the
// settings tries Vulkan again.
func TestRememberVulkanFailed(t *testing.T) {
	defer app.SetGraphics(app.Graphics{})
	store := &memoryStore{g: preferences.Graphics{API: preferences.GraphicsVulkan}}
	rememberVulkanFailed(store, false)
	if store.Global().Graphics.VulkanFailed {
		t.Fatal("kept a failure Gio did not tell")
	}
	rememberVulkanFailed(store, true)
	g := store.Global().Graphics
	if !g.VulkanFailed {
		t.Fatal("the failure was not kept")
	}
	ApplyGraphics(g)
	if !app.CurrentGraphics().VulkanFailed {
		t.Fatal("the failure kept was not handed to Gio")
	}
	v := newTestGraphicsView(&g, new(atomic.Int32))
	v.chooseAPI(preferences.GraphicsAuto)
	if g.VulkanFailed {
		t.Fatal("choosing the API did not try Vulkan again")
	}
	g.VulkanFailed = true
	v.chooseDevice(toVulkanDevice(testIntel))
	if g.VulkanFailed {
		t.Fatal("choosing a device did not try Vulkan again")
	}
}

// The automatic choice of the API tells what it chose, as the GPU made
// last for a window tells it, while it is the choice.
func TestAutoAPILabel(t *testing.T) {
	l := localization.For("en")
	vulkan := gpu.DeviceInfo{API: "Vulkan 1.4.354"}
	gl := gpu.DeviceInfo{API: "OpenGL ES 3.2"}
	for _, c := range []struct {
		api  string
		info gpu.DeviceInfo
		want string
	}{
		{preferences.GraphicsAuto, vulkan, "Automatic (Vulkan)"},
		{preferences.GraphicsAuto, gl, "Automatic (OpenGL)"},
		{preferences.GraphicsAuto, gpu.DeviceInfo{}, "Automatic"},
		{preferences.GraphicsOpenGL, vulkan, "Automatic"},
	} {
		if got := autoAPILabel(l, preferences.Graphics{API: c.api}, c.info); got != c.want {
			t.Errorf("chosen %q, drawn with %q: %q, want %q", c.api, c.info.API, got, c.want)
		}
	}
}

// TestHardwareVideoFollowsWindowGPU checks the decoder's client is made
// anew, for the windows' GPU first, when the windows move to another GPU,
// and kept while they do not.
func TestHardwareVideoFollowsWindowGPU(t *testing.T) {
	if !hwvideo.Available() || runtime.GOOS != "linux" {
		t.Skip("the GPU is a Vulkan device on Linux")
	}
	defer func() {
		if c := hardwareVideo.Swap(nil); c != nil {
			c.Close()
		}
		hardwareVideoGPU = decoderGPU{}
	}()
	intel := preferences.VulkanDevice{Driver: "intel_icd.x86_64.json", VendorID: 0x8086, DeviceID: 0x4626}
	nvidia := preferences.VulkanDevice{Driver: "nvidia_icd.json", VendorID: 0x10de, DeviceID: 0x25a5}
	g := preferences.Graphics{HardwareGIFs: true, Vulkan: intel}
	first := hardwareVideoFor(g)
	if first == nil || hardwareVideoFor(g) != first {
		t.Fatal("the client not kept for the same GPU")
	}
	g.Vulkan = nvidia
	second := hardwareVideoFor(g)
	if second == first {
		t.Fatal("the client kept when the windows moved to another GPU")
	}
	if hardwareVideoGPU.vendorID != 0x10de {
		t.Fatalf("made for %+v", hardwareVideoGPU)
	}
	g.HardwareGIFs = false
	if hardwareVideoFor(g) != nil {
		t.Fatal("a client with the setting off")
	}
	first.Close()
}

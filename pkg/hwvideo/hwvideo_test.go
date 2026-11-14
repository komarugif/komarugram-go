// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import (
	"context"
	"errors"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"komarugram/pkg/mp4"
	"komarugram/pkg/program"
	"komarugram/pkg/resample"
)

// helperEnv makes the test binary a helper: "fake" decodes on a device
// that decodes nothing, "die" ends at the first sample, as a driver that
// falls does, "real" decodes on the GPU.
const helperEnv = "KOMARUGRAM_HWVIDEO_TEST_HELPER"

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "":
		os.Exit(m.Run())
	case "real":
		_ = Serve(os.Stdin, os.Stdout)
	case "fake":
		_ = serve(os.Stdin, os.Stdout, func() (device, error) { return new(fakeDevice), nil })
	case "die":
		_ = serve(os.Stdin, os.Stdout, func() (device, error) { return &fakeDevice{die: true}, nil })
	case "none":
		_ = serve(os.Stdin, os.Stdout, func() (device, error) { return nil, unsupported("no GPU") })
	case "refuse":
		_ = serve(os.Stdin, os.Stdout, func() (device, error) { return &fakeDevice{refuse: true}, nil })
	case "gpu":
		_ = serve(os.Stdin, os.Stdout, func() (device, error) { return &fakeDevice{renders: true}, nil })
	}
	os.Exit(0)
}

// testClient is a client whose helpers are the test binary in modes, a
// tier each.
func testClient(t testing.TB, modes ...string) *Client {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{}
	for _, mode := range modes {
		c.tiers = append(c.tiers, &tier{command: func() *exec.Cmd {
			cmd := program.Command(self)
			cmd.Env = append(os.Environ(), helperEnv+"="+mode)
			return cmd
		}})
	}
	t.Cleanup(c.Close)
	return c
}

// fakeDevice decodes nothing: it keeps the pictures it was handed, and
// reads each surface as a picture whose first sample is the number, in
// decoding order, of the picture decoded into it.
type fakeDevice struct {
	die bool
	// refuse refuses every stream, as a device that decodes fewer
	// references than the stream keeps.
	refuse   bool
	pictures []*picture
	surfaces map[int]int
	// renders scales pictures "on the GPU": each pixel the number of the
	// picture, the blocks of the plan and 0xa5, an alpha the CPU never
	// gives; otherwise it does not, and they are scaled on the CPU.
	renders bool
}

func (d *fakeDevice) open(sps *SPS, surfaces int) (session, error) {
	if d.refuse {
		return nil, unsupported("%d surfaces", surfaces)
	}
	if d.surfaces == nil {
		d.surfaces = map[int]int{}
	}
	return d, nil
}

func (d *fakeDevice) close() {}

func (d *fakeDevice) reset() {}

func (d *fakeDevice) decode(p *picture) error {
	if d.die {
		os.Exit(3)
	}
	d.surfaces[p.surface] = len(d.pictures)
	d.pictures = append(d.pictures, p)
	return nil
}

func (d *fakeDevice) read(surface int, sps *SPS, dst *image.YCbCr) error {
	sizeYCbCr(dst, sps.Width(), sps.Height())
	clear(dst.Y)
	dst.Y[0], dst.Y[1] = byte(d.surfaces[surface]), byte(d.surfaces[surface]>>8)
	return nil
}

func (d *fakeDevice) render(surface int, sps *SPS, plan resample.Plan, dst *image.RGBA) error {
	if !d.renders {
		return noRender("a fake device")
	}
	n := d.surfaces[surface]
	for i := 0; i+3 < len(dst.Pix); i += 4 {
		dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = byte(n), byte(plan.BlockX), byte(plan.BlockY), 0xa5
	}
	return nil
}

func testFile(t testing.TB) *mp4.File {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "assets", "video.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	file, err := mp4.Demux(data)
	if err != nil {
		t.Fatal(err)
	}
	return file
}

// TestPicturesComeOutInOrder decodes a video with pictures predicted from
// later ones on a device that decodes nothing, and checks what is the
// decoder's own: every picture comes out once, in the order they show,
// each decoded from references that are kept, into a surface nothing else
// still needs.
func TestPicturesComeOutInOrder(t *testing.T) {
	file := testFile(t)
	dev := new(fakeDevice)
	d, err := newDecoder(dev, file.Config)
	if err != nil {
		t.Fatal(err)
	}
	if got := d.Size(); got != image.Pt(file.Width, file.Height) {
		t.Fatalf("pictures of %v, the container tells %dx%d", got, file.Width, file.Height)
	}
	var im image.YCbCr
	var order []int
	reordered := false
	take := func() {
		for d.Receive() {
			if err := d.Read(&im); err != nil {
				t.Fatal(err)
			}
			n := int(im.Y[0]) | int(im.Y[1])<<8
			if len(order) > 0 && n < order[len(order)-1] {
				reordered = true
			}
			order = append(order, n)
		}
	}
	for i := range file.Len() {
		sample, _ := file.Sample(i, nil)
		if err := d.Send(sample); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
		take()
	}
	d.End()
	take()
	if len(order) != file.Len() {
		t.Fatalf("%d pictures out of %d samples", len(order), file.Len())
	}
	if !reordered {
		t.Fatal("the video has no pictures shown out of the order they are decoded in: it tests nothing")
	}
	// In the order they show: by picture order count, counted anew from
	// each IDR picture.
	seen := map[int]bool{}
	period, last := 0, int32(0)
	idrs := 0
	for _, p := range dev.pictures {
		if p.idr {
			idrs++
		}
	}
	periods := make([]int, len(dev.pictures))
	n := -1
	for i, p := range dev.pictures {
		if p.idr {
			n++
		}
		periods[i] = n
	}
	for i, n := range order {
		if seen[n] {
			t.Fatalf("picture %d came out twice", n)
		}
		seen[n] = true
		p := dev.pictures[n]
		poc := min(p.top, p.bottom)
		if i > 0 && (periods[n] < period || periods[n] == period && poc <= last) {
			t.Fatalf("picture %d (order count %d, after IDR %d) came out after one of order count %d, after IDR %d", n, poc, periods[n], last, period)
		}
		period, last = periods[n], poc
	}
	// Surfaces: a picture is never decoded over one of its references.
	for i, p := range dev.pictures {
		if !p.intra && len(p.refs) == 0 {
			t.Fatalf("picture %d has no references", i)
		}
		for _, r := range p.refs {
			if r.surface == p.surface {
				t.Fatalf("picture %d is decoded over its reference in surface %d", i, r.surface)
			}
		}
		if len(p.refs) > int(p.sps.MaxRefFrames) {
			t.Fatalf("picture %d has %d references of %d", i, len(p.refs), p.sps.MaxRefFrames)
		}
	}
}

// TestRefusesWhatHardwareDoesNotDecode checks streams the decoder leaves to
// software are refused as unsupported, not as broken.
func TestRefusesWhatHardwareDoesNotDecode(t *testing.T) {
	file := testFile(t)
	c, err := parseAVCConfig(file.Config)
	if err != nil {
		t.Fatal(err)
	}
	sps, err := parseSPS(c.sps[0])
	if err != nil {
		t.Fatal(err)
	}
	if sps.Profile != 100 || !sps.FrameMbsOnly {
		t.Fatalf("the video is of profile %d, frames only: %v", sps.Profile, sps.FrameMbsOnly)
	}
	// The same set with another profile: High 10.
	other := append([]byte(nil), c.sps[0]...)
	other[1] = 110
	config := append([]byte(nil), file.Config...)
	// The first sequence parameter set starts after 6 bytes of header and
	// 2 of its length.
	copy(config[8:], other)
	_, err = newDecoder(new(fakeDevice), config)
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("a stream of 10 bits: %v", err)
	}
	// A sample cut short is broken, and never a panic.
	d, err := newDecoder(new(fakeDevice), file.Config)
	if err != nil {
		t.Fatal(err)
	}
	sample, _ := file.Sample(0, nil)
	if err := d.Send(sample[:len(sample)/2]); err == nil || errors.Is(err, ErrUnsupported) {
		t.Fatalf("half a sample: %v", err)
	}
}

// TestHelperPlaysLoop plays a video through a helper: every picture of a
// loop, the loop again, and from a keyframe sought.
func TestHelperPlaysLoop(t *testing.T) {
	file := testFile(t)
	c := testClient(t, "fake")
	v, err := c.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for round := range 2 {
		for want := range v.Pictures() {
			i, err := v.Next(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if i != want {
				t.Fatalf("round %d: picture %d, want %d", round, i, want)
			}
			if want%100 == 0 {
				if im := v.Picture(image.Pt(36, 64)); im == nil {
					t.Fatalf("picture %d: %v", i, v.Err())
				}
			}
		}
	}
	k, err := v.Seek(ctx, v.Time(v.Pictures()/2))
	if err != nil {
		t.Fatal(err)
	}
	if k == 0 || !file.Keyframe(k) {
		t.Fatalf("sought to %d", k)
	}
	if i, err := v.Next(ctx); err != nil || i != k {
		t.Fatalf("after the seek: picture %d, %v; want %d", i, err, k)
	}
	v.Close(ctx)
	if err := c.Failed(); err != nil {
		t.Fatal(err)
	}
}

// TestHelperThatDiesFailsTheClient ends the helper as a fault in a driver
// does, in the middle of a video: the video fails with ErrHelper, and the
// client opens none after it, for them to be decoded in software.
func TestHelperThatDiesFailsTheClient(t *testing.T) {
	file := testFile(t)
	c := testClient(t, "die")
	v, err := c.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := v.Next(context.Background()); !errors.Is(err, ErrHelper) {
		t.Fatalf("a video of a helper that died: %v", err)
	}
	v.Close(context.Background())
	if c.Failed() == nil {
		t.Fatal("the client did not fail")
	}
	if _, err := c.Open(file); !errors.Is(err, ErrHelper) {
		t.Fatalf("a video opened after the helper died: %v", err)
	}
}

// TestNoDeviceFailsTheClient checks a machine without a decoder starts no
// helper again.
func TestNoDeviceFailsTheClient(t *testing.T) {
	file := testFile(t)
	c := testClient(t, "none")
	if _, err := c.Open(file); err == nil {
		t.Fatal("a video opened without a device")
	}
	if c.Failed() == nil {
		t.Fatal("the client did not fail")
	}
	c.mu.Lock()
	running := c.tiers[0].helper != nil
	c.mu.Unlock()
	if running {
		t.Fatal("the helper still runs")
	}
}

// TestTiersFallBack checks a video goes to the next GPU where the first
// has no decoder or refuses the stream, that a GPU without a decoder is
// tried no more, and that one that refused a stream still decodes others.
func TestTiersFallBack(t *testing.T) {
	file := testFile(t)
	for _, first := range []string{"none", "refuse"} {
		t.Run(first, func(t *testing.T) {
			c := testClient(t, first, "fake")
			for range 2 {
				v, err := c.Open(file)
				if err != nil {
					t.Fatalf("not opened on the second GPU: %v", err)
				}
				if v.tier != c.tiers[1] {
					t.Fatal("opened on the first GPU")
				}
				if _, err := v.Next(context.Background()); err != nil {
					t.Fatal(err)
				}
				v.Close(context.Background())
			}
			if c.Failed() != nil {
				t.Fatalf("the client failed: %v", c.Failed())
			}
			c.mu.Lock()
			failed, running := c.tiers[0].failed != nil, c.tiers[0].helper != nil
			c.mu.Unlock()
			if want := first == "none"; failed != want || running == want {
				t.Fatalf("first GPU failed %v, its helper running %v", failed, running)
			}
		})
	}
	t.Run("all refuse", func(t *testing.T) {
		c := testClient(t, "refuse", "refuse")
		_, err := c.Open(file)
		if !errors.Is(err, ErrUnsupported) {
			t.Fatalf("opened on GPUs that refuse it: %v", err)
		}
		if c.Failed() != nil {
			t.Fatal("refusals failed the client")
		}
		if strings.Count(err.Error(), ErrUnsupported.Error()) != 2 {
			t.Fatalf("not one reason of each GPU: %v", err)
		}
	})
	t.Run("stream not decoded in hardware", func(t *testing.T) {
		c := testClient(t, "fake", "fake")
		// Extended profile, which the parser refuses before any device.
		bad := *file
		bad.Config = slices.Clone(file.Config)
		bad.Config[9] = 88
		if _, err := c.Open(&bad); err == nil {
			t.Fatal("opened")
		}
		c.mu.Lock()
		started := c.tiers[1].helper != nil
		c.mu.Unlock()
		if started {
			t.Fatal("the second GPU was woken for a stream no GPU decodes")
		}
	})
}

// TestProfileOfHelpers records profiles in the running helpers alone,
// while they decode, and stops waiting for one when told to.
func TestProfileOfHelpers(t *testing.T) {
	file := testFile(t)
	c := testClient(t, "fake", "fake")
	if got := c.Profile(context.Background(), "goroutine", 0); len(got) != 0 {
		t.Fatalf("%d profiles of helpers not started", len(got))
	}
	v, err := c.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())
	for _, kind := range []string{"cpu", "heap", "goroutine"} {
		got := c.Profile(context.Background(), kind, 200*time.Millisecond)
		if len(got) != 1 || got[0].Err != nil || got[0].PID == 0 || got[0].Name != "" && len(got[0].Data) == 0 {
			t.Fatalf("%s: %+v", kind, got)
		}
		// A profile of pprof is gzip.
		if len(got[0].Data) < 2 || got[0].Data[0] != 0x1f || got[0].Data[1] != 0x8b {
			t.Fatalf("%s: not a pprof profile", kind)
		}
	}
	if _, err := v.Next(context.Background()); err != nil {
		t.Fatalf("the stream after the profiles: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	got := c.Profile(ctx, "cpu", 10*time.Second)
	if len(got) != 1 || !errors.Is(got[0].Err, context.DeadlineExceeded) || time.Since(start) > 5*time.Second {
		t.Fatalf("a profile not stopped: %+v after %v", got, time.Since(start))
	}
}

// TestPictureIntoAnyImage checks a picture read straight into the image
// it goes to is the one copied into an image whose rows do not follow one
// another, picture after picture, the helper's buffer reused between them.
func TestPictureIntoAnyImage(t *testing.T) {
	file := testFile(t)
	c := testClient(t, "fake")
	v, err := c.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close(context.Background())
	size := image.Pt(36, 64)
	for range 3 {
		if _, err := v.Next(context.Background()); err != nil {
			t.Fatal(err)
		}
		flat := image.NewRGBA(image.Rectangle{Max: size})
		into := &flat.Pix[0]
		if got := v.PictureInto(flat); got != flat || &flat.Pix[0] != into {
			t.Fatal("not read into the image")
		}
		wide := image.NewRGBA(image.Rect(0, 0, size.X+5, size.Y))
		sub := wide.SubImage(image.Rectangle{Max: size}).(*image.RGBA)
		sub.Rect = sub.Rect.Sub(sub.Rect.Min)
		if v.PictureInto(sub) == nil {
			t.Fatal(v.Err())
		}
		for y := range size.Y {
			if string(flat.Pix[y*flat.Stride:y*flat.Stride+size.X*4]) != string(sub.Pix[y*sub.Stride:y*sub.Stride+size.X*4]) {
				t.Fatalf("row %d differs", y)
			}
		}
	}
}

// TestCloseStopsAndRestarts checks that the client stopping its helpers,
// every window hidden, fails nothing: the videos of the old helper tell
// ErrStopped, the next one opened starts a new helper, and the old video
// ending does not count against the new one's streams.
func TestCloseStopsAndRestarts(t *testing.T) {
	file := testFile(t)
	c := testClient(t, "fake")
	old, err := c.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
	c.Close()
	if _, err := old.Next(context.Background()); !errors.Is(err, ErrStopped) || errors.Is(err, ErrHelper) {
		t.Fatalf("a video of a helper stopped: %v", err)
	}
	if c.Failed() != nil {
		t.Fatalf("stopping failed the client: %v", c.Failed())
	}
	v, err := c.Open(file)
	if err != nil {
		t.Fatalf("not opened after the stop: %v", err)
	}
	defer v.Close(context.Background())
	if v.helper == old.helper {
		t.Fatal("opened on the helper stopped")
	}
	old.Close(context.Background())
	c.mu.Lock()
	running, streams := c.tiers[0].helper == v.helper, v.helper.streams
	idle := c.tiers[0].idle != nil
	c.mu.Unlock()
	if !running || streams != 1 || idle {
		t.Fatalf("the new helper running %v with %d streams, idle %v", running, streams, idle)
	}
	if _, err := v.Next(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// TestPick reads what the client tells a helper of the GPU to decode on,
// the system's default included, as the Windows helper reads it.
func TestPick(t *testing.T) {
	for _, tc := range []struct {
		value          string
		pick           int
		vendor, device uint32
	}{
		{"", pickAny, 0, 0},
		{"10de:25a5", pickOnly, 0x10de, 0x25a5},
		{"!10de:25a5", pickOthers, 0x10de, 0x25a5},
		{"default", pickOnly, 0, 0},
		{"!default", pickOthers, 0, 0},
		{"0000:0000", pickAny, 0, 0},
		{"nvidia", pickAny, 0, 0},
	} {
		t.Setenv(DeviceEnv, tc.value)
		pick, vendor, device := parsePick()
		if pick != tc.pick || vendor != tc.vendor || device != tc.device {
			t.Errorf("%q: pick %d %04x:%04x", tc.value, pick, vendor, device)
		}
	}
}

// TestClientForWindows checks the helpers of a client on Windows: the
// window's adapter alone first, then every other, the system's default
// named where none was chosen.
func TestClientForWindows(t *testing.T) {
	env := func(c *Client, i int) string {
		cmd := c.tiers[i].command()
		v := ""
		for _, e := range cmd.Env {
			if s, ok := strings.CutPrefix(e, DeviceEnv+"="); ok {
				v = s
			}
			if strings.HasPrefix(e, "VK_DRIVER_FILES=") {
				t.Errorf("a Vulkan driver named on Windows: %s", e)
			}
		}
		return v
	}
	for _, tc := range []struct {
		vendor, device uint32
		own, others    string
	}{
		{0x10de, 0x25a5, "10de:25a5", "!10de:25a5"},
		{0, 0, "default", "!default"},
	} {
		c := clientFor("windows", "", tc.vendor, tc.device)
		if len(c.tiers) != 2 || env(c, 0) != tc.own || env(c, 1) != tc.others {
			t.Errorf("%04x:%04x: %d tiers, %q then %q", tc.vendor, tc.device, len(c.tiers), env(c, 0), env(c, 1))
		}
	}
	if c := clientFor("darwin", "", 0x10de, 0x25a5); len(c.tiers) != 1 {
		t.Errorf("%d tiers where nothing decodes", len(c.tiers))
	}
}

// TestDeviceEnv reads what the client tells the helpers.
func TestDeviceEnv(t *testing.T) {
	for _, tc := range []struct {
		value          string
		vendor, device uint32
		only, ok       bool
	}{
		{"8086:46a3", 0x8086, 0x46a3, true, true},
		{"!10de:25a2", 0x10de, 0x25a2, false, true},
		{"", 0, 0, false, false},
		{"nvidia", 0, 0, false, false},
	} {
		t.Setenv(DeviceEnv, tc.value)
		vendor, device, only, ok := ParseDeviceEnv()
		if vendor != tc.vendor || device != tc.device || only != tc.only || ok != tc.ok {
			t.Errorf("%q: %04x:%04x only %v ok %v", tc.value, vendor, device, only, ok)
		}
	}
	c := NewClientFor("intel_icd.x86_64.json", 0x8086, 0x46a3)
	if runtime.GOOS == "linux" && len(c.tiers) != 2 {
		t.Fatalf("%d tiers", len(c.tiers))
	}
}

// FuzzSend sends a decoder samples made of anything: errors, never a
// panic, and never a picture handed to the device that names a surface it
// has not.
func FuzzSend(f *testing.F) {
	file := testFile(f)
	for i := range 4 {
		sample, _ := file.Sample(i, nil)
		f.Add(sample[:min(len(sample), 512)])
	}
	c, _ := parseAVCConfig(file.Config)
	for _, nal := range append(c.sps, c.pps...) {
		f.Add(append([]byte{0, 0, 0, byte(len(nal))}, nal...))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		dev := new(fakeDevice)
		d, err := newDecoder(dev, file.Config)
		if err != nil {
			t.Fatal(err)
		}
		// Twice: the second is decoded with the state the first left.
		for range 2 {
			if d.Send(data) != nil {
				break
			}
			for d.Receive() {
			}
		}
		for _, p := range dev.pictures {
			if p.surface < 0 || p.surface >= len(d.used) || len(p.refs) > 16 {
				t.Fatalf("picture in surface %d with %d references", p.surface, len(p.refs))
			}
			for _, r := range p.refs {
				if r.surface < 0 || r.surface >= len(d.used) {
					t.Fatalf("reference in surface %d", r.surface)
				}
			}
		}
	})
}

// FuzzParameterSets parses parameter sets and slice headers made of
// anything.
func FuzzParameterSets(f *testing.F) {
	file := testFile(f)
	c, _ := parseAVCConfig(file.Config)
	for _, nal := range append(c.sps, c.pps...) {
		f.Add(nal)
	}
	f.Fuzz(func(t *testing.T, nal []byte) {
		sps, err := parseSPS(nal)
		if err == nil && (sps.Width() <= 0 || sps.Height() <= 0 || sps.Width() > MaxSide || sps.Height() > MaxSide) {
			t.Fatalf("a sequence parameter set of %dx%d", sps.Width(), sps.Height())
		}
		known, _ := parseSPS(c.sps[0])
		pps, _ := parsePPS(nal, func(uint8) *SPS { return known })
		if pps == nil {
			pps, _ = parsePPS(c.pps[0], func(uint8) *SPS { return known })
		}
		_, _ = parseSliceHeader(nal, func(uint8) (*SPS, *PPS) { return known, pps })
		_, _ = parseAVCConfig(nal)
	})
}

// TestRendersOnGPU checks the helper has a session that scales pictures
// on the GPU do it, with the plan of the CPU's scaler, and scales them
// itself where the session cannot.
func TestRendersOnGPU(t *testing.T) {
	file := testFile(t)
	size := image.Pt(36, 64)
	// The CPU's blocks for this size: what the GPU is told to average.
	plan := (&resample.VideoScaler{}).Plan(file.Width, file.Height, size.X, size.Y)
	for _, mode := range []string{"gpu", "fake"} {
		t.Run(mode, func(t *testing.T) {
			c := testClient(t, mode)
			v, err := c.Open(file)
			if err != nil {
				t.Fatal(err)
			}
			defer v.Close(context.Background())
			for range 3 {
				if _, err := v.Next(context.Background()); err != nil {
					t.Fatal(err)
				}
				im := image.NewRGBA(image.Rectangle{Max: size})
				if v.PictureInto(im) == nil {
					t.Fatal(v.Err())
				}
				p := im.Pix[len(im.Pix)-4:]
				switch {
				case mode == "gpu" && (p[3] != 0xa5 || int(p[1]) != plan.BlockX || int(p[2]) != plan.BlockY):
					t.Fatalf("a picture not scaled on the GPU as planned: %v, want blocks of %dx%d", p, plan.BlockX, plan.BlockY)
				case mode == "fake" && p[3] != 0xff:
					t.Fatalf("a picture not scaled on the CPU: %v", p)
				}
			}
		})
	}
}

// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import (
	"context"
	"errors"
	"image"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"komarugram/pkg/mp4"
	"komarugram/pkg/program"
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
	}
	os.Exit(0)
}

// testClient is a client whose helper is the test binary in mode.
func testClient(t *testing.T, mode string) *Client {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := NewClient(func() *exec.Cmd {
		cmd := program.Command(self)
		cmd.Env = append(os.Environ(), helperEnv+"="+mode)
		return cmd
	})
	t.Cleanup(c.Close)
	return c
}

// fakeDevice decodes nothing: it keeps the pictures it was handed, and
// reads each surface as a picture whose first sample is the number, in
// decoding order, of the picture decoded into it.
type fakeDevice struct {
	die      bool
	pictures []*picture
	surfaces map[int]int
}

func (d *fakeDevice) open(sps *SPS, surfaces int) (session, error) {
	if d.surfaces == nil {
		d.surfaces = map[int]int{}
	}
	return d, nil
}

func (d *fakeDevice) close() {}

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
	running := c.helper != nil
	c.mu.Unlock()
	if running {
		t.Fatal("the helper still runs")
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

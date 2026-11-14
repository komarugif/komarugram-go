// SPDX-License-Identifier: Unlicense OR MIT

// Package hwvideo decodes H.264 with the video decoder of the GPU, in a
// process of its own (docs/HW_VIDEO.md).
//
// A hardware decoder decodes slices; everything about the stream above
// them is the application's: which pictures are references, in which
// order pictures show, which surface each is decoded into. That part is
// here, in Go and the same for every system (Decoder), checked before
// anything reaches a driver: what is not plainly a progressive 4:2:0 stream
// of 8 bits is refused with ErrUnsupported, and left to the software
// decoder. Under it is the device of a system (device, session): Direct3D
// 11 on Windows; Vulkan Video on Linux is to come.
//
// The window never decodes here itself: it starts the client's own binary
// again as a helper (Client, Serve), sends it the samples and gets small
// pictures back, so that a driver that falls on hostile data ends the
// helper and not the windows.
package hwvideo

import (
	"image"
	"slices"
)

// ref is a reference picture of the picture being decoded.
type ref struct {
	surface int
	// frameNum is its frame_num, or its LongTermFrameIdx if long.
	frameNum    uint32
	long        bool
	top, bottom int32
}

// picture is one coded picture, a frame, for a session to decode.
type picture struct {
	sps        *SPS
	pps        *PPS
	surface    int
	frameNum   uint32
	reference  bool
	intra, idr bool
	// top and bottom are the picture order counts of its fields.
	top, bottom int32
	refs        []ref
	// slices are its NAL units, header byte included, as the stream holds
	// them.
	slices [][]byte
}

// device is the video decoder of a GPU.
type device interface {
	// open makes a session for streams of sps with so many surfaces, or
	// fails with ErrUnsupported for a stream the device does not decode.
	open(sps *SPS, surfaces int) (session, error)
	close()
}

// session decodes the pictures of one stream into its surfaces.
type session interface {
	decode(p *picture) error
	// read copies the picture of surface, without what sps crops off it,
	// into dst, whose planes are reused when large enough.
	read(surface int, sps *SPS, dst *image.YCbCr) error
	close()
}

// frame is a decoded picture kept in a surface.
type frame struct {
	surface     int
	frameNum    uint32
	long        bool
	longIdx     uint32
	top, bottom int32
	// ref is set while it is a reference, waiting until it is put out, and
	// held from then until the caller has taken another.
	ref, waiting, held bool
}

func (f *frame) order() int32 { return min(f.top, f.bottom) }

// MaxSlices bounds the slices of one picture.
const MaxSlices = 256

// Decoder decodes one H.264 stream of frames: it is sent the samples in
// decoding order and puts the pictures out in the order they show.
type Decoder struct {
	device     device
	lengthSize int
	sps        map[uint8]*SPS
	pps        map[uint8]*PPS
	// active is the sequence parameter set of the session.
	active  *SPS
	session session
	used    []bool
	// dpb holds the references and the pictures waiting to be put out;
	// ready those put out, in order; current the one taken last.
	dpb     []*frame
	ready   []*frame
	current *frame
	// reorder is how many pictures may wait for one that shows before
	// them, size how many pictures the stream keeps at once.
	reorder, size int
	// The state the picture order counts and the frame numbers are
	// derived from. started is set once a picture was decoded.
	started            bool
	prevPOCMsb         int32
	prevPOCLsb         int32
	prevFrameNum       uint32
	prevFrameNumOffset int64
	prevRefFrameNum    uint32
	// maxLongIdx is the largest long-term index in use, -1 for none.
	maxLongIdx int
}

// newDecoder starts a decoder on dev of the stream config, an
// AVCDecoderConfigurationRecord, describes.
func newDecoder(dev device, config []byte) (*Decoder, error) {
	c, err := parseAVCConfig(config)
	if err != nil {
		return nil, err
	}
	d := &Decoder{device: dev, lengthSize: c.lengthSize, sps: map[uint8]*SPS{}, pps: map[uint8]*PPS{}, maxLongIdx: -1}
	for _, nal := range slices.Concat(c.sps, c.pps) {
		if err := d.parameterSet(nal); err != nil {
			return nil, err
		}
	}
	// The session is made now, for a stream the device cannot decode to
	// be refused before its first sample: with the sequence parameter set
	// the pictures name, which a picture parameter set tells.
	var first *SPS
	for _, p := range d.pps {
		if s := d.sps[p.SPS]; first == nil || s.ID < first.ID {
			first = s
		}
	}
	if first == nil {
		return nil, malformed("decoder configuration without parameter sets")
	}
	if err := d.activate(first); err != nil {
		return nil, err
	}
	return d, nil
}

// parameterSet keeps the parameter set of nal.
func (d *Decoder) parameterSet(nal []byte) error {
	if len(nal) == 0 {
		return malformed("empty NAL unit")
	}
	switch nal[0] & 0x1f {
	case nalSPS:
		s, err := parseSPS(nal)
		if err != nil {
			return err
		}
		if old := d.sps[s.ID]; old == nil || !slices.Equal(old.raw, s.raw) {
			d.sps[s.ID] = s
		}
	case nalPPS:
		p, err := parsePPS(nal, func(id uint8) *SPS { return d.sps[id] })
		if err != nil {
			return err
		}
		d.pps[p.ID] = p
	default:
		return malformed("NAL unit of type %d among the parameter sets", nal[0]&0x1f)
	}
	return nil
}

// maxDpbMbs is MaxDpbMbs of a level (table A-1).
func maxDpbMbs(level uint8) int {
	switch {
	case level <= 10:
		return 396
	case level == 11:
		return 900
	case level <= 20:
		return 2376
	case level == 21:
		return 4752
	case level <= 30:
		return 8100
	case level == 31:
		return 18000
	case level == 32:
		return 20480
	case level <= 41:
		return 32768
	case level == 42:
		return 34816
	case level == 50:
		return 110400
	case level <= 52:
		return 184320
	}
	return 696320
}

// activate makes s the sequence parameter set of the pictures to come.
func (d *Decoder) activate(s *SPS) error {
	switch s.Profile {
	case 66, 77, 100: // Baseline, Main, High
	default:
		return unsupported("profile %d", s.Profile)
	}
	size := min(maxDpbMbs(s.Level)/(s.WidthMbs*s.HeightMbs), 16)
	if s.Restricted {
		size = int(s.MaxDecBuffering)
	}
	size = max(size, int(s.MaxRefFrames), 1)
	reorder := size
	switch {
	case s.Restricted:
		reorder = int(s.MaxReorder)
	case s.POCType == 2:
		// Pictures show in the order they are decoded in.
		reorder = 0
	}
	// The references and the pictures waiting, the one being decoded, and
	// those put out and not taken yet: after a picture that puts out every
	// one before it, as many as waited.
	surfaces := size + reorder + 3
	if d.session != nil {
		if s.WidthMbs != d.active.WidthMbs || s.HeightMbs != d.active.HeightMbs || surfaces > len(d.used) {
			return unsupported("the stream changes its size")
		}
	} else {
		session, err := d.device.open(s, surfaces)
		if err != nil {
			return err
		}
		d.session, d.used = session, make([]bool, surfaces)
	}
	d.active, d.size, d.reorder = s, size, reorder
	return nil
}

// Size is the size of the pictures put out.
func (d *Decoder) Size() image.Point { return image.Pt(d.active.Width(), d.active.Height()) }

// units splits a sample into its NAL units.
func (d *Decoder) units(sample []byte) ([][]byte, error) {
	var out [][]byte
	for len(sample) > 0 {
		if len(sample) < d.lengthSize {
			return nil, malformed("sample ends in a NAL unit's length")
		}
		n := 0
		for _, b := range sample[:d.lengthSize] {
			n = n<<8 | int(b)
		}
		sample = sample[d.lengthSize:]
		if n <= 0 || n > len(sample) {
			return nil, malformed("NAL unit of %d bytes in %d", n, len(sample))
		}
		if sample[0]&0x80 != 0 {
			return nil, malformed("NAL unit with the forbidden bit")
		}
		out = append(out, sample[:n])
		sample = sample[n:]
	}
	return out, nil
}

// Send decodes sample, one coded picture as NAL units prefixed with their
// lengths. An error other than of the device leaves the decoder as it was
// before a picture; any error is the end of the stream for the caller,
// which decodes it in software.
func (d *Decoder) Send(sample []byte) error {
	units, err := d.units(sample)
	if err != nil {
		return err
	}
	var coded [][]byte
	var first *sliceHeader
	var lastMb uint32
	intra := true
	sets := func(id uint8) (*SPS, *PPS) {
		p := d.pps[id]
		if p == nil {
			return nil, nil
		}
		return d.sps[p.SPS], p
	}
	for _, nal := range units {
		switch nal[0] & 0x1f {
		case nalSPS, nalPPS:
			if first != nil {
				return unsupported("parameter sets between the slices of a picture")
			}
			if err := d.parameterSet(nal); err != nil {
				return err
			}
		case nalPartitionA, nalPartitionB, nalPartitionC:
			return unsupported("data partitioning")
		case nalSlice, nalIDR:
			h, err := parseSliceHeader(nal, sets)
			if err != nil {
				return err
			}
			if first == nil {
				first = h
			} else if h.pps != first.pps || h.frameNum != first.frameNum || h.idr != first.idr || h.reference != first.reference {
				return unsupported("slices of several pictures in a sample")
			}
			if len(coded) > 0 && h.firstMb <= lastMb {
				return unsupported("slices in an arbitrary order")
			}
			lastMb = h.firstMb
			if h.sliceType != sliceI {
				intra = false
			}
			if len(coded) == MaxSlices {
				return unsupported("over %d slices in a picture", MaxSlices)
			}
			coded = append(coded, nal)
		}
	}
	if first == nil {
		// Parameter sets or messages alone.
		return nil
	}
	sps, pps := sets(first.pps)
	if sps != d.active {
		if !first.idr {
			return malformed("the sequence parameter set changes at a picture that is not IDR")
		}
		d.outputAll()
		if err := d.activate(sps); err != nil {
			return err
		}
	}
	maxFrameNum := uint32(1) << sps.Log2MaxFrameNum
	if first.idr {
		if first.frameNum != 0 {
			return malformed("an IDR picture of frame number %d", first.frameNum)
		}
		if first.noOutputOfPrior {
			for _, f := range d.dpb {
				f.waiting = false
			}
		} else {
			d.outputAll()
		}
		for _, f := range d.dpb {
			f.ref = false
		}
		d.sweep()
		d.prevPOCMsb, d.prevPOCLsb, d.prevFrameNumOffset, d.prevFrameNum = 0, 0, 0, 0
		d.maxLongIdx = -1
	} else if !d.started {
		// Decoding starts at a picture that is not IDR, as after a seek to
		// a recovery point: its references are not there, and it is taken
		// as the first of its numbers.
		if !intra {
			return unsupported("the stream starts at a picture with references")
		}
		d.prevFrameNum, d.prevRefFrameNum = first.frameNum, first.frameNum
	} else if first.frameNum != d.prevRefFrameNum && first.frameNum != (d.prevRefFrameNum+1)%maxFrameNum {
		// Frames the stream left out are for a software decoder to make up.
		return unsupported("a gap in the frame numbers")
	}
	top, bottom, offset := d.pictureOrder(sps, first)
	surface := slices.Index(d.used, false)
	if surface < 0 {
		return unsupported("no surface left of %d", len(d.used))
	}
	p := &picture{sps: sps, pps: pps, surface: surface, frameNum: first.frameNum, reference: first.reference, intra: intra, idr: first.idr, top: top, bottom: bottom, slices: coded}
	for _, f := range d.dpb {
		if !f.ref {
			continue
		}
		r := ref{surface: f.surface, frameNum: f.frameNum, long: f.long, top: f.top, bottom: f.bottom}
		if f.long {
			r.frameNum = f.longIdx
		}
		p.refs = append(p.refs, r)
	}
	if !intra && len(p.refs) == 0 {
		return malformed("a picture with references and none kept")
	}
	if err := d.session.decode(p); err != nil {
		return err
	}
	d.used[surface] = true
	d.started = true
	current := &frame{surface: surface, frameNum: first.frameNum, top: top, bottom: bottom, waiting: true}
	cleared, err := d.mark(sps, first, current)
	if err != nil {
		// The picture is decoded but the stream ends here: nothing of it
		// is put out.
		d.used[surface] = false
		return err
	}
	d.prevFrameNum, d.prevFrameNumOffset = first.frameNum, offset
	if first.reference {
		d.prevRefFrameNum = first.frameNum
		if sps.POCType == 0 {
			d.prevPOCMsb, d.prevPOCLsb = top-int32(first.pocLsb), int32(first.pocLsb)
		}
	}
	if cleared {
		// A picture that forgets every one before it (operation 5) is the
		// first of its numbers, as an IDR picture is: those before it show
		// before it.
		d.outputAll()
		base := current.order()
		current.top -= base
		current.bottom -= base
		current.frameNum = 0
		d.prevFrameNum, d.prevFrameNumOffset, d.prevRefFrameNum = 0, 0, 0
		d.prevPOCMsb, d.prevPOCLsb = 0, current.top
	}
	d.dpb = append(d.dpb, current)
	for {
		waiting := 0
		for _, f := range d.dpb {
			if f.waiting {
				waiting++
			}
		}
		if waiting == 0 || waiting <= d.reorder && len(d.dpb) <= d.size {
			break
		}
		d.output()
	}
	return nil
}

// pictureOrder derives the picture order counts of the fields of the frame
// of h (8.2.1), and its FrameNumOffset.
func (d *Decoder) pictureOrder(sps *SPS, h *sliceHeader) (top, bottom int32, offset int64) {
	maxFrameNum := int64(1) << sps.Log2MaxFrameNum
	if !h.idr {
		offset = d.prevFrameNumOffset
		if d.prevFrameNum > h.frameNum {
			offset += maxFrameNum
		}
	}
	switch sps.POCType {
	case 0:
		maxLsb := int32(1) << sps.Log2MaxPOCLsb
		lsb := int32(h.pocLsb)
		msb := d.prevPOCMsb
		switch {
		case lsb < d.prevPOCLsb && d.prevPOCLsb-lsb >= maxLsb/2:
			msb += maxLsb
		case lsb > d.prevPOCLsb && lsb-d.prevPOCLsb > maxLsb/2:
			msb -= maxLsb
		}
		top = msb + lsb
		bottom = top + h.deltaBottom
	case 1:
		var abs int64
		if len(sps.RefFrameOffsets) != 0 {
			abs = offset + int64(h.frameNum)
		}
		if !h.reference && abs > 0 {
			abs--
		}
		var expected int64
		if abs > 0 {
			var cycle int64
			for _, o := range sps.RefFrameOffsets {
				cycle += int64(o)
			}
			n := int64(len(sps.RefFrameOffsets))
			expected = (abs - 1) / n * cycle
			for _, o := range sps.RefFrameOffsets[:(abs-1)%n+1] {
				expected += int64(o)
			}
		}
		if !h.reference {
			expected += int64(sps.OffsetNonRef)
		}
		top = int32(expected) + h.delta[0]
		bottom = top + sps.OffsetTopToBottom + h.delta[1]
	case 2:
		if !h.idr {
			top = int32(2 * (offset + int64(h.frameNum)))
			if !h.reference {
				top--
			}
		}
		bottom = top
	}
	return top, bottom, offset
}

// mark marks the references as the picture of h, current, tells, and
// current as one if it is (8.2.5). cleared tells the picture forgot every
// one before it (operation 5).
func (d *Decoder) mark(sps *SPS, h *sliceHeader, current *frame) (cleared bool, err error) {
	if !h.reference {
		return false, nil
	}
	current.ref = true
	limit := max(int(sps.MaxRefFrames), 1)
	count := func() (short, long int) {
		for _, f := range d.dpb {
			switch {
			case f.ref && f.long:
				long++
			case f.ref:
				short++
			}
		}
		return short, long
	}
	// picNum is FrameNumWrap of a short-term reference: its number as seen
	// from the current picture.
	maxFrameNum := int64(1) << sps.Log2MaxFrameNum
	picNum := func(f *frame) int64 {
		if f.frameNum > h.frameNum {
			return int64(f.frameNum) - maxFrameNum
		}
		return int64(f.frameNum)
	}
	unmarkLong := func(idx uint32) {
		for _, f := range d.dpb {
			if f.ref && f.long && f.longIdx == idx {
				f.ref = false
			}
		}
	}
	switch {
	case h.idr:
		if h.longTermReference {
			current.long, current.longIdx = true, 0
			d.maxLongIdx = 0
		}
	case !h.adaptive:
		// The sliding window: the oldest short-term reference goes.
		short, long := count()
		if short+long >= limit {
			var oldest *frame
			for _, f := range d.dpb {
				if f.ref && !f.long && (oldest == nil || picNum(f) < picNum(oldest)) {
					oldest = f
				}
			}
			if oldest == nil {
				return false, malformed("no short-term reference for the sliding window to drop")
			}
			oldest.ref = false
		}
	default:
		for _, m := range h.mmcos {
			switch m.op {
			case 1, 3:
				target := int64(h.frameNum) - int64(m.diffPicNums) - 1
				var found *frame
				for _, f := range d.dpb {
					if f.ref && !f.long && picNum(f) == target {
						found = f
					}
				}
				if found == nil {
					return false, malformed("marking of a short-term reference that is not kept")
				}
				if m.op == 1 {
					found.ref = false
					break
				}
				if int(m.longTermFrameIdx) > d.maxLongIdx {
					return false, malformed("long-term index %d over the largest, %d", m.longTermFrameIdx, d.maxLongIdx)
				}
				unmarkLong(m.longTermFrameIdx)
				found.ref, found.long, found.longIdx = true, true, m.longTermFrameIdx
			case 2:
				unmarkLong(m.longTermPicNum)
			case 4:
				d.maxLongIdx = int(m.maxLongTermIdxOne) - 1
				for _, f := range d.dpb {
					if f.ref && f.long && int(f.longIdx) > d.maxLongIdx {
						f.ref = false
					}
				}
			case 5:
				for _, f := range d.dpb {
					f.ref = false
				}
				d.maxLongIdx = -1
				cleared = true
			case 6:
				if int(m.longTermFrameIdx) > d.maxLongIdx {
					return false, malformed("long-term index %d over the largest, %d", m.longTermFrameIdx, d.maxLongIdx)
				}
				unmarkLong(m.longTermFrameIdx)
				current.long, current.longIdx = true, m.longTermFrameIdx
			}
		}
	}
	if short, long := count(); short+long+1 > limit {
		return false, malformed("%d references kept of %d", short+long+1, limit)
	}
	d.sweep()
	return cleared, nil
}

// sweep drops from the pictures kept those that are neither references nor
// waiting, and frees their surfaces.
func (d *Decoder) sweep() {
	d.dpb = slices.DeleteFunc(d.dpb, func(f *frame) bool {
		if f.ref || f.waiting {
			return false
		}
		d.free(f)
		return true
	})
}

// free gives back the surface of f once nothing needs it.
func (d *Decoder) free(f *frame) {
	if !f.ref && !f.waiting && !f.held {
		d.used[f.surface] = false
	}
}

// output puts out the picture waiting that shows first.
func (d *Decoder) output() {
	var next *frame
	for _, f := range d.dpb {
		if f.waiting && (next == nil || f.order() < next.order()) {
			next = f
		}
	}
	if next == nil {
		return
	}
	next.waiting, next.held = false, true
	d.ready = append(d.ready, next)
	d.sweep()
}

// outputAll puts out every picture waiting.
func (d *Decoder) outputAll() {
	for range len(d.dpb) {
		d.output()
	}
}

// End puts out the pictures still waiting, at the end of the stream.
func (d *Decoder) End() { d.outputAll() }

// Receive takes the next picture put out, for Read, and reports whether
// there was one.
func (d *Decoder) Receive() bool {
	if d.current != nil {
		d.current.held = false
		d.free(d.current)
		d.current = nil
	}
	if len(d.ready) == 0 {
		return false
	}
	d.current = d.ready[0]
	d.ready = d.ready[1:]
	return true
}

// Read copies the picture taken last into dst.
func (d *Decoder) Read(dst *image.YCbCr) error {
	if d.current == nil {
		return malformed("no picture taken")
	}
	return d.session.read(d.current.surface, d.active, dst)
}

// Flush forgets every picture, to go on from a keyframe.
func (d *Decoder) Flush() {
	d.dpb, d.ready, d.current = nil, nil, nil
	clear(d.used)
	d.started = false
	d.prevPOCMsb, d.prevPOCLsb, d.prevFrameNum, d.prevFrameNumOffset, d.prevRefFrameNum = 0, 0, 0, 0, 0
	d.maxLongIdx = -1
}

// Close releases the session.
func (d *Decoder) Close() {
	if d.session != nil {
		d.session.close()
		d.session = nil
	}
}

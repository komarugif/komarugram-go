// SPDX-License-Identifier: Unlicense OR MIT

package hwvideo

import (
	"errors"
	"fmt"
)

// ErrUnsupported is the error of a stream that is well formed, as far as it
// was read, but not decoded in hardware here: it is for the software
// decoder.
var ErrUnsupported = errors.New("hwvideo: not decoded in hardware")

// errMalformed is the error of a stream whose headers cannot be read.
var errMalformed = errors.New("hwvideo: malformed stream")

func unsupported(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrUnsupported, fmt.Sprintf(format, args...))
}

func malformed(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errMalformed, fmt.Sprintf(format, args...))
}

// Types of NAL units.
const (
	nalSlice      = 1
	nalPartitionA = 2
	nalPartitionB = 3
	nalPartitionC = 4
	nalIDR        = 5
	nalSPS        = 7
	nalPPS        = 8
)

// Types of slices, modulo 5.
const (
	sliceP  = 0
	sliceB  = 1
	sliceI  = 2
	sliceSP = 3
	sliceSI = 4
)

// MaxSide bounds the pictures decoded in hardware: GIFs are within HD.
const MaxSide = 2048

// scalingLists are the scaling lists of a parameter set, in the order they
// are sent in (zigzag): the six of 4x4 and the two of 8x8 that pictures
// of 4:2:0 use.
type scalingLists struct {
	l4 [6][16]uint8
	l8 [2][64]uint8
}

// The default scaling lists (tables 7-3 and 7-4), in the order they are
// sent in.
var (
	default4Intra = [16]uint8{6, 13, 13, 20, 20, 20, 28, 28, 28, 28, 32, 32, 32, 37, 37, 42}
	default4Inter = [16]uint8{10, 14, 14, 20, 20, 20, 24, 24, 24, 24, 27, 27, 27, 30, 30, 34}
	default8Intra = [64]uint8{
		6, 10, 10, 13, 11, 13, 16, 16, 16, 16, 18, 18, 18, 18, 18, 23,
		23, 23, 23, 23, 23, 25, 25, 25, 25, 25, 25, 25, 27, 27, 27, 27,
		27, 27, 27, 27, 29, 29, 29, 29, 29, 29, 29, 31, 31, 31, 31, 31,
		31, 33, 33, 33, 33, 33, 36, 36, 36, 36, 38, 38, 38, 40, 40, 42,
	}
	default8Inter = [64]uint8{
		9, 13, 13, 15, 13, 15, 17, 17, 17, 17, 19, 19, 19, 19, 19, 21,
		21, 21, 21, 21, 21, 22, 22, 22, 22, 22, 22, 22, 24, 24, 24, 24,
		24, 24, 24, 24, 25, 25, 25, 25, 25, 25, 25, 27, 27, 27, 27, 27,
		27, 28, 28, 28, 28, 28, 30, 30, 30, 30, 32, 32, 32, 33, 33, 35,
	}
)

// flatLists are the lists of a stream that sends none.
func flatLists() (l scalingLists) {
	for i := range l.l4 {
		for j := range l.l4[i] {
			l.l4[i][j] = 16
		}
	}
	for i := range l.l8 {
		for j := range l.l8[i] {
			l.l8[i][j] = 16
		}
	}
	return l
}

// readScalingList reads one list into list. useDefault tells the stream
// asked for the default one instead.
func readScalingList(r *bitReader, list []uint8) (useDefault bool) {
	last, next := int32(8), int32(8)
	for j := range list {
		if next != 0 {
			delta := r.se()
			if delta < -128 || delta > 127 {
				r.failed = true
				return false
			}
			next = (last + delta + 256) % 256
			if j == 0 && next == 0 {
				useDefault = true
			}
		}
		if next != 0 {
			last = next
		}
		list[j] = uint8(last)
	}
	return useDefault
}

// readScalingLists reads the lists of a parameter set that sends them.
// A list not sent falls back to the one before it, and the first of its
// kind to that of fallback: the defaults for a sequence parameter set (rule
// A), the sequence's own for a picture parameter set (rule B). with8 tells
// whether the lists of 8x8 are there.
func readScalingLists(r *bitReader, fallback *scalingLists, with8 bool) scalingLists {
	var l scalingLists
	for i := range 6 {
		if r.flag() {
			if readScalingList(r, l.l4[i][:]) {
				if i < 3 {
					l.l4[i] = default4Intra
				} else {
					l.l4[i] = default4Inter
				}
			}
			continue
		}
		if i == 0 || i == 3 {
			l.l4[i] = fallback.l4[i]
		} else {
			l.l4[i] = l.l4[i-1]
		}
	}
	for i := range 2 {
		if with8 && r.flag() {
			if readScalingList(r, l.l8[i][:]) {
				if i == 0 {
					l.l8[i] = default8Intra
				} else {
					l.l8[i] = default8Inter
				}
			}
			continue
		}
		l.l8[i] = fallback.l8[i]
	}
	return l
}

// SPS is a sequence parameter set, as far as decoding in hardware needs it.
type SPS struct {
	ID                  uint8
	Profile, Level      uint8
	ChromaFormat        uint8
	BitDepthLuma        uint8 // minus 8
	BitDepthChroma      uint8 // minus 8
	ScalingPresent      bool
	scaling             scalingLists
	Log2MaxFrameNum     uint8
	POCType             uint8
	Log2MaxPOCLsb       uint8
	DeltaAlwaysZero     bool
	OffsetNonRef        int32
	OffsetTopToBottom   int32
	RefFrameOffsets     []int32
	MaxRefFrames        uint8
	GapsAllowed         bool
	WidthMbs, HeightMbs int
	FrameMbsOnly        bool
	MBAFF               bool
	Direct8x8           bool
	// The picture without what the stream crops off it, in luma samples.
	CropLeft, CropRight, CropTop, CropBottom int
	// Restricted tells MaxReorder and MaxDecBuffering were sent.
	Restricted      bool
	MaxReorder      uint8
	MaxDecBuffering uint8
	// raw is the set as it was sent, to tell one sent again from another.
	raw []byte
}

// Width and Height are the size of the pictures shown.
func (s *SPS) Width() int  { return s.WidthMbs*16 - s.CropLeft - s.CropRight }
func (s *SPS) Height() int { return s.HeightMbs*16 - s.CropTop - s.CropBottom }

// highProfile reports whether a sequence parameter set of profile holds
// the fields of the High profiles.
func highProfile(profile uint8) bool {
	switch profile {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		return true
	}
	return false
}

// parseSPS reads a sequence parameter set from its NAL unit, header byte
// included.
func parseSPS(nal []byte) (*SPS, error) {
	if len(nal) < 4 {
		return nil, malformed("sequence parameter set of %d bytes", len(nal))
	}
	r := &bitReader{data: unescape(nal[1:], 1<<16)}
	s := &SPS{raw: append([]byte(nil), nal...), ChromaFormat: 1, scaling: flatLists()}
	s.Profile = uint8(r.u(8))
	r.u(8) // The constraint flags.
	s.Level = uint8(r.u(8))
	s.ID = uint8(r.ueMax(31))
	if highProfile(s.Profile) {
		s.ChromaFormat = uint8(r.ueMax(3))
		separate := false
		if s.ChromaFormat == 3 {
			separate = r.flag()
		}
		s.BitDepthLuma = uint8(r.ueMax(6))
		s.BitDepthChroma = uint8(r.ueMax(6))
		bypass := r.flag()
		if r.failed {
			return nil, malformed("sequence parameter set")
		}
		// Before the scaling lists, of which 4:4:4 has more.
		if s.ChromaFormat != 1 || separate {
			return nil, unsupported("chroma format %d", s.ChromaFormat)
		}
		if s.BitDepthLuma != 0 || s.BitDepthChroma != 0 {
			return nil, unsupported("%d bits a sample", 8+s.BitDepthLuma)
		}
		if bypass {
			return nil, unsupported("lossless")
		}
		s.ScalingPresent = r.flag()
		if s.ScalingPresent {
			defaults := scalingLists{
				l4: [6][16]uint8{0: default4Intra, 3: default4Inter},
				l8: [2][64]uint8{default8Intra, default8Inter},
			}
			s.scaling = readScalingLists(r, &defaults, true)
		}
	}
	s.Log2MaxFrameNum = uint8(r.ueMax(12)) + 4
	s.POCType = uint8(r.ueMax(2))
	switch s.POCType {
	case 0:
		s.Log2MaxPOCLsb = uint8(r.ueMax(12)) + 4
	case 1:
		s.DeltaAlwaysZero = r.flag()
		s.OffsetNonRef = r.se()
		s.OffsetTopToBottom = r.se()
		n := r.ueMax(255)
		if r.failed {
			return nil, malformed("sequence parameter set")
		}
		s.RefFrameOffsets = make([]int32, n)
		for i := range s.RefFrameOffsets {
			s.RefFrameOffsets[i] = r.se()
		}
	}
	s.MaxRefFrames = uint8(r.ueMax(16))
	s.GapsAllowed = r.flag()
	s.WidthMbs = int(r.ueMax(MaxSide/16-1)) + 1
	heightUnits := int(r.ueMax(MaxSide/16-1)) + 1
	s.FrameMbsOnly = r.flag()
	s.HeightMbs = heightUnits
	if !s.FrameMbsOnly {
		s.HeightMbs = 2 * heightUnits
		s.MBAFF = r.flag()
	}
	s.Direct8x8 = r.flag()
	if r.flag() {
		// In units of two luma samples, for 4:2:0 frames.
		limit := uint32(MaxSide)
		s.CropLeft = 2 * int(r.ueMax(limit))
		s.CropRight = 2 * int(r.ueMax(limit))
		s.CropTop = 2 * int(r.ueMax(limit))
		s.CropBottom = 2 * int(r.ueMax(limit))
	}
	vui := r.flag()
	if r.failed {
		return nil, malformed("sequence parameter set")
	}
	if !s.FrameMbsOnly {
		return nil, unsupported("interlaced")
	}
	if s.HeightMbs > MaxSide/16 {
		return nil, unsupported("%d macroblocks high", s.HeightMbs)
	}
	if s.Width() <= 0 || s.Height() <= 0 {
		return nil, malformed("cropped to %dx%d", s.Width(), s.Height())
	}
	if vui {
		// What follows only tells how far pictures are reordered: a set
		// that ends early is read as one that does not tell.
		s.readVUI(r)
	}
	return s, nil
}

// readVUI reads the video usability information for how far the pictures
// are reordered.
func (s *SPS) readVUI(r *bitReader) {
	if r.flag() { // aspect_ratio_info_present_flag
		if r.u(8) == 255 {
			r.u(32)
		}
	}
	if r.flag() { // overscan_info_present_flag
		r.bit()
	}
	if r.flag() { // video_signal_type_present_flag
		r.u(4)
		if r.flag() {
			r.u(24)
		}
	}
	if r.flag() { // chroma_loc_info_present_flag
		r.ue()
		r.ue()
	}
	if r.flag() { // timing_info_present_flag
		r.u(32)
		r.u(32)
		r.bit()
	}
	hrd := func() {
		n := r.ueMax(31) + 1
		r.u(8)
		for range n {
			r.ue()
			r.ue()
			r.bit()
		}
		r.u(20)
	}
	nal := r.flag()
	if nal {
		hrd()
	}
	vcl := r.flag()
	if vcl {
		hrd()
	}
	if nal || vcl {
		r.bit() // low_delay_hrd_flag
	}
	r.bit() // pic_struct_present_flag
	if !r.flag() || r.failed {
		return
	}
	r.bit()
	r.ue()
	r.ue()
	r.ue()
	r.ue()
	reorder := r.ueMax(16)
	buffering := r.ueMax(16)
	if r.failed || reorder > buffering {
		return
	}
	s.Restricted, s.MaxReorder, s.MaxDecBuffering = true, uint8(reorder), uint8(buffering)
}

// PPS is a picture parameter set.
type PPS struct {
	ID, SPS                uint8
	CABAC                  bool
	PicOrderPresent        bool
	RefIdxL0, RefIdxL1     uint8 // minus 1, the defaults
	WeightedPred           bool
	WeightedBipred         uint8
	InitQP, InitQS         int8 // minus 26
	ChromaQPOffset         int8
	SecondChromaQPOffset   int8
	DeblockingControl      bool
	ConstrainedIntraPred   bool
	RedundantPicCntPresent bool
	Transform8x8           bool
	scaling                scalingLists
	raw                    []byte
}

// parsePPS reads a picture parameter set from its NAL unit, header byte
// included. sps gives the sequence parameter set it names, nil for none.
func parsePPS(nal []byte, sps func(id uint8) *SPS) (*PPS, error) {
	if len(nal) < 2 {
		return nil, malformed("picture parameter set of %d bytes", len(nal))
	}
	r := &bitReader{data: unescape(nal[1:], 1<<16)}
	p := &PPS{raw: append([]byte(nil), nal...)}
	p.ID = uint8(r.ueMax(255))
	p.SPS = uint8(r.ueMax(31))
	p.CABAC = r.flag()
	p.PicOrderPresent = r.flag()
	groups := r.ue()
	if r.failed {
		return nil, malformed("picture parameter set")
	}
	if groups != 0 {
		return nil, unsupported("slice groups")
	}
	s := sps(p.SPS)
	if s == nil {
		return nil, malformed("picture parameter set %d of no sequence parameter set %d", p.ID, p.SPS)
	}
	p.RefIdxL0 = uint8(r.ueMax(31))
	p.RefIdxL1 = uint8(r.ueMax(31))
	p.WeightedPred = r.flag()
	p.WeightedBipred = uint8(r.u(2))
	qp := func(low, high int32) int8 {
		v := r.se()
		if v < low || v > high {
			r.failed = true
		}
		return int8(v)
	}
	p.InitQP = qp(-26, 25)
	p.InitQS = qp(-26, 25)
	p.ChromaQPOffset = qp(-12, 12)
	p.DeblockingControl = r.flag()
	p.ConstrainedIntraPred = r.flag()
	p.RedundantPicCntPresent = r.flag()
	p.SecondChromaQPOffset = p.ChromaQPOffset
	p.scaling = s.scaling
	if r.failed || p.WeightedBipred == 3 {
		return nil, malformed("picture parameter set")
	}
	if r.more() {
		p.Transform8x8 = r.flag()
		if r.flag() {
			fallback := &s.scaling
			if !s.ScalingPresent {
				fallback = &scalingLists{
					l4: [6][16]uint8{0: default4Intra, 3: default4Inter},
					l8: [2][64]uint8{default8Intra, default8Inter},
				}
			}
			p.scaling = readScalingLists(r, fallback, p.Transform8x8)
		}
		p.SecondChromaQPOffset = qp(-12, 12)
		if r.failed {
			return nil, malformed("picture parameter set")
		}
	}
	return p, nil
}

// A memory management control operation of a reference picture's marking.
type mmco struct {
	op                uint8
	diffPicNums       uint32 // minus 1
	longTermPicNum    uint32
	longTermFrameIdx  uint32
	maxLongTermIdxOne uint32 // plus 1
}

// sliceHeader is what the header of a slice tells of its picture.
type sliceHeader struct {
	firstMb   uint32
	sliceType uint8 // modulo 5
	pps       uint8
	frameNum  uint32
	idr       bool
	reference bool
	idrPicID  uint32
	pocLsb    uint32
	// deltaBottom is delta_pic_order_cnt_bottom, delta delta_pic_order_cnt.
	deltaBottom int32
	delta       [2]int32
	// Of an IDR picture.
	noOutputOfPrior, longTermReference bool
	// Of another reference picture: the operations, nil for the sliding
	// window.
	adaptive bool
	mmcos    []mmco
}

// sliceHeaderBytes is how much of a slice is read for its header, which
// ends long before: the longest, with every reference reordered and
// weighted and every marking operation, is about 2 KB.
const sliceHeaderBytes = 4096

// parseSliceHeader reads the header of a slice from its NAL unit, header
// byte included, to the end of its marking of reference pictures. sets
// gives the parameter sets of a picture parameter set's id.
func parseSliceHeader(nal []byte, sets func(pps uint8) (*SPS, *PPS)) (*sliceHeader, error) {
	if len(nal) < 2 {
		return nil, malformed("slice of %d bytes", len(nal))
	}
	kind := nal[0] & 0x1f
	h := &sliceHeader{idr: kind == nalIDR, reference: nal[0]>>5&3 != 0}
	if h.idr && !h.reference {
		return nil, malformed("an IDR picture that is no reference")
	}
	r := &bitReader{data: unescape(nal[1:], sliceHeaderBytes)}
	h.firstMb = r.ue()
	sliceType := r.ueMax(9)
	h.sliceType = uint8(sliceType % 5)
	h.pps = uint8(r.ueMax(255))
	if r.failed {
		return nil, malformed("slice header")
	}
	if h.sliceType == sliceSP || h.sliceType == sliceSI {
		return nil, unsupported("switching slices")
	}
	if h.idr && h.sliceType != sliceI {
		return nil, malformed("an IDR picture with a slice of type %d", h.sliceType)
	}
	sps, pps := sets(h.pps)
	if sps == nil || pps == nil {
		return nil, malformed("slice of no picture parameter set %d", h.pps)
	}
	h.frameNum = r.u(int(sps.Log2MaxFrameNum))
	// Frames only: no field_pic_flag.
	if h.idr {
		h.idrPicID = r.ueMax(65535)
	}
	switch sps.POCType {
	case 0:
		h.pocLsb = r.u(int(sps.Log2MaxPOCLsb))
		if pps.PicOrderPresent {
			h.deltaBottom = r.se()
		}
	case 1:
		if !sps.DeltaAlwaysZero {
			h.delta[0] = r.se()
			if pps.PicOrderPresent {
				h.delta[1] = r.se()
			}
		}
	}
	if pps.RedundantPicCntPresent {
		if r.ueMax(127) != 0 {
			return nil, unsupported("redundant pictures")
		}
	}
	if h.sliceType == sliceB {
		r.bit() // direct_spatial_mv_pred_flag
	}
	refs := [2]uint32{uint32(pps.RefIdxL0), uint32(pps.RefIdxL1)}
	lists := 0
	switch h.sliceType {
	case sliceP:
		lists = 1
	case sliceB:
		lists = 2
	}
	if lists > 0 && r.flag() { // num_ref_idx_override_flag
		for i := range lists {
			refs[i] = r.ueMax(31)
		}
	}
	// The modifications of the lists of references.
	for range lists {
		if !r.flag() {
			continue
		}
		for n := 0; ; n++ {
			op := r.ue()
			if op == 3 {
				break
			}
			if op > 3 || n > 32 || r.failed {
				return nil, malformed("slice header: reordering of references")
			}
			r.ue()
		}
	}
	// The table of prediction weights.
	if pps.WeightedPred && h.sliceType == sliceP || pps.WeightedBipred == 1 && h.sliceType == sliceB {
		r.ueMax(7)
		r.ueMax(7) // 4:2:0 has chroma.
		for i := range lists {
			for range refs[i] + 1 {
				if r.flag() {
					r.se()
					r.se()
				}
				if r.flag() {
					r.se()
					r.se()
					r.se()
					r.se()
				}
			}
		}
	}
	if h.reference {
		if h.idr {
			h.noOutputOfPrior = r.flag()
			h.longTermReference = r.flag()
		} else if h.adaptive = r.flag(); h.adaptive {
			for {
				op := r.ue()
				if op == 0 {
					break
				}
				if op > 6 || len(h.mmcos) >= 66 || r.failed {
					return nil, malformed("slice header: marking of references")
				}
				m := mmco{op: uint8(op)}
				if op == 1 || op == 3 {
					m.diffPicNums = r.ue()
				}
				if op == 2 {
					m.longTermPicNum = r.ue()
				}
				if op == 3 || op == 6 {
					m.longTermFrameIdx = r.ueMax(15)
				}
				if op == 4 {
					m.maxLongTermIdxOne = r.ueMax(16)
				}
				h.mmcos = append(h.mmcos, m)
			}
		}
	}
	if r.failed {
		return nil, malformed("slice header")
	}
	return h, nil
}

// avcConfig is an AVCDecoderConfigurationRecord: the length of the prefix
// of each NAL unit of the samples, and the parameter sets.
type avcConfig struct {
	lengthSize int
	sps, pps   [][]byte
}

func parseAVCConfig(data []byte) (*avcConfig, error) {
	if len(data) < 7 || data[0] != 1 {
		return nil, malformed("decoder configuration")
	}
	c := &avcConfig{lengthSize: int(data[4]&3) + 1}
	if c.lengthSize == 3 {
		return nil, malformed("decoder configuration: NAL unit lengths of 3 bytes")
	}
	rest := data[5:]
	units := func(mask byte) ([][]byte, error) {
		if len(rest) == 0 {
			return nil, malformed("decoder configuration")
		}
		count := int(rest[0] & mask)
		rest = rest[1:]
		var out [][]byte
		for range count {
			if len(rest) < 2 {
				return nil, malformed("decoder configuration")
			}
			n := int(rest[0])<<8 | int(rest[1])
			rest = rest[2:]
			if n > len(rest) {
				return nil, malformed("decoder configuration")
			}
			out = append(out, rest[:n])
			rest = rest[n:]
		}
		return out, nil
	}
	var err error
	if c.sps, err = units(0x1f); err != nil {
		return nil, err
	}
	if c.pps, err = units(0xff); err != nil {
		return nil, err
	}
	return c, nil
}

// SPDX-License-Identifier: Unlicense OR MIT

//go:build linux && cgo && !novulkan

package hwvideo

/*
#cgo LDFLAGS: -ldl -lpthread
#include <stdlib.h>
#include "hwvk_linux.h"
*/
import "C"

import (
	"fmt"
	"image"
	"maps"
	"slices"
	"unsafe"
)

// The decoder of Vulkan Video (VK_KHR_video_decode_h264), in hwvk_linux.c:
// the driver is handed the parameter sets and the picture's references as
// Vulkan's StdVideo structures, and decodes the slices, sent as they are,
// into an array of NV12 pictures that are both the output and the
// references. Pictures are read back through a host buffer. Every call
// waits for the GPU: the decoder is one of a process of its own.

// vkError turns a status of hwvk_linux.c into an error.
func vkError(what string, status C.int, msg []C.char) error {
	text := C.GoString(&msg[0])
	switch status {
	case C.HWVK_OK:
		return nil
	case C.HWVK_UNSUPPORTED:
		return unsupported("%s: %s", what, text)
	case C.HWVK_LOST:
		return fmt.Errorf("%w: %s: %s", errDeviceLost, what, text)
	}
	return fmt.Errorf("hwvideo: %s: %s", what, text)
}

type vkDevice struct {
	d *C.hwvk_device
}

func newDevice() (device, error) {
	var d *C.hwvk_device
	var msg [512]C.char
	if err := vkError("Vulkan", C.hwvk_device_open(&d, &msg[0], C.size_t(len(msg))), msg[:]); err != nil {
		return nil, err
	}
	return &vkDevice{d: d}, nil
}

// Available reports whether this system has a hardware decoder to try:
// whether a machine has one is known once the helper is asked.
func Available() bool { return true }

func (d *vkDevice) close() {
	C.hwvk_device_close(d.d)
	d.d = nil
}

type vkSession struct {
	s *C.hwvk_session
	// gen is the generation of the parameter sets the session was given.
	gen     int
	given   bool
	data    []byte
	offsets []C.uint32_t
}

func (d *vkDevice) open(sps *SPS, surfaces int) (session, error) {
	var s *C.hwvk_session
	var msg [512]C.char
	status := C.hwvk_session_open(d.d, C.uint32_t(sps.WidthMbs), C.uint32_t(sps.HeightMbs), C.uint32_t(sps.Level), C.uint32_t(max(sps.MaxRefFrames, 1)), C.uint32_t(surfaces), &s, &msg[0], C.size_t(len(msg)))
	if err := vkError("video session", status, msg[:]); err != nil {
		return nil, err
	}
	return &vkSession{s: s}, nil
}

func (s *vkSession) close() {
	C.hwvk_session_close(s.s)
	s.s = nil
}

func copyLists(l *scalingLists, s4 *[6][16]C.uint8_t, s8 *[2][64]C.uint8_t) {
	for i := range l.l4 {
		for j, v := range l.l4[i] {
			s4[i][j] = C.uint8_t(v)
		}
	}
	for i := range l.l8 {
		for j, v := range l.l8[i] {
			s8[i][j] = C.uint8_t(v)
		}
	}
}

func cBool(b bool) C.uint8_t {
	if b {
		return 1
	}
	return 0
}

// parameters hands the session every parameter set of p's stream.
func (s *vkSession) parameters(p *picture) error {
	spsIDs := slices.Sorted(maps.Keys(p.spsSets))
	sps := make([]C.hwvk_sps, 0, len(spsIDs))
	for _, id := range spsIDs {
		in := p.spsSets[id]
		var o C.hwvk_sps
		o.id = C.uint8_t(in.ID)
		o.profile = C.uint8_t(in.Profile)
		o.level = C.uint8_t(in.Level)
		o.constraints = C.uint8_t(in.Constraints)
		o.log2_max_frame_num_minus4 = C.uint8_t(in.Log2MaxFrameNum - 4)
		o.poc_type = C.uint8_t(in.POCType)
		if in.POCType == 0 {
			o.log2_max_poc_lsb_minus4 = C.uint8_t(in.Log2MaxPOCLsb - 4)
		}
		o.max_num_ref_frames = C.uint8_t(in.MaxRefFrames)
		o.delta_pic_order_always_zero = cBool(in.DeltaAlwaysZero)
		o.gaps_allowed = cBool(in.GapsAllowed)
		o.direct_8x8 = cBool(in.Direct8x8)
		o.scaling_present = cBool(in.ScalingPresent)
		o.offset_for_non_ref_pic = C.int32_t(in.OffsetNonRef)
		o.offset_for_top_to_bottom_field = C.int32_t(in.OffsetTopToBottom)
		o.num_ref_frames_in_poc_cycle = C.uint32_t(len(in.RefFrameOffsets))
		for i, v := range in.RefFrameOffsets {
			o.offset_for_ref_frame[i] = C.int32_t(v)
		}
		o.width_mbs = C.uint32_t(in.WidthMbs)
		o.height_mbs = C.uint32_t(in.HeightMbs)
		o.crop_left = C.uint32_t(in.CropLeft / 2)
		o.crop_right = C.uint32_t(in.CropRight / 2)
		o.crop_top = C.uint32_t(in.CropTop / 2)
		o.crop_bottom = C.uint32_t(in.CropBottom / 2)
		copyLists(&in.scaling, &o.scaling4, &o.scaling8)
		sps = append(sps, o)
	}
	ppsIDs := slices.Sorted(maps.Keys(p.ppsSets))
	pps := make([]C.hwvk_pps, 0, len(ppsIDs))
	for _, id := range ppsIDs {
		in := p.ppsSets[id]
		if p.spsSets[in.SPS] == nil {
			continue
		}
		var o C.hwvk_pps
		o.id = C.uint8_t(in.ID)
		o.sps_id = C.uint8_t(in.SPS)
		o.num_ref_idx_l0_minus1 = C.uint8_t(in.RefIdxL0)
		o.num_ref_idx_l1_minus1 = C.uint8_t(in.RefIdxL1)
		o.weighted_bipred_idc = C.uint8_t(in.WeightedBipred)
		o.weighted_pred = cBool(in.WeightedPred)
		o.transform_8x8 = cBool(in.Transform8x8)
		o.redundant_pic_cnt_present = cBool(in.RedundantPicCntPresent)
		o.constrained_intra_pred = cBool(in.ConstrainedIntraPred)
		o.deblocking_filter_control_present = cBool(in.DeblockingControl)
		o.bottom_field_pic_order_present = cBool(in.PicOrderPresent)
		o.entropy_coding_mode = cBool(in.CABAC)
		o.init_qp_minus26 = C.int8_t(in.InitQP)
		o.init_qs_minus26 = C.int8_t(in.InitQS)
		o.chroma_qp_index_offset = C.int8_t(in.ChromaQPOffset)
		o.second_chroma_qp_index_offset = C.int8_t(in.SecondChromaQPOffset)
		copyLists(&in.scaling, &o.scaling4, &o.scaling8)
		pps = append(pps, o)
	}
	if len(sps) == 0 || len(pps) == 0 {
		return malformed("no parameter sets")
	}
	var msg [512]C.char
	status := C.hwvk_session_parameters(s.s, &sps[0], C.uint32_t(len(sps)), &pps[0], C.uint32_t(len(pps)), &msg[0], C.size_t(len(msg)))
	if err := vkError("parameter sets", status, msg[:]); err != nil {
		return err
	}
	s.gen, s.given = p.gen, true
	return nil
}

func (s *vkSession) decode(p *picture) error {
	if len(p.refs) > 16 {
		return malformed("%d references", len(p.refs))
	}
	if !s.given || s.gen != p.gen {
		if err := s.parameters(p); err != nil {
			return err
		}
	}
	s.data = s.data[:0]
	s.offsets = s.offsets[:0]
	for _, nal := range p.slices {
		s.offsets = append(s.offsets, C.uint32_t(len(s.data)))
		s.data = append(s.data, 0, 0, 1)
		s.data = append(s.data, nal...)
	}
	if len(s.offsets) == 0 {
		return malformed("a picture without slices")
	}
	var pic C.hwvk_pic
	c := &pic
	c.sps_id = C.uint8_t(p.sps.ID)
	c.pps_id = C.uint8_t(p.pps.ID)
	c.intra = cBool(p.intra)
	c.idr = cBool(p.idr)
	c.reference = cBool(p.reference)
	c.frame_num = C.uint32_t(p.frameNum)
	c.idr_pic_id = C.uint32_t(p.idrPicID)
	c.poc[0], c.poc[1] = C.int32_t(p.top), C.int32_t(p.bottom)
	c.surface = C.int32_t(p.surface)
	c.nrefs = C.uint32_t(len(p.refs))
	for i, r := range p.refs {
		c.refs[i] = C.hwvk_ref{
			surface:   C.int32_t(r.surface),
			frame_num: C.uint32_t(r.frameNum),
			long_term: cBool(r.long),
			poc:       [2]C.int32_t{C.int32_t(r.top), C.int32_t(r.bottom)},
		}
	}
	var msg [512]C.char
	status := C.hwvk_decode(s.s, c, (*C.uint8_t)(unsafe.Pointer(&s.data[0])), C.size_t(len(s.data)), &s.offsets[0], C.uint32_t(len(s.offsets)), &msg[0], C.size_t(len(msg)))
	return vkError("decode", status, msg[:])
}

func (s *vkSession) read(surface int, sps *SPS, dst *image.YCbCr) error {
	var nv12 *C.uint8_t
	var pitch, rows C.uint32_t
	var msg [512]C.char
	status := C.hwvk_read(s.s, C.uint32_t(surface), &nv12, &pitch, &rows, &msg[0], C.size_t(len(msg)))
	if err := vkError("read", status, msg[:]); err != nil {
		return err
	}
	p, h := int(pitch), int(rows)
	if nv12 == nil || p < sps.Width()+sps.CropLeft || h < sps.Height()+sps.CropTop {
		return fmt.Errorf("hwvideo: a picture of %dx%d read for %dx%d", p, h, sps.Width(), sps.Height())
	}
	src := unsafe.Slice((*byte)(unsafe.Pointer(nv12)), p*h*3/2)
	w, ht := sps.Width(), sps.Height()
	sizeYCbCr(dst, w, ht)
	for y := range ht {
		row := src[(y+sps.CropTop)*p+sps.CropLeft:]
		copy(dst.Y[y*dst.YStride:y*dst.YStride+w], row)
	}
	cw, ch := (w+1)/2, (ht+1)/2
	chroma := src[p*h:]
	for y := range ch {
		row := chroma[(y+sps.CropTop/2)*p+sps.CropLeft/2*2:]
		cb := dst.Cb[y*dst.CStride : y*dst.CStride+cw]
		cr := dst.Cr[y*dst.CStride : y*dst.CStride+cw]
		for x := range cw {
			cb[x], cr[x] = row[2*x], row[2*x+1]
		}
	}
	return nil
}

// SPDX-License-Identifier: Unlicense OR MIT

package mp4

import "encoding/binary"

// matrixUnknown is H.273's code for a matrix not told.
const matrixUnknown = 2

// readColr reads a colr box of a sample entry: the matrix of its pictures and, in an
// nclx one, whether they use the full range.
func (t *track) readColr(body []byte) {
	if len(body) < 10 {
		return
	}
	switch string(body[:4]) {
	case "nclx":
		t.colrMatrix = int(binary.BigEndian.Uint16(body[8:]))
		t.colrRange = len(body) >= 11
		t.colrFull = t.colrRange && body[10]&0x80 != 0
	case "nclc":
		t.colrMatrix = int(binary.BigEndian.Uint16(body[8:]))
	}
}

// colors gives file how its pictures are to be read: as H.264's own parameter set says
// (ITU-T H.264, E.1.1), which a decoder goes by, or where that says nothing, as the
// container does.
func (t *track) colors(file *File) {
	file.Matrix = matrixUnknown
	if t.colrMatrix != 0 {
		file.Matrix = t.colrMatrix
	}
	file.FullRange = t.colrRange && t.colrFull
	if t.codec == "mp4v" {
		return
	}
	matrix, full, told := avcColors(t.config)
	if !told {
		return
	}
	file.FullRange = full
	if matrix != matrixUnknown {
		file.Matrix = matrix
	}
}

// avcColors reads the matrix and the range the first sequence parameter set of an
// AVCDecoderConfigurationRecord tells; told is false when it tells nothing or cannot be
// read.
func avcColors(config []byte) (matrix int, full, told bool) {
	set, ok := readSPS(config)
	if !ok || !set.signal {
		return 0, false, false
	}
	return set.matrix, set.full, true
}

// AVCFormat is how the pictures of an H.264 stream are sampled, as its sequence
// parameter set tells (ITU-T H.264, 7.4.2.1.1): what FFmpeg picks the pixel format of
// its pictures by.
type AVCFormat struct {
	// Chroma is chroma_format_idc: 0 for monochrome, 1 for 4:2:0, 2 for 4:2:2, 3 for
	// 4:4:4.
	Chroma int
	// Depth is the bit depth of luma, and of chroma unless they differ.
	Depth int
	// RGB is set for 4:4:4 pictures whose planes are G, B and R rather than Y, Cb and
	// Cr: those whose matrix is the identity (H.273, code 0).
	RGB bool
}

// ReadAVCFormat reads how the pictures of the first sequence parameter set of an
// AVCDecoderConfigurationRecord are sampled; ok is false when it cannot be read.
func ReadAVCFormat(config []byte) (format AVCFormat, ok bool) {
	set, ok := readSPS(config)
	if !ok {
		return AVCFormat{}, false
	}
	return AVCFormat{Chroma: set.chroma, Depth: set.depth, RGB: set.chroma == 3 && set.described && set.matrix == 0}, true
}

// paramSet is what a sequence parameter set tells of its pictures.
type paramSet struct {
	// chroma is chroma_format_idc, depth the bit depth of luma.
	chroma, depth int
	// signal is set when the set tells the range, full; described when it tells
	// the matrix too.
	signal, described, full bool
	matrix                  int
}

// readSPS reads the first sequence parameter set of an AVCDecoderConfigurationRecord,
// as far as its video signal type; ok is false when it cannot be read. The record is
// from strangers: a read past its end ends the reading.
func readSPS(config []byte) (out paramSet, ok bool) {
	if len(config) < 8 || config[5]&0x1f == 0 {
		return paramSet{}, false
	}
	size := int(binary.BigEndian.Uint16(config[6:]))
	if size < 4 || len(config) < 8+size {
		return paramSet{}, false
	}
	nal := config[8 : 8+size]
	// Without the NAL header, and the bytes that keep a start code from occurring.
	rbsp := make([]byte, 0, len(nal))
	zeros := 0
	for _, b := range nal[1:] {
		if zeros >= 2 && b == 3 {
			zeros = 0
			continue
		}
		rbsp = append(rbsp, b)
		if b == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	r := &bitReader{data: rbsp}
	profile := r.bits(8)
	r.bits(16) // constraints, level
	r.ue()     // seq_parameter_set_id
	// Profiles without the fields are 8-bit 4:2:0.
	out.chroma, out.depth = 1, 8
	switch profile {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		chroma := r.ue()
		out.chroma = chroma
		if chroma == 3 {
			r.bits(1)
		}
		out.depth = 8 + r.ue() // bit_depth_luma
		r.ue()                 // bit_depth_chroma
		r.bits(1)
		if r.bits(1) == 1 { // seq_scaling_matrix_present
			lists := 8
			if chroma == 3 {
				lists = 12
			}
			for i := range lists {
				if r.bits(1) == 0 {
					continue
				}
				n := 16
				if i >= 6 {
					n = 64
				}
				last, next := 8, 8
				for range n {
					if next != 0 {
						next = (last + r.se() + 256) % 256
					}
					if next != 0 {
						last = next
					}
				}
			}
		}
	}
	if r.over {
		return paramSet{}, false
	}
	// What follows tells nothing more of how pictures are sampled: a set that cannot
	// be read on still tells that, without its signal type.
	sampled := paramSet{chroma: out.chroma, depth: out.depth}
	r.ue() // log2_max_frame_num
	switch r.ue() {
	case 0:
		r.ue()
	case 1:
		r.bits(1)
		r.se()
		r.se()
		cycle := r.ue()
		for range min(cycle, 255) {
			r.se()
		}
	}
	r.ue()    // max_num_ref_frames
	r.bits(1) // gaps_in_frame_num
	r.ue()    // width
	r.ue()    // height
	if r.bits(1) == 0 {
		r.bits(1) // mb_adaptive_frame_field
	}
	r.bits(1)           // direct_8x8_inference
	if r.bits(1) == 1 { // frame_cropping
		r.ue()
		r.ue()
		r.ue()
		r.ue()
	}
	if r.bits(1) == 0 { // vui_parameters_present
		return sampled, true
	}
	if r.bits(1) == 1 { // aspect_ratio_info_present
		if r.bits(8) == 255 {
			r.bits(16)
			r.bits(16)
		}
	}
	if r.bits(1) == 1 { // overscan_info_present
		r.bits(1)
	}
	if r.bits(1) == 0 { // video_signal_type_present
		return sampled, true
	}
	r.bits(3) // video_format
	out.signal = true
	out.full = r.bits(1) == 1
	out.matrix = matrixUnknown
	if r.bits(1) == 1 { // colour_description_present
		r.bits(8)
		r.bits(8)
		out.described = true
		out.matrix = r.bits(8)
	}
	if r.over {
		return sampled, true
	}
	return out, true
}

// bitReader reads bits from the top of each byte; past its end, zeros, and over is set.
type bitReader struct {
	data []byte
	at   int
	over bool
}

func (r *bitReader) bits(n int) int {
	v := 0
	for range n {
		v <<= 1
		if r.at/8 >= len(r.data) {
			r.over = true
			continue
		}
		v |= int(r.data[r.at/8]>>(7-r.at%8)) & 1
		r.at++
	}
	return v
}

// ue reads an unsigned Exp-Golomb code.
func (r *bitReader) ue() int {
	zeros := 0
	for zeros < 32 && !r.over && r.bits(1) == 0 {
		zeros++
	}
	if zeros == 0 || zeros >= 32 {
		return 0
	}
	return 1<<zeros - 1 + r.bits(zeros)
}

// se reads a signed Exp-Golomb code.
func (r *bitReader) se() int {
	v := r.ue()
	if v%2 == 0 {
		return -v / 2
	}
	return (v + 1) / 2
}

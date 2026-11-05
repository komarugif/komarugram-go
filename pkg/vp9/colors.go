// SPDX-License-Identifier: Unlicense OR MIT

package vp9

import "komarugram/pkg/resample"

// packetColors reads how the pictures of a stream of width×height are to be read,
// from the header of a keyframe, packet (VP9's bitstream, 6.2 and 6.2.2); ok is false
// for a packet that does not tell. A stream that leaves the matrix unknown has it
// guessed, as players do.
func packetColors(packet []byte, width, height int) (colors resample.Colors, ok bool) {
	bit := 0
	read := func(n int) int {
		v := 0
		for range n {
			if bit/8 >= len(packet) {
				ok = false
				return 0
			}
			v = v<<1 | int(packet[bit/8]>>(7-bit%8))&1
			bit++
		}
		return v
	}
	ok = true
	if read(2) != 2 { // frame_marker
		return colors, false
	}
	low := read(1)
	profile := read(1)<<1 | low
	if profile == 3 {
		read(1) // reserved_zero
	}
	if read(1) == 1 { // show_existing_frame
		return colors, false
	}
	if read(1) != 0 { // frame_type: 0 is a keyframe
		return colors, false
	}
	read(2)                   // show_frame, error_resilient_mode
	if read(24) != 0x498342 { // frame_sync_code
		return colors, false
	}
	if profile >= 2 {
		read(1) // ten_or_twelve_bit
	}
	space := read(3)
	if !ok {
		return colors, false
	}
	const sRGB = 7
	if space == sRGB {
		// Stored as red, green and blue: nothing a client of this decoder sees, which
		// asks for 4:2:0.
		return resample.Colors{}, true
	}
	full := read(1) == 1
	if !ok {
		return colors, false
	}
	colors.Limited = !full
	switch space {
	case 1, 3: // BT.601, SMPTE 170M
		colors.Matrix = resample.BT601
	case 2, 4: // BT.709, SMPTE 240M, which lies near it
		colors.Matrix = resample.BT709
	case 5: // BT.2020
		colors.Matrix = resample.BT2020
	default:
		colors.Matrix = resample.GuessMatrix(width, height)
	}
	return colors, true
}

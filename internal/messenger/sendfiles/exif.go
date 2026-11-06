// SPDX-License-Identifier: Unlicense OR MIT

package sendfiles

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
)

// orientationOfFile reads the turn a JPEG file asks for in its Exif data:
// 1 for none, up to 8; 1 for a file that is not a JPEG or has none.
func orientationOfFile(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 1
	}
	defer f.Close()
	// The Exif segment comes first and is at most 64 KiB.
	head := make([]byte, 70<<10)
	n, _ := io.ReadFull(f, head)
	return orientationOf(head[:n])
}

// orientationOf reads the orientation tag of the Exif segment of a JPEG.
func orientationOf(data []byte) int {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return 1
	}
	i := 2
	for i+4 <= len(data) {
		if data[i] != 0xff {
			return 1
		}
		marker := data[i+1]
		if marker == 0xff {
			i++
			continue
		}
		// Start of scan or end of image: no Exif after it.
		if marker == 0xda || marker == 0xd9 {
			return 1
		}
		size := int(binary.BigEndian.Uint16(data[i+2:]))
		if size < 2 || i+2+size > len(data) {
			return 1
		}
		segment := data[i+4 : i+2+size]
		if marker == 0xe1 && bytes.HasPrefix(segment, []byte("Exif\x00\x00")) {
			return tiffOrientation(segment[6:])
		}
		i += 2 + size
	}
	return 1
}

// tiffOrientation finds tag 0x0112 in the first directory of Exif's TIFF.
func tiffOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(t[2:]) != 42 {
		return 1
	}
	dir := int(order.Uint32(t[4:]))
	if dir < 8 || dir+2 > len(t) {
		return 1
	}
	count := int(order.Uint16(t[dir:]))
	for k := 0; k < count; k++ {
		entry := dir + 2 + 12*k
		if entry+12 > len(t) {
			return 1
		}
		if order.Uint16(t[entry:]) != 0x0112 {
			continue
		}
		// A SHORT: its value sits in the first two bytes of the value field.
		if v := int(order.Uint16(t[entry+8:])); v >= 1 && v <= 8 {
			return v
		}
		return 1
	}
	return 1
}

// stripJPEGMetadata is the JPEG data without what tells where, when and with
// what it was taken: Exif (with its GPS and its thumbnail), XMP and IPTC, the
// comments and the other application segments, and what comes after the end
// of the image, where phones put more pictures with their own (MPF). The JFIF
// header, the ICC color profile and Adobe's segment, which decoders need for
// the colors, stay, and the picture's own data is not touched. ok is false for
// data that is not a JPEG it can read through.
func stripJPEGMetadata(data []byte) (out []byte, ok bool) {
	if len(data) < 4 || data[0] != 0xff || data[1] != 0xd8 {
		return nil, false
	}
	out = make([]byte, 0, len(data))
	out = append(out, 0xff, 0xd8)
	i := 2
	for i < len(data) {
		if data[i] != 0xff {
			// Entropy-coded data of a scan, kept as it is up to the next
			// marker: a 0xff in it is followed by 0x00 or a restart marker.
			j := i
			for j < len(data) && !(data[j] == 0xff && j+1 < len(data) && data[j+1] != 0x00 && (data[j+1] < 0xd0 || data[j+1] > 0xd7)) {
				j++
			}
			out = append(out, data[i:j]...)
			i = j
			continue
		}
		if i+1 >= len(data) {
			return nil, false
		}
		marker := data[i+1]
		switch {
		case marker == 0xff:
			// Fill byte.
			i++
			continue
		case marker == 0xd9:
			// The end of the image: nothing after it is kept.
			return append(out, 0xff, 0xd9), true
		case marker == 0x00 || marker >= 0xd0 && marker <= 0xd7 || marker == 0x01:
			// Stuffed byte, restart marker, or TEM: no length.
			out = append(out, data[i:i+2]...)
			i += 2
			continue
		}
		if i+4 > len(data) {
			return nil, false
		}
		size := int(binary.BigEndian.Uint16(data[i+2:]))
		if size < 2 || i+2+size > len(data) {
			return nil, false
		}
		segment := data[i+4 : i+2+size]
		if keepJPEGSegment(marker, segment) {
			out = append(out, data[i:i+2+size]...)
		}
		i += 2 + size
	}
	// No end of image: a file cut short is not one to pass on.
	return nil, false
}

// keepJPEGSegment tells whether the segment of marker, its payload, stays: all
// but the application segments and comments, of which only the JFIF and JFXX
// headers (APP0), the ICC profile (APP2) and Adobe's color transform (APP14).
func keepJPEGSegment(marker byte, segment []byte) bool {
	switch {
	case marker == 0xfe:
		return false
	case marker == 0xe0:
		return bytes.HasPrefix(segment, []byte("JFIF\x00")) || bytes.HasPrefix(segment, []byte("JFXX\x00"))
	case marker == 0xe2:
		return bytes.HasPrefix(segment, []byte("ICC_PROFILE\x00"))
	case marker == 0xee:
		return bytes.HasPrefix(segment, []byte("Adobe"))
	case marker >= 0xe0 && marker <= 0xef:
		return false
	}
	return true
}

// SPDX-License-Identifier: Unlicense OR MIT

package webm

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"time"
)

// Matroska element ids only the muxer writes.
const (
	idEBML               = 0x1A45DFA3
	idEBMLVersion        = 0x4286
	idEBMLReadVersion    = 0x42F7
	idEBMLMaxIDLength    = 0x42F2
	idEBMLMaxSizeLength  = 0x42F3
	idDocType            = 0x4282
	idDocTypeVersion     = 0x4287
	idDocTypeReadVersion = 0x4285
	idDuration           = 0x4489
	idMuxingApp          = 0x4D80
	idWritingApp         = 0x5741
	idTrackUID           = 0x73C5
	idFlagLacing         = 0x9C
	idAlphaMode          = 0x53C0
	idReferenceBlock     = 0xFB
)

// MuxFrame is a frame to write: its VP9 packet, the packet of its alpha
// when there is one, when it shows, and whether it is a key frame.
type MuxFrame struct {
	Data, Alpha []byte
	Timestamp   time.Duration
	Key         bool
}

// Mux writes a WebM file of one VP9 video track of width x height frames,
// each frameDuration long, as Telegram's video stickers are: the alpha of a
// frame, when frames have one, in its BlockAdditional, the track marked as
// having it. A cluster starts at each key frame, which Demux and players
// seek by.
func Mux(width, height int, frameDuration time.Duration, frames []MuxFrame) ([]byte, error) {
	if len(frames) == 0 || !frames[0].Key {
		return nil, errors.New("webm: frames must start with a key frame")
	}
	alpha := false
	for _, f := range frames {
		alpha = alpha || len(f.Alpha) > 0
	}
	const scale = time.Millisecond // TimecodeScale 1 000 000 ns
	var out bytes.Buffer
	out.Write(element(idEBML,
		uintElement(idEBMLVersion, 1), uintElement(idEBMLReadVersion, 1),
		uintElement(idEBMLMaxIDLength, 4), uintElement(idEBMLMaxSizeLength, 8),
		element(idDocType, []byte("webm")),
		// BlockAdditions and AlphaMode came with WebM version 4.
		uintElement(idDocTypeVersion, 4), uintElement(idDocTypeReadVersion, 2)))

	last := frames[len(frames)-1].Timestamp + frameDuration
	info := element(idInfo,
		uintElement(idTimecodeScale, uint64(scale)),
		floatElement(idDuration, float64(last)/float64(scale)),
		element(idMuxingApp, []byte("KomaruGram")), element(idWritingApp, []byte("KomaruGram")))
	video := [][]byte{uintElement(idPixelWidth, uint64(width)), uintElement(idPixelHeight, uint64(height))}
	if alpha {
		video = append(video, uintElement(idAlphaMode, 1))
	}
	tracks := element(idTracks, element(idTrackEntry,
		uintElement(idTrackNumber, 1), uintElement(idTrackUID, 1), uintElement(idTrackType, trackTypeVideo),
		uintElement(idFlagLacing, 0), element(idCodecID, []byte("V_VP9")),
		uintElement(idDefaultDuration, uint64(frameDuration)), element(idVideo, video...)))

	// Times are whole milliseconds, each rounded once.
	ms := func(d time.Duration) int64 { return int64((d + scale/2) / scale) }
	var clusters [][]byte
	var blocks [][]byte
	var clusterStart, previous int64
	flush := func() {
		if len(blocks) > 0 {
			clusters = append(clusters, element(idCluster, append([][]byte{uintElement(idTimecode, uint64(clusterStart))}, blocks...)...))
			blocks = nil
		}
	}
	for _, f := range frames {
		at := ms(f.Timestamp)
		if f.Key {
			flush()
			clusterStart = at
		} else if at-clusterStart > 30_000 {
			return nil, errors.New("webm: a cluster longer than 30 s without a key frame")
		}
		relative := at - clusterStart
		block := make([]byte, 0, 4+len(f.Data))
		block = append(block, 0x81) // track 1
		block = binary.BigEndian.AppendUint16(block, uint16(int16(relative)))
		block = append(block, 0) // no lacing
		block = append(block, f.Data...)
		parts := [][]byte{element(idBlock, block)}
		if len(f.Alpha) > 0 {
			parts = append(parts, element(idBlockAdditions, element(idBlockMore,
				uintElement(idBlockAddID, 1), element(idBlockAdditional, f.Alpha))))
		}
		if !f.Key {
			// A block that refers to another is not a key frame.
			parts = append(parts, intElement(idReferenceBlock, previous-at))
		}
		blocks = append(blocks, element(idBlockGroup, parts...))
		previous = at
	}
	flush()
	out.Write(element(idSegment, append([][]byte{info, tracks}, clusters...)...))
	return out.Bytes(), nil
}

// element is an EBML element of id, its payload the parts one after another.
func element(id uint32, parts ...[]byte) []byte {
	size := 0
	for _, p := range parts {
		size += len(p)
	}
	out := make([]byte, 0, 4+8+size)
	switch {
	case id >= 1<<24:
		out = append(out, byte(id>>24), byte(id>>16), byte(id>>8), byte(id))
	case id >= 1<<16:
		out = append(out, byte(id>>16), byte(id>>8), byte(id))
	case id >= 1<<8:
		out = append(out, byte(id>>8), byte(id))
	default:
		out = append(out, byte(id))
	}
	out = appendSize(out, uint64(size))
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// appendSize appends n as an EBML variable-size integer, the shortest one
// (the all-ones values of each length mean "unknown" and are passed over).
func appendSize(out []byte, n uint64) []byte {
	length := 1
	for length < 8 && n >= 1<<(7*length)-1 {
		length++
	}
	v := n | 1<<(7*length)
	for i := length - 1; i >= 0; i-- {
		out = append(out, byte(v>>(8*i)))
	}
	return out
}

func uintElement(id uint32, v uint64) []byte {
	n := 1
	for n < 8 && v >= 1<<(8*n) {
		n++
	}
	b := make([]byte, n)
	for i := range n {
		b[n-1-i] = byte(v >> (8 * i))
	}
	return element(id, b)
}

func intElement(id uint32, v int64) []byte {
	n := 1
	for n < 8 && (v < -(1<<(8*n-1)) || v >= 1<<(8*n-1)) {
		n++
	}
	b := make([]byte, n)
	for i := range n {
		b[n-1-i] = byte(v >> (8 * i))
	}
	return element(id, b)
}

func floatElement(id uint32, v float64) []byte {
	return element(id, binary.BigEndian.AppendUint64(nil, math.Float64bits(v)))
}

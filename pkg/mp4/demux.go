// SPDX-License-Identifier: Unlicense OR MIT

// Package mp4 takes apart the MP4 files Telegram uses for GIFs and animated
// avatars, H.264 video without sound, looped; for video messages, H.264
// video with AAC sound; and M4A files, AAC sound, for voice messages and
// music.
//
// The container is parsed here, in Go, and only the compressed samples are
// handed to the decoder, as the webm package does for stickers. The file
// layout is attacker controlled: every count and offset is checked against
// the data before anything is allocated or read, and a malformed structure
// is an error rather than a panic.
package mp4

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"
)

// MaxSamples bounds the frames of one file. A GIF is at most a minute of
// video at 60 fps; this leaves room for longer loops without letting a
// hostile count allocate much.
const MaxSamples = 1 << 16

// MaxVideoSamples bounds the pictures of a video read where it lies
// (DemuxAt): about 19 hours at 30 fps. Its tables, a few dozen bytes a
// picture, are what a hostile count makes the parser allocate, some 60 MiB.
const MaxVideoSamples = 1 << 21

// MaxAudioSamples bounds the access units of a sound track: about 26 hours
// of AAC at 44.1 kHz. A table entry of 8 bytes can ask for this many
// samples, so it bounds what a hostile file makes the parser allocate too,
// to 16 MiB a table.
const MaxAudioSamples = 1 << 22

// ErrNotMP4 is returned for data that is not an MP4 file at all.
var ErrNotMP4 = errors.New("mp4: not an MP4 file")

// ErrUnsupported is returned for an MP4 file without an H.264 video track.
var ErrUnsupported = errors.New("mp4: no H.264 video track")

// Sample is one coded picture in decoding order, as length-prefixed NAL units.
type Sample struct {
	Data     []byte
	Keyframe bool
}

// File is the H.264 video track of an MP4 file.
type File struct {
	Width, Height int
	// Config is the AVCDecoderConfigurationRecord (avcC): the parameter sets
	// and the length of the NAL unit prefixes of the samples.
	Config []byte
	// Samples are in decoding order, which is also the order to feed them;
	// a file read where it lies (DemuxAt) has none, its samples read with
	// Sample as they are fed.
	Samples []Sample
	// units and keyframes are where the samples of a file read where it
	// lies are, and which are keyframes; file reads them.
	units     []Unit
	keyframes []bool
	file      io.ReaderAt
	// Times are when the pictures show, from the first one, in the order a
	// decoder puts them out: the k-th picture out shows at Times[k].
	Times []time.Duration
	// Duration is how long one loop lasts.
	Duration time.Duration
}

// FrameDuration is the average time a picture shows.
func (f *File) FrameDuration() time.Duration {
	if f.Len() == 0 {
		return 0
	}
	return f.Duration / time.Duration(f.Len())
}

// Len is how many samples the file has.
func (f *File) Len() int {
	if f.file != nil {
		return len(f.units)
	}
	return len(f.Samples)
}

// Keyframe reports whether sample i is a keyframe.
func (f *File) Keyframe(i int) bool {
	if f.file != nil {
		return f.keyframes[i]
	}
	return f.Samples[i].Keyframe
}

// Sample is the data of sample i: in buf, grown as needed, for a file read
// where it lies.
func (f *File) Sample(i int, buf []byte) ([]byte, error) {
	if f.file == nil {
		return f.Samples[i].Data, nil
	}
	u := f.units[i]
	if cap(buf) < int(u.Size) {
		buf = make([]byte, u.Size)
	}
	buf = buf[:u.Size]
	if _, err := f.file.ReadAt(buf, u.Offset); err != nil && !(errors.Is(err, io.EOF) && u.Size == 0) {
		return nil, err
	}
	return buf, nil
}

// track is what is read of one trak box.
type track struct {
	video, audio         bool
	channels             int
	rate                 uint32
	timescale            uint32
	width, height        int
	codec                string
	config               []byte
	deltas, offsets      []uint32 // stts and ctts, expanded per sample
	sizes                []uint32
	chunkOffsets         []uint64
	chunkRuns            []chunkRun
	keyframes            []uint32
	haveOffsets, haveSSS bool

	// long is set for a video read where it lies, which may have up to
	// MaxVideoSamples pictures.
	long bool
}

// chunkRun is an stsc entry: from chunk first on, chunks hold samples each.
type chunkRun struct{ first, samples uint32 }

// Demux parses data and returns its first H.264 video track.
func Demux(data []byte) (*File, error) {
	var moov []byte
	sawFtyp := false
	if err := walk(data, func(kind string, body []byte) error {
		switch kind {
		case "ftyp":
			sawFtyp = true
		case "moov":
			if moov == nil {
				moov = body
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if !sawFtyp && moov == nil {
		return nil, ErrNotMP4
	}
	if moov == nil {
		return nil, fmt.Errorf("mp4: no moov box")
	}
	var found *track
	err := walk(moov, func(kind string, body []byte) error {
		if kind != "trak" || found != nil {
			return nil
		}
		t := &track{}
		if err := parseTrak(body, t); err != nil {
			return err
		}
		if t.video && (t.codec == "avc1" || t.codec == "avc3") && t.config != nil {
			found = t
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, ErrUnsupported
	}
	return found.file(data)
}

// DemuxAt parses the file of size bytes that r reads, and returns its first
// H.264 video track, whose samples are read from r as they are fed: of the
// file it reads the box headers and the movie box, where it is. It takes
// videos of up to MaxVideoSamples pictures.
func DemuxAt(r io.ReaderAt, size int64) (*File, error) {
	moov, err := readMoov(r, size)
	if err != nil {
		return nil, err
	}
	var found *track
	err = walk(moov, func(kind string, body []byte) error {
		if kind != "trak" || found != nil {
			return nil
		}
		t := &track{long: true}
		if err := parseTrak(body, t); err != nil {
			return err
		}
		if t.video && (t.codec == "avc1" || t.codec == "avc3") && t.config != nil {
			found = t
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if found == nil {
		return nil, ErrUnsupported
	}
	units, err := found.units(size)
	if err != nil {
		return nil, err
	}
	file := &File{units: units, keyframes: make([]bool, len(units)), file: r}
	for i := range file.keyframes {
		file.keyframes[i] = !found.haveSSS
	}
	for _, k := range found.keyframes {
		if k >= 1 && int(k) <= len(units) {
			file.keyframes[k-1] = true
		}
	}
	if err := found.timing(file); err != nil {
		return nil, err
	}
	return file, nil
}

// walk calls fn for every box in data, in order.
func walk(data []byte, fn func(kind string, body []byte) error) error {
	for len(data) > 0 {
		if len(data) < 8 {
			return fmt.Errorf("mp4: truncated box header")
		}
		size := uint64(binary.BigEndian.Uint32(data))
		kind := string(data[4:8])
		header := uint64(8)
		switch size {
		case 0:
			size = uint64(len(data))
		case 1:
			if len(data) < 16 {
				return fmt.Errorf("mp4: truncated %q header", kind)
			}
			size, header = binary.BigEndian.Uint64(data[8:]), 16
		}
		if size < header || size > uint64(len(data)) {
			return fmt.Errorf("mp4: %q box of %d bytes in %d", kind, size, len(data))
		}
		if err := fn(kind, data[header:size]); err != nil {
			return err
		}
		data = data[size:]
	}
	return nil
}

// fullBox strips the version and flags of a full box.
func fullBox(body []byte) (version byte, rest []byte, err error) {
	if len(body) < 4 {
		return 0, nil, fmt.Errorf("mp4: truncated full box")
	}
	return body[0], body[4:], nil
}

func parseTrak(body []byte, t *track) error {
	return walk(body, func(kind string, body []byte) error {
		if kind != "mdia" {
			return nil
		}
		return walk(body, func(kind string, body []byte) error {
			switch kind {
			case "mdhd":
				version, rest, err := fullBox(body)
				if err != nil {
					return err
				}
				// Creation and modification times come before the timescale.
				at := 8
				if version == 1 {
					at = 16
				}
				if len(rest) < at+4 {
					return fmt.Errorf("mp4: truncated mdhd")
				}
				t.timescale = binary.BigEndian.Uint32(rest[at:])
			case "hdlr":
				_, rest, err := fullBox(body)
				if err != nil {
					return err
				}
				if len(rest) < 8 {
					return fmt.Errorf("mp4: truncated hdlr")
				}
				t.video = string(rest[4:8]) == "vide"
				t.audio = string(rest[4:8]) == "soun"
			case "minf":
				return walk(body, func(kind string, body []byte) error {
					if kind != "stbl" {
						return nil
					}
					return walk(body, func(kind string, body []byte) error {
						return parseTable(kind, body, t)
					})
				})
			}
			return nil
		})
	})
}

// entries reads the entry count of a table and checks that count entries of
// size bytes each fit in what follows it.
func entries(rest []byte, size int) (uint32, []byte, error) {
	if len(rest) < 4 {
		return 0, nil, fmt.Errorf("mp4: truncated table")
	}
	n := binary.BigEndian.Uint32(rest)
	rest = rest[4:]
	if uint64(n)*uint64(size) > uint64(len(rest)) {
		return 0, nil, fmt.Errorf("mp4: %d entries of %d bytes in %d", n, size, len(rest))
	}
	return n, rest, nil
}

// limit is how many samples t may have: MaxSamples, MaxAudioSamples for
// a sound track.
func (t *track) limit() uint32 {
	switch {
	case t.audio:
		return MaxAudioSamples
	case t.long:
		return MaxVideoSamples
	}
	return MaxSamples
}

// expand appends count copies of value, within limit.
func expand(out []uint32, count, value, limit uint32) ([]uint32, error) {
	if uint64(len(out))+uint64(count) > uint64(limit) {
		return nil, fmt.Errorf("mp4: more than %d samples", limit)
	}
	for range count {
		out = append(out, value)
	}
	return out, nil
}

func parseTable(kind string, body []byte, t *track) error {
	switch kind {
	case "stsd":
		_, rest, err := fullBox(body)
		if err != nil {
			return err
		}
		if len(rest) < 4 {
			return fmt.Errorf("mp4: truncated stsd")
		}
		return walk(rest[4:], func(kind string, body []byte) error {
			if t.codec != "" {
				return nil
			}
			t.codec = kind
			if kind == "mp4a" {
				return parseMP4A(body, t)
			}
			if kind != "avc1" && kind != "avc3" {
				return nil
			}
			// A VisualSampleEntry: 8 bytes of SampleEntry, 16 reserved, the
			// size, 50 more bytes, then its own boxes.
			if len(body) < 78 {
				return fmt.Errorf("mp4: truncated %s entry", kind)
			}
			t.width = int(binary.BigEndian.Uint16(body[24:]))
			t.height = int(binary.BigEndian.Uint16(body[26:]))
			return walk(body[78:], func(kind string, body []byte) error {
				if kind == "avcC" && t.config == nil {
					if len(body) < 7 || body[0] != 1 {
						return fmt.Errorf("mp4: bad avcC")
					}
					t.config = body
				}
				return nil
			})
		})
	case "stts", "ctts":
		_, rest, err := fullBox(body)
		if err != nil {
			return err
		}
		n, rest, err := entries(rest, 8)
		if err != nil {
			return err
		}
		out := &t.deltas
		if kind == "ctts" {
			out = &t.offsets
			t.haveOffsets = true
		}
		for i := range n {
			count := binary.BigEndian.Uint32(rest[i*8:])
			// Composition offsets are read as signed, as version 1 has them
			// and as muxers write them in version 0 too.
			value := binary.BigEndian.Uint32(rest[i*8+4:])
			if *out, err = expand(*out, count, value, t.limit()); err != nil {
				return err
			}
		}
	case "stsz":
		_, rest, err := fullBox(body)
		if err != nil {
			return err
		}
		if len(rest) < 8 {
			return fmt.Errorf("mp4: truncated stsz")
		}
		fixed := binary.BigEndian.Uint32(rest)
		if fixed != 0 {
			t.sizes, err = expand(nil, binary.BigEndian.Uint32(rest[4:]), fixed, t.limit())
			return err
		}
		n, rest, err := entries(rest[4:], 4)
		if err != nil {
			return err
		}
		if n > t.limit() {
			return fmt.Errorf("mp4: more than %d samples", t.limit())
		}
		t.sizes = make([]uint32, n)
		for i := range n {
			t.sizes[i] = binary.BigEndian.Uint32(rest[i*4:])
		}
	case "stco", "co64":
		_, rest, err := fullBox(body)
		if err != nil {
			return err
		}
		size := 4
		if kind == "co64" {
			size = 8
		}
		n, rest, err := entries(rest, size)
		if err != nil {
			return err
		}
		if n > t.limit() {
			return fmt.Errorf("mp4: more than %d chunks", t.limit())
		}
		t.chunkOffsets = make([]uint64, n)
		for i := range n {
			if size == 4 {
				t.chunkOffsets[i] = uint64(binary.BigEndian.Uint32(rest[i*4:]))
			} else {
				t.chunkOffsets[i] = binary.BigEndian.Uint64(rest[i*8:])
			}
		}
	case "stsc":
		_, rest, err := fullBox(body)
		if err != nil {
			return err
		}
		n, rest, err := entries(rest, 12)
		if err != nil {
			return err
		}
		if n > t.limit() {
			return fmt.Errorf("mp4: more than %d chunk runs", t.limit())
		}
		t.chunkRuns = make([]chunkRun, n)
		for i := range n {
			t.chunkRuns[i] = chunkRun{binary.BigEndian.Uint32(rest[i*12:]), binary.BigEndian.Uint32(rest[i*12+4:])}
		}
	case "stss":
		_, rest, err := fullBox(body)
		if err != nil {
			return err
		}
		n, rest, err := entries(rest, 4)
		if err != nil {
			return err
		}
		if n > t.limit() {
			return fmt.Errorf("mp4: more than %d keyframes", t.limit())
		}
		t.haveSSS = true
		t.keyframes = make([]uint32, n)
		for i := range n {
			t.keyframes[i] = binary.BigEndian.Uint32(rest[i*4:])
		}
	}
	return nil
}

// file lays the samples of t out over data.
func (t *track) file(data []byte) (*File, error) {
	if len(t.sizes) == 0 {
		return nil, fmt.Errorf("mp4: no samples")
	}
	file := &File{Samples: make([]Sample, 0, len(t.sizes))}
	samples, err := t.samples(data)
	if err != nil {
		return nil, err
	}
	for _, b := range samples {
		file.Samples = append(file.Samples, Sample{Data: b, Keyframe: !t.haveSSS})
	}
	for _, k := range t.keyframes {
		if k >= 1 && int(k) <= len(file.Samples) {
			file.Samples[k-1].Keyframe = true
		}
	}
	if err := t.timing(file); err != nil {
		return nil, err
	}
	return file, nil
}

// timing gives file, whose samples are laid out, the size, the decoder's
// configuration and the times of t.
func (t *track) timing(file *File) error {
	n := len(t.sizes)
	if n == 0 {
		return fmt.Errorf("mp4: no samples")
	}
	if t.timescale == 0 {
		return fmt.Errorf("mp4: no timescale")
	}
	if len(t.deltas) < n || t.haveOffsets && len(t.offsets) < n {
		return fmt.Errorf("mp4: timing covers %d of %d samples", len(t.deltas), n)
	}
	if t.width <= 0 || t.height <= 0 {
		return fmt.Errorf("mp4: size %dx%d", t.width, t.height)
	}
	file.Width, file.Height, file.Config = t.width, t.height, t.config
	if !file.Keyframe(0) {
		return fmt.Errorf("mp4: the first sample is not a keyframe")
	}

	// A decoder puts pictures out in presentation order: sorted
	// presentation times are the times of its output, in turn.
	pts := make([]int64, n)
	var dts, end int64
	for i := range n {
		pts[i] = dts
		if t.haveOffsets {
			pts[i] += int64(int32(t.offsets[i]))
		}
		dts += int64(t.deltas[i])
		end = max(end, pts[i]+int64(t.deltas[i]))
	}
	slices.Sort(pts)
	// Hostile tables may add up to more than time.Duration holds in
	// nanoseconds; floating point saturates instead of wrapping.
	scale := func(v int64) time.Duration {
		return time.Duration(float64(v) / float64(t.timescale) * float64(time.Second))
	}
	file.Times = make([]time.Duration, n)
	for i, p := range pts {
		file.Times[i] = scale(p - pts[0])
	}
	file.Duration = scale(end - pts[0])
	if file.Duration <= 0 {
		return fmt.Errorf("mp4: no duration")
	}
	return nil
}

// Unit is where a sample lies in the file.
type Unit struct {
	Offset int64
	Size   uint32
}

// units lays the samples of t out over a file of size bytes: they lie in
// chunks, which stsc says how many each holds.
func (t *track) units(size int64) ([]Unit, error) {
	n := len(t.sizes)
	if len(t.chunkRuns) == 0 || t.chunkRuns[0].first != 1 {
		return nil, fmt.Errorf("mp4: bad stsc")
	}
	out := make([]Unit, 0, n)
	for i, run := range t.chunkRuns {
		last := uint32(len(t.chunkOffsets))
		if i+1 < len(t.chunkRuns) {
			last = t.chunkRuns[i+1].first - 1
		}
		if run.first == 0 || last < run.first-1 || last > uint32(len(t.chunkOffsets)) || run.samples == 0 {
			return nil, fmt.Errorf("mp4: bad stsc entry %d", i)
		}
		for chunk := run.first; chunk <= last && len(out) < n; chunk++ {
			offset := t.chunkOffsets[chunk-1]
			for range run.samples {
				if len(out) >= n {
					break
				}
				length := uint64(t.sizes[len(out)])
				if offset > uint64(size) || length > uint64(size)-offset {
					return nil, fmt.Errorf("mp4: sample %d lies outside the file", len(out))
				}
				out = append(out, Unit{Offset: int64(offset), Size: uint32(length)})
				offset += length
			}
		}
	}
	if len(out) < n {
		return nil, fmt.Errorf("mp4: chunks hold %d of %d samples", len(out), n)
	}
	return out, nil
}

// samples lays the samples of t out over data.
func (t *track) samples(data []byte) ([][]byte, error) {
	units, err := t.units(int64(len(data)))
	if err != nil {
		return nil, err
	}
	out := make([][]byte, len(units))
	for i, u := range units {
		out[i] = data[u.Offset : u.Offset+int64(u.Size)]
	}
	return out, nil
}

// ErrNoAudio is returned for an MP4 file without a sound track.
var ErrNoAudio = errors.New("mp4: no sound track")

// ErrUnsupportedAudio is returned for an MP4 file whose sound track is not
// AAC.
var ErrUnsupportedAudio = errors.New("mp4: no AAC sound track")

// Audio is the AAC sound track of an MP4 (M4A) file, read from where the
// file is, as it plays: the file need not be all there.
type Audio struct {
	// Config is the AudioSpecificConfig of the decoder, from esds.
	Config []byte
	// Channels and Rate are what the sample entry states; the decoder
	// tells what it puts out, which SBR can double.
	Channels int
	Rate     uint32
	// Units are where the access units lie, in order; Starts[i] is where
	// unit i starts, in Timescale units from the first, and Starts has one
	// more entry, the end.
	Units     []Unit
	Starts    []int64
	Timescale uint32
	file      io.ReaderAt
}

// Unit reads access unit i into buf, grown as needed.
func (a *Audio) Unit(i int, buf []byte) ([]byte, error) {
	u := a.Units[i]
	if cap(buf) < int(u.Size) {
		buf = make([]byte, u.Size)
	}
	buf = buf[:u.Size]
	if _, err := a.file.ReadAt(buf, u.Offset); err != nil && !(errors.Is(err, io.EOF) && u.Size == 0) {
		return nil, err
	}
	return buf, nil
}

// Duration is how long the track plays.
func (a *Audio) Duration() time.Duration {
	return time.Duration(float64(a.Starts[len(a.Starts)-1]) / float64(a.Timescale) * float64(time.Second))
}

// maxMoov bounds the movie box read into memory: the tables of a day of
// sound fit.
const maxMoov = 64 << 20

// DemuxAudio parses data and returns its first AAC sound track.
func DemuxAudio(data []byte) (*Audio, error) {
	return DemuxAudioAt(bytes.NewReader(data), int64(len(data)))
}

// DemuxAudioAt parses the file of size bytes that r reads, and returns its
// first AAC sound track. Of the file it reads the box headers and the
// movie box, where it is, the start or the end.
func DemuxAudioAt(r io.ReaderAt, size int64) (*Audio, error) {
	moov, err := readMoov(r, size)
	if err != nil {
		return nil, err
	}
	var found *track
	sound := false
	err = walk(moov, func(kind string, body []byte) error {
		if kind != "trak" || found != nil {
			return nil
		}
		t := &track{}
		if err := parseTrak(body, t); err != nil {
			return err
		}
		sound = sound || t.audio
		if t.audio && t.codec == "mp4a" && t.config != nil {
			found = t
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if found == nil && sound {
		return nil, ErrUnsupportedAudio
	}
	if found == nil {
		return nil, ErrNoAudio
	}
	t := found
	if len(t.sizes) == 0 || t.timescale == 0 || len(t.deltas) < len(t.sizes) {
		return nil, fmt.Errorf("mp4: bad sound track")
	}
	units, err := t.units(size)
	if err != nil {
		return nil, err
	}
	a := &Audio{Config: t.config, Channels: t.channels, Rate: t.rate, Units: units, Starts: make([]int64, len(units)+1), Timescale: t.timescale, file: r}
	for i := range units {
		a.Starts[i+1] = a.Starts[i] + int64(t.deltas[i])
	}
	if a.Starts[len(units)] <= 0 {
		return nil, fmt.Errorf("mp4: no duration")
	}
	return a, nil
}

// readMoov reads the movie box of the file of size bytes that r reads,
// where it is, the start or the end, and the box headers before it.
func readMoov(r io.ReaderAt, size int64) ([]byte, error) {
	var moov []byte
	sawFtyp := false
	for at := int64(0); at < size; {
		var h [16]byte
		n, err := r.ReadAt(h[:min(16, size-at)], at)
		if n < 8 {
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return nil, fmt.Errorf("mp4: box header: %w", err)
		}
		length, header, kind := int64(binary.BigEndian.Uint32(h[:])), int64(8), string(h[4:8])
		switch length {
		case 0:
			length = size - at
		case 1:
			if n < 16 {
				return nil, fmt.Errorf("mp4: truncated %q header", kind)
			}
			length, header = int64(binary.BigEndian.Uint64(h[8:])), 16
		}
		if length < header || length > size-at {
			if !sawFtyp {
				return nil, ErrNotMP4
			}
			return nil, fmt.Errorf("mp4: %q box of %d bytes in %d", kind, length, size-at)
		}
		switch kind {
		case "ftyp":
			sawFtyp = true
		case "moov":
			if length-header > maxMoov {
				return nil, fmt.Errorf("mp4: movie box of %d bytes", length-header)
			}
			moov = make([]byte, length-header)
			if _, err := r.ReadAt(moov, at+header); err != nil && !errors.Is(err, io.EOF) {
				return nil, err
			}
		}
		if moov != nil {
			break
		}
		at += length
	}
	if !sawFtyp && moov == nil {
		return nil, ErrNotMP4
	}
	if moov == nil {
		return nil, fmt.Errorf("mp4: no moov box")
	}
	return moov, nil
}

// parseMP4A reads an AudioSampleEntry, and the AudioSpecificConfig of the
// esds box in it.
func parseMP4A(body []byte, t *track) error {
	// 8 bytes of SampleEntry, then version, revision, vendor, channels,
	// sample size, compression id, packet size and the rate, 16.16; version
	// 1 of QuickTime's adds 16 bytes, version 2 36.
	if len(body) < 28 {
		return fmt.Errorf("mp4: truncated mp4a entry")
	}
	t.channels = int(binary.BigEndian.Uint16(body[16:]))
	t.rate = binary.BigEndian.Uint32(body[24:]) >> 16
	at := 28
	switch binary.BigEndian.Uint16(body[8:]) {
	case 1:
		at += 16
	case 2:
		at += 36
	}
	if len(body) < at {
		return fmt.Errorf("mp4: truncated mp4a entry")
	}
	return walk(body[at:], func(kind string, body []byte) error {
		if kind != "esds" || t.config != nil {
			return nil
		}
		_, rest, err := fullBox(body)
		if err != nil {
			return err
		}
		t.config, err = audioConfig(rest)
		if errors.Is(err, ErrUnsupportedAudio) {
			// Not AAC: the track is left out.
			return nil
		}
		return err
	})
}

// audioConfig finds the DecoderSpecificInfo, the AudioSpecificConfig, in
// the descriptors of an esds box (ISO/IEC 14496-1): ES_Descriptor (3)
// holds DecoderConfigDescriptor (4), which holds it (5).
func audioConfig(data []byte) ([]byte, error) {
	for len(data) > 0 {
		tag, body, rest, err := descriptor(data)
		if err != nil {
			return nil, err
		}
		switch tag {
		case 3:
			// ES_ID, then flags that tell what optional fields follow.
			if len(body) < 3 {
				return nil, fmt.Errorf("mp4: truncated ES descriptor")
			}
			flags, at := body[2], 3
			if flags&0x80 != 0 {
				at += 2
			}
			if flags&0x40 != 0 {
				if len(body) <= at {
					return nil, fmt.Errorf("mp4: truncated ES descriptor")
				}
				at += 1 + int(body[at])
			}
			if flags&0x20 != 0 {
				at += 2
			}
			if len(body) < at {
				return nil, fmt.Errorf("mp4: truncated ES descriptor")
			}
			return audioConfig(body[at:])
		case 4:
			// Object type, stream type, buffer size and bit rates: 13 bytes.
			// 0x40 is MPEG-4 audio, 0x66 to 0x68 MPEG-2 AAC.
			if len(body) < 13 {
				return nil, fmt.Errorf("mp4: truncated decoder config")
			}
			if body[0] != 0x40 && (body[0] < 0x66 || body[0] > 0x68) {
				return nil, ErrUnsupportedAudio
			}
			return audioConfig(body[13:])
		case 5:
			if len(body) == 0 {
				return nil, fmt.Errorf("mp4: empty AudioSpecificConfig")
			}
			return body, nil
		}
		data = rest
	}
	return nil, fmt.Errorf("mp4: no AudioSpecificConfig")
}

// descriptor reads one descriptor: its tag, body and what follows it. Its
// size takes up to four bytes of seven bits each.
func descriptor(data []byte) (tag byte, body, rest []byte, err error) {
	if len(data) < 2 {
		return 0, nil, nil, fmt.Errorf("mp4: truncated descriptor")
	}
	tag = data[0]
	size, at := 0, 1
	for range 4 {
		if at >= len(data) {
			return 0, nil, nil, fmt.Errorf("mp4: truncated descriptor")
		}
		b := data[at]
		at++
		size = size<<7 | int(b&0x7f)
		if b&0x80 == 0 {
			break
		}
	}
	if size > len(data)-at {
		return 0, nil, nil, fmt.Errorf("mp4: descriptor of %d bytes in %d", size, len(data)-at)
	}
	return tag, data[at : at+size], data[at+size:], nil
}

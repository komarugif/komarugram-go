// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"fmt"
	"image"
	"os"
	"strconv"
	"strings"
	"time"
)

// RowInput is a video of a row that a player plays as one picture
// (NewRowPlayer).
type RowInput struct {
	// Path is the file or URL FFmpeg reads, where File is nil.
	Path string
	// File is a file handed to FFmpeg open (MemoryFile), which it reads
	// itself: nothing goes through this process, loop after loop.
	File *os.File
	// Width is the width of its tile in the row's picture.
	Width int
	// Source is the size of its pictures, for the memory FFmpeg may take.
	Source image.Point
}

// NewRowPlayer prepares a player of a row of videos, each covering its
// tile, height high, cropped to it, and all side by side in one picture,
// at fps: one FFmpeg for the row, not one for each video. The videos loop
// for ever, each its own length. Frame gives the row's picture; a tile's
// is at the sum of the widths before it.
func NewRowPlayer(inputs []RowInput, height int, fps float64, ffmpeg string) *Player {
	width := 0
	for _, in := range inputs {
		width += in.Width
	}
	return &Player{
		ffmpeg:   ResolveFFmpeg(ffmpeg),
		info:     Info{Width: width, Height: height, FPS: fps},
		row:      inputs,
		lastPoll: time.Now(),
		resume:   make(chan struct{}),
	}
}

// RowMemory is the most an FFmpeg playing a row of videos of the sizes of
// sources may take: one program, the pictures of every decoder.
func RowMemory(sources []image.Point) uint64 {
	memory := uint64(baseMemory)
	for _, s := range sources {
		memory += uint64(max(s.X, 1)) * uint64(max(s.Y, 1)) * pixelMemory
	}
	return memory
}

// rowArgs are the arguments of the FFmpeg of a row, and the files it is
// handed, at the descriptors after the standard ones.
func (p *Player) rowArgs() ([]string, []*os.File, uint64) {
	sources := make([]image.Point, len(p.row))
	for i, in := range p.row {
		sources[i] = in.Source
	}
	memory := RowMemory(sources)
	args := append(LimitArgs(memory), "-hide_banner", "-loglevel", "error")
	var files []*os.File
	for _, in := range p.row {
		path := in.Path
		if in.File != nil {
			// The child has it at the descriptor after those before it.
			path = "/proc/self/fd/" + strconv.Itoa(3+len(files))
			files = append(files, in.File)
		}
		// Each paced at its own rate, a decoding thread each: the
		// threads' stacks count in the bound of the memory.
		args = append(args, "-re", "-threads", "1", "-format_whitelist", "mov,matroska,webm,gif",
			"-stream_loop", "-1", "-i", path)
	}
	var graph strings.Builder
	fps := strconv.FormatFloat(p.info.FPS, 'f', 3, 64)
	for i, in := range p.row {
		// Scaled while still YCbCr to cover the tile, then cropped to it.
		fmt.Fprintf(&graph, "[%d:v]scale=%d:%d:force_original_aspect_ratio=increase:force_divisible_by=2,format=rgba,crop=%d:%d,setsar=1,fps=%s[v%d];",
			i, in.Width, p.info.Height, in.Width, p.info.Height, fps, i)
	}
	for i := range p.row {
		fmt.Fprintf(&graph, "[v%d]", i)
	}
	if len(p.row) == 1 {
		graph.WriteString("null[row]")
	} else {
		fmt.Fprintf(&graph, "hstack=inputs=%d[row]", len(p.row))
	}
	args = append(args, "-filter_complex_threads", "1", "-filter_complex", graph.String(), "-map", "[row]", "-an")
	args = append(args, rawOutputArgs("rgba")...)
	return args, files, memory
}

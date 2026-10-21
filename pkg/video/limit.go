// SPDX-License-Identifier: Unlicense OR MIT

package video

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"os/exec"
	"strconv"
	"sync/atomic"

	"komarugram/pkg/program"
)

// The memory an FFmpeg or ffprobe that reads a stranger's file may take
// (program.StartLimited): a hostile file must not take the machine's. A
// decoder keeps its reference pictures, those its threads work on, and
// those of the filters, a few dozen bytes a pixel of the source at most,
// over what the program itself takes.
const (
	// ProbeMemory bounds ffprobe, and an FFmpeg that decodes sound.
	ProbeMemory = 256 << 20
	// baseMemory is what a decoding FFmpeg takes besides its pictures.
	baseMemory = 384 << 20
	// pixelMemory is how much a decoding FFmpeg may take a pixel of the
	// source.
	pixelMemory = 64
)

// DecodeMemory is the most an FFmpeg decoding the pictures of a source of
// size may take.
func DecodeMemory(size image.Point) uint64 {
	return baseMemory + uint64(max(size.X, 1))*uint64(max(size.Y, 1))*pixelMemory
}

// LimitArgs are the arguments that bound a single allocation of FFmpeg's to
// memory too (-max_alloc, every FFmpeg's): where the system only watches
// a program's memory, one allocation of more than it never gets made.
func LimitArgs(memory uint64) []string {
	// FFmpeg reads it as a long, 32 bits on Windows.
	return []string{"-max_alloc", strconv.FormatUint(min(memory, 1<<31-1), 10)}
}

// limited is an FFmpeg started with its memory bounded, and whether it
// was killed for taking more.
type limited struct {
	memory   uint64
	exceeded atomic.Bool
}

// startLimited starts cmd with its memory bounded to memory.
func startLimited(cmd *exec.Cmd, memory uint64) (*limited, error) {
	l := &limited{memory: memory}
	if err := program.StartLimited(cmd, program.Limits{Memory: memory}, func() { l.exceeded.Store(true) }); err != nil {
		return nil, err
	}
	return l, nil
}

// explain is err, or ErrMemory with the bound when the program was killed
// for taking more.
func (l *limited) explain(err error) error {
	if l != nil && l.exceeded.Load() {
		return fmt.Errorf("%w (%d MiB)", program.ErrMemory, l.memory>>20)
	}
	return err
}

// outputLimited is cmd.Output, cmd's memory bounded to memory.
func outputLimited(cmd *exec.Cmd, memory uint64) ([]byte, error) {
	var out, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &stderr
	l, err := startLimited(cmd, memory)
	if err != nil {
		return nil, err
	}
	if err := cmd.Wait(); err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			exit.Stderr = stderr.Bytes()
		}
		return out.Bytes(), l.explain(err)
	}
	return out.Bytes(), nil
}

// SPDX-License-Identifier: Unlicense OR MIT

// Package ratex lays out LaTeX formulas as KaTeX does, and draws them with
// Gio.
//
// The layout is RaTeX (https://github.com/erweixin/RaTeX, MIT), a Rust
// typesetter that follows KaTeX, compiled to WebAssembly (build/) and run by
// wazero. Formulas come from strangers, in rich messages and Markdown, so
// they are laid out in a sandbox: the module imports nothing, so it cannot
// reach the filesystem, the network or the host's memory, and its memory
// and time are bounded. It hands back a display list (List): glyphs of
// KaTeX's fonts, lines, rectangles and paths, in em. The fonts, under the
// SIL Open Font License, are embedded here (fonts/), and Draw draws a list
// from their outlines.
package ratex

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"komarugram/pkg/sandbox"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
)

//go:embed ratex.wasm.gz
var moduleGz []byte

// Limits bounds what a hostile formula can make the layout take.
type Limits struct {
	sandbox.Limits
	// MaxSource is the longest formula laid out, in bytes.
	MaxSource int
	// MaxOutput is the largest display list read back, in bytes.
	MaxOutput int
	// Fresh is the size of the module's memory past which its sandbox is
	// dropped after a formula: WebAssembly memory never shrinks.
	Fresh uint64
}

// DefaultLimits suit the formulas of messages. RaTeX bounds its recursion
// and macro expansion itself: hostile formulas measured stay under 40 MB.
var DefaultLimits = Limits{
	Limits:    sandbox.Limits{Memory: 64 << 20, Slow: time.Second},
	MaxSource: 16 << 10,
	MaxOutput: 8 << 20,
	Fresh:     16 << 20,
}

// ErrTooLong is returned for a formula longer than Limits.MaxSource.
var ErrTooLong = errors.New("ratex: formula too long")

// Runtime lays formulas out, one at a time, in one sandbox it keeps until
// a formula makes its memory grow past Limits.Fresh.
type Runtime struct {
	limits   Limits
	mu       sync.Mutex
	sandbox  *sandbox.Runtime
	compiled wazero.CompiledModule
	module   api.Module
}

// NewRuntime compiles the module under limits.
func NewRuntime(ctx context.Context, limits Limits) (*Runtime, error) {
	sb, err := sandbox.NewRuntime(ctx, limits.Limits)
	if err != nil {
		return nil, err
	}
	compiled, err := sb.CompileGzipModule(ctx, moduleGz)
	if err != nil {
		sb.Close(ctx)
		return nil, err
	}
	return &Runtime{limits: limits, sandbox: sb, compiled: compiled}, nil
}

// Close lets the sandbox and the compiled module go.
func (r *Runtime) Close(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.module = nil
	return r.sandbox.Close(ctx)
}

// Error is a formula RaTeX could not lay out, with its message.
type Error struct{ Message string }

func (e *Error) Error() string { return "ratex: " + e.Message }

// Layout lays out source, a formula of LaTeX without its dollars, in
// display style when display is set and in text style otherwise. A formula
// RaTeX cannot read gives an *Error.
func (r *Runtime) Layout(ctx context.Context, source string, display bool) (*List, error) {
	if len(source) > r.limits.MaxSource {
		return nil, ErrTooLong
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.module == nil {
		m, err := r.sandbox.Instantiate(ctx, r.compiled, wazero.NewModuleConfig().WithName(""))
		if err != nil {
			return nil, err
		}
		r.module = m
	}
	m := r.module
	started := time.Now()
	out, code, err := r.call(ctx, m, source, display)
	if err == nil {
		err = r.sandbox.Check(ctx, m, started)
	}
	if err != nil || m.Memory().Size() > uint32(min(r.limits.Fresh, 1<<32-1)) {
		// A trap, a slow formula or a large one: the next formula gets a
		// fresh sandbox.
		m.Close(ctx)
		r.module = nil
	}
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, &Error{Message: string(out)}
	}
	var list List
	if err := json.Unmarshal(out, &list); err != nil {
		return nil, fmt.Errorf("ratex: display list: %w", err)
	}
	return &list, nil
}

// call writes source into the sandbox, lays it out, and copies back the
// output and its code.
func (r *Runtime) call(ctx context.Context, m api.Module, source string, display bool) ([]byte, uint64, error) {
	fn := func(name string, args ...uint64) (uint64, error) {
		f := m.ExportedFunction(name)
		if f == nil {
			return 0, fmt.Errorf("ratex: no %s in the module", name)
		}
		res, err := f.Call(ctx, args...)
		if err != nil {
			return 0, err
		}
		if len(res) == 0 {
			return 0, nil
		}
		return res[0], nil
	}
	n := uint64(len(source))
	ptr, err := fn("rt_alloc", max(n, 1))
	if err != nil {
		return nil, 0, err
	}
	if !m.Memory().Write(uint32(ptr), []byte(source)) {
		return nil, 0, errors.New("ratex: formula out of the module's memory")
	}
	flag := uint64(0)
	if display {
		flag = 1
	}
	code, err := fn("rt_layout", ptr, n, flag)
	if err != nil {
		return nil, 0, err
	}
	if _, err := fn("rt_free", ptr, max(n, 1)); err != nil {
		return nil, 0, err
	}
	at, err := fn("rt_out")
	if err != nil {
		return nil, 0, err
	}
	size, err := fn("rt_out_len")
	if err != nil {
		return nil, 0, err
	}
	if size > uint64(r.limits.MaxOutput) {
		return nil, 0, errors.New("ratex: display list too large")
	}
	out, ok := m.Memory().Read(uint32(at), uint32(size))
	if !ok {
		return nil, 0, errors.New("ratex: display list out of the module's memory")
	}
	return bytes.Clone(out), code, nil
}

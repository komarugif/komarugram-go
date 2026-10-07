// SPDX-License-Identifier: Unlicense OR MIT

// Package cmark parses Markdown with cmark-gfm, the CommonMark and GitHub
// Flavored Markdown parser Telegram Desktop uses, with its extensions and
// options: tables, strikethrough, autolinks, the tag filter, task lists and
// footnotes.
//
// cmark-gfm (BSD-2-Clause, parts MIT; LICENSE.cmark-gfm) is compiled to
// WebAssembly (build/) and run by wazero. Markdown files come from
// strangers, and cmark's worst inputs are quadratic, so it runs in a
// sandbox that bounds its memory and time; a module that imports only
// WASI, with no files, environment or clock given to it. Each Parse runs
// in a fresh instance, whose memory goes with it.
package cmark

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"

	"komarugram/pkg/sandbox"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
)

//go:embed cmark.wasm.gz
var moduleGz []byte

// Limits bounds what a hostile document can make the parser take.
type Limits struct {
	sandbox.Limits
	// MaxSource is the largest document parsed, in bytes; MaxNodes, the
	// most nodes its tree may have, and MaxDepth how deep they may nest.
	MaxSource, MaxNodes, MaxDepth int
}

// DefaultLimits are Telegram Desktop's (ParseLimitsForIv: 4 MB, 100,000
// nodes, nesting 128), and its parser's memory budget of 128 MB, with room
// for the source and the tree written out.
var DefaultLimits = Limits{
	Limits:    sandbox.Limits{Memory: 192 << 20, Slow: 5 * time.Second},
	MaxSource: 4 << 20,
	MaxNodes:  100_000,
	MaxDepth:  128,
}

var (
	// ErrTooLong is a document longer than Limits.MaxSource.
	ErrTooLong = errors.New("cmark: document too long")
	// ErrTooLarge is a document whose tree has more nodes, or nests
	// deeper, than its limits allow.
	ErrTooLarge = errors.New("cmark: document too large")
)

// Runtime parses Markdown, a document at a time.
type Runtime struct {
	limits   Limits
	sandbox  *sandbox.Runtime
	compiled wazero.CompiledModule
}

// NewRuntime compiles the module under limits.
func NewRuntime(ctx context.Context, limits Limits) (*Runtime, error) {
	sb, err := sandbox.NewRuntime(ctx, limits.Limits)
	if err != nil {
		return nil, err
	}
	if _, err := wasi_snapshot_preview1.Instantiate(ctx, sb.Wazero()); err != nil {
		sb.Close(ctx)
		return nil, fmt.Errorf("cmark: wasi: %w", err)
	}
	zr, err := gzip.NewReader(bytes.NewReader(moduleGz))
	if err != nil {
		sb.Close(ctx)
		return nil, err
	}
	binary, err := io.ReadAll(zr)
	if err != nil {
		sb.Close(ctx)
		return nil, err
	}
	compiled, err := sb.CompileModule(ctx, binary)
	if err != nil {
		sb.Close(ctx)
		return nil, err
	}
	return &Runtime{limits: limits, sandbox: sb, compiled: compiled}, nil
}

// Close lets the sandbox and the compiled module go.
func (r *Runtime) Close(ctx context.Context) error {
	return r.sandbox.Close(ctx)
}

// Parse parses source, Markdown, into its tree.
func (r *Runtime) Parse(ctx context.Context, source []byte) (*Node, error) {
	if len(source) > r.limits.MaxSource {
		return nil, ErrTooLong
	}
	m, err := r.sandbox.Instantiate(ctx, r.compiled, wazero.NewModuleConfig().WithName("").WithStartFunctions("_initialize"))
	if err != nil {
		return nil, err
	}
	defer m.Close(ctx)
	started := time.Now()
	out, code, err := r.call(ctx, m, source)
	if err == nil {
		err = r.sandbox.Check(ctx, m, started)
	}
	if err != nil {
		return nil, err
	}
	switch code {
	case 0:
	case 2:
		return nil, ErrTooLarge
	default:
		return nil, errors.New("cmark: the parser failed")
	}
	d := decoder{data: out}
	root := d.node(0)
	if d.err != nil {
		return nil, d.err
	}
	return root, nil
}

func (r *Runtime) call(ctx context.Context, m api.Module, source []byte) ([]byte, uint64, error) {
	fn := func(name string, args ...uint64) (uint64, error) {
		f := m.ExportedFunction(name)
		if f == nil {
			return 0, fmt.Errorf("cmark: no %s in the module", name)
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
	ptr, err := fn("md_alloc", uint64(len(source)))
	if err != nil {
		return nil, 0, err
	}
	if ptr == 0 || !m.Memory().Write(uint32(ptr), source) {
		return nil, 0, errors.New("cmark: document out of the module's memory")
	}
	code, err := fn("md_parse", ptr, uint64(len(source)), uint64(r.limits.MaxNodes), uint64(r.limits.MaxDepth))
	if err != nil {
		return nil, 0, err
	}
	at, err := fn("md_out")
	if err != nil {
		return nil, 0, err
	}
	size, err := fn("md_out_len")
	if err != nil {
		return nil, 0, err
	}
	out, ok := m.Memory().Read(uint32(at), uint32(size))
	if !ok {
		return nil, 0, errors.New("cmark: tree out of the module's memory")
	}
	return bytes.Clone(out), code, nil
}

// decoder reads the tree cmshim.c writes.
type decoder struct {
	data []byte
	err  error
}

func (d *decoder) fail() {
	if d.err == nil {
		d.err = errors.New("cmark: malformed tree")
	}
	d.data = nil
}

func (d *decoder) u8() uint8 {
	if len(d.data) < 1 {
		d.fail()
		return 0
	}
	v := d.data[0]
	d.data = d.data[1:]
	return v
}

func (d *decoder) u16() uint16 {
	if len(d.data) < 2 {
		d.fail()
		return 0
	}
	v := binary.LittleEndian.Uint16(d.data)
	d.data = d.data[2:]
	return v
}

func (d *decoder) u32() uint32 {
	if len(d.data) < 4 {
		d.fail()
		return 0
	}
	v := binary.LittleEndian.Uint32(d.data)
	d.data = d.data[4:]
	return v
}

func (d *decoder) str() string {
	n := d.u32()
	if uint64(n) > uint64(len(d.data)) {
		d.fail()
		return ""
	}
	s := string(d.data[:n])
	d.data = d.data[n:]
	return s
}

// node reads a node and its children; the module has checked the depth.
func (d *decoder) node(depth int) *Node {
	if depth > DefaultLimits.MaxDepth+1 {
		d.fail()
		return nil
	}
	n := &Node{Kind: Kind(d.u8())}
	n.StartLine, n.StartColumn = int(d.u32()), int(d.u32())
	n.EndLine, n.EndColumn = int(d.u32()), int(d.u32())
	children := d.u32()
	switch n.Kind {
	case List:
		n.Ordered = d.u8() != 0
		n.Delimiter = Delimiter(d.u8())
		n.Start = int(d.u32())
		n.Tight = d.u8() != 0
	case Item:
		n.Task = Task(d.u8())
	case Heading:
		n.Level = int(d.u8())
	case CodeBlock:
		n.Info, n.Literal = d.str(), d.str()
	case Text, Code, HTMLBlock, HTMLInline, FootnoteDefinition, FootnoteReference:
		n.Literal = d.str()
	case Link, Image:
		n.URL, n.Title = d.str(), d.str()
	case Table:
		columns := int(d.u16())
		if columns > len(d.data) {
			d.fail()
			return n
		}
		n.Alignments = make([]Alignment, columns)
		for i := range n.Alignments {
			n.Alignments[i] = Alignment(d.u8())
		}
	case TableRow:
		n.Header = d.u8() != 0
	case Unknown:
		n.Type = d.str()
	}
	if uint64(children) > uint64(len(d.data)) {
		d.fail()
		return n
	}
	for range children {
		if d.err != nil {
			break
		}
		n.Children = append(n.Children, d.node(depth+1))
	}
	return n
}

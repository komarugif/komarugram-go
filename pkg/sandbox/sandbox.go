// SPDX-License-Identifier: Unlicense OR MIT

// Package sandbox holds the limits every WebAssembly sandbox of this project
// runs under. The modules decode input that arrives from strangers, so a
// hostile file must not be able to take the client down or starve the rest of
// the system: a sandbox gets a memory cap of its own, a share of a budget it
// splits with its neighbours, and a time limit for every operation.
//
// Running out of memory is an error for that one sandbox: the module sees its
// memory.grow fail, which a C or Rust allocator turns into a failed allocation
// or an abort — a trap that wazero hands back as an error.
//
// Time is checked after the fact. wazero can interrupt a running call, but only
// by compiling a check into every loop and call, which made libvpx twice and
// tlottie five times slower. So a slow operation is allowed to finish, and then
// its sandbox is closed: an input that makes a decoder crawl costs one slow
// frame rather than every frame after it. An operation that never finishes is
// not caught; memory caps bound how much work libvpx can be given, but not
// what a Lottie file can ask of tlottie.
//
// Runtimes share compiled code through a Cache: in memory, so that two
// decoders of one kind compile their module once, and on disk when the
// program sets a NewDiskCache, so that the next start does not compile it
// again. See cache.go for how the disk cache keeps out code it did not
// write.
package sandbox

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/api"
	"github.com/tetratelabs/wazero/experimental"
)

const pageSize = 64 << 10

// Limits bounds what one runtime's sandboxes may take from the host.
type Limits struct {
	// Memory caps the linear memory of one sandbox, in bytes. It is rounded
	// down to whole 64 KiB pages.
	Memory uint64
	// Budget is shared by the sandboxes of every runtime given the same one.
	// A nil Budget gives the runtime a budget of its own, of DefaultBudget.
	Budget *Budget
	// Slow is how long one operation on a sandbox may take. An operation that
	// takes longer fails with ErrSlow, and its sandbox is closed.
	Slow time.Duration
}

// DefaultBudget is the budget a runtime gets when its Limits name none.
const DefaultBudget = 256 << 20

// ErrBudget is returned when a new sandbox would not fit in the budget.
var ErrBudget = errors.New("sandbox: memory budget exhausted")

// ErrSlow is returned for an operation that took longer than Limits.Slow.
var ErrSlow = errors.New("sandbox: operation too slow")

// Runtime is a wazero runtime set up to enforce Limits.
type Runtime struct {
	wazero wazero.Runtime
	limits Limits

	mu       sync.Mutex
	compiled []wazero.CompiledModule
}

// NewRuntime creates a wazero runtime that enforces l.
func NewRuntime(ctx context.Context, l Limits) (*Runtime, error) {
	pages := l.Memory / pageSize
	if pages == 0 || pages > 65536 {
		return nil, fmt.Errorf("sandbox: memory limit %d is out of range", l.Memory)
	}
	if l.Slow <= 0 {
		return nil, fmt.Errorf("sandbox: time limit %v is out of range", l.Slow)
	}
	if l.Budget == nil {
		l.Budget = NewBudget(DefaultBudget)
	}
	// Every runtime has the same features, so they can share one cache:
	// wazero sets up a cache's compiler with the features of the first
	// runtime to use it.
	config := wazero.NewRuntimeConfig().WithMemoryLimitPages(uint32(pages)).WithCompilationCache(currentCache().wazero)
	return &Runtime{wazero: wazero.NewRuntimeWithConfig(ctx, config), limits: l}, nil
}

// Wazero returns the underlying runtime, for setting up host modules.
// Modules must be compiled with CompileModule and sandboxes started with
// Instantiate, which is what charges them to the budget.
func (r *Runtime) Wazero() wazero.Runtime { return r.wazero }

// CompileModule compiles a module, or takes it from the cache. Close lets it
// go: with a shared cache, closing the runtime alone would keep its code in
// memory for the rest of the process.
func (r *Runtime) CompileModule(ctx context.Context, binary []byte) (wazero.CompiledModule, error) {
	compiled, err := r.wazero.CompileModule(ctx, binary)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.compiled = append(r.compiled, compiled)
	r.mu.Unlock()
	return compiled, nil
}

// CompileGzipModule compiles a module embedded gzipped, as the modules of
// this program are: unpacked for the compiling only, so that the program
// carries a third of their size and keeps none of them unpacked.
func (r *Runtime) CompileGzipModule(ctx context.Context, gz []byte) (wazero.CompiledModule, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	binary, err := io.ReadAll(zr)
	if err != nil {
		return nil, err
	}
	return r.CompileModule(ctx, binary)
}

// Close tears down every sandbox of this runtime and lets its compiled
// modules go.
func (r *Runtime) Close(ctx context.Context) error {
	err := r.wazero.Close(ctx)
	r.mu.Lock()
	compiled := r.compiled
	r.compiled = nil
	r.mu.Unlock()
	for _, c := range compiled {
		_ = c.Close(ctx)
	}
	return err
}

// Instantiate starts a sandbox from compiled once the budget has room for the
// memory the module starts with.
func (r *Runtime) Instantiate(ctx context.Context, compiled wazero.CompiledModule, config wazero.ModuleConfig) (api.Module, error) {
	var initial uint64
	for _, memory := range compiled.ExportedMemories() {
		initial += uint64(memory.Min()) * pageSize
	}
	if !r.limits.Budget.fits(initial) {
		return nil, ErrBudget
	}
	// The allocator travels in the context; wazero reads it while creating the
	// module's memory, so this context need not outlive the call.
	ctx = experimental.WithMemoryAllocator(ctx, r.limits.Budget)
	return r.wazero.InstantiateModule(ctx, compiled, config)
}

// Budget returns the budget this runtime's sandboxes draw on.
func (r *Runtime) Budget() *Budget { return r.limits.Budget }

// Check ends one operation on module, which began at started. An operation
// that took longer than the time limit closes module and fails with ErrSlow.
func (r *Runtime) Check(ctx context.Context, module api.Module, started time.Time) error {
	took := time.Since(started)
	if took <= r.limits.Slow {
		return nil
	}
	_ = module.Close(ctx)
	return fmt.Errorf("%w: took %v, limit %v", ErrSlow, took.Round(time.Microsecond), r.limits.Slow)
}

// Budget is a memory allowance shared by many sandboxes. Its zero value is
// not usable; create one with NewBudget.
type Budget struct {
	mu          sync.Mutex
	limit, used uint64
}

// NewBudget returns a budget of the given size in bytes.
func NewBudget(bytes uint64) *Budget {
	return &Budget{limit: bytes}
}

// Used reports how much of the budget the live sandboxes hold.
func (b *Budget) Used() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

func (b *Budget) fits(n uint64) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used+n <= b.limit
}

// take reserves n bytes. A forced reservation always succeeds: wazero cannot
// cope with a module's initial memory being refused, so that is checked
// before instantiation instead and granted here unconditionally.
func (b *Budget) take(n uint64, force bool) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !force && b.used+n > b.limit {
		return false
	}
	b.used += n
	return true
}

func (b *Budget) release(n uint64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used -= min(n, b.used)
}

// Allocate implements experimental.MemoryAllocator.
func (b *Budget) Allocate(_, max uint64) experimental.LinearMemory {
	return &memory{budget: b, max: max}
}

// memory is a linear memory whose capacity is charged to a budget.
type memory struct {
	budget  *Budget
	buf     []byte
	max     uint64
	started bool
}

func (m *memory) Reallocate(size uint64) []byte {
	// Only the first call, which wazero makes for the initial memory, is forced.
	force := !m.started
	m.started = true
	if size > m.max {
		return nil
	}
	if size <= uint64(cap(m.buf)) {
		m.buf = m.buf[:size]
		return m.buf
	}

	// Grow by half again, so that page-by-page growth does not copy the whole
	// memory each time; fall back to the exact size when the budget is tight.
	old := uint64(cap(m.buf))
	grown := min(max(size, old+old/2), m.max)
	if !m.budget.take(grown-old, force) {
		if grown == size || !m.budget.take(size-old, false) {
			return nil
		}
		grown = size
	}
	buf := make([]byte, size, grown)
	copy(buf, m.buf)
	m.buf = buf
	return m.buf
}

func (m *memory) Free() {
	m.budget.release(uint64(cap(m.buf)))
	m.buf = nil
}

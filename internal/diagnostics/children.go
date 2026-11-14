package diagnostics

import (
	"context"
	"maps"
	"slices"
	"sync"
	"time"
)

// ChildProfile is a profile a process of the client's own recorded for a
// capture, as its runtime/pprof or runtime/trace writes it.
type ChildProfile struct {
	// Name goes in the file's name: what the process is.
	Name string
	PID  int
	Data []byte
	Err  error
}

// ChildProfiler records kind in processes of the client's own, for d
// where it lasts, and returns once they are done or ctx is: the video
// decoder's helpers (pkg/hwvideo).
type ChildProfiler func(ctx context.Context, kind string, d time.Duration) []ChildProfile

var (
	childMu        sync.Mutex
	childProfilers = map[string]ChildProfiler{}
)

// SetChildProfiler makes the captures of kinds a process records record
// with f too, under name; nil removes it.
func SetChildProfiler(name string, f ChildProfiler) {
	childMu.Lock()
	defer childMu.Unlock()
	if f == nil {
		delete(childProfilers, name)
		return
	}
	childProfilers[name] = f
}

// profileChildren records kind for d with every child profiler at once.
func profileChildren(ctx context.Context, kind string, d time.Duration) []ChildProfile {
	childMu.Lock()
	names := slices.Sorted(maps.Keys(childProfilers))
	fs := make([]ChildProfiler, len(names))
	for i, name := range names {
		fs[i] = childProfilers[name]
	}
	childMu.Unlock()
	results := make([][]ChildProfile, len(fs))
	var wg sync.WaitGroup
	for i, f := range fs {
		wg.Go(func() { results[i] = f(ctx, kind, d) })
	}
	wg.Wait()
	return slices.Concat(results...)
}

// Package diagnostics collects bounded, opt-in, process-local performance data.
// It never retains request bodies, message text, credentials or raw RPC errors.
package diagnostics

import (
	"fmt"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

const FrameLimit = 600
const EventLimit = 2048
const RPCLimit = 512
const contextKey = "komarugram-go/diagnostics/frame"

var active atomic.Pointer[Recorder]

func Current() *Recorder { return active.Load() }
func Enable() *Recorder {
	r := &Recorder{started: time.Now(), scopes: map[string]string{}, frames: map[string]*ring[Frame]{}, gauges: map[string]Gauge{}, histories: map[string]History{}, due: map[string]time.Time{}, pending: map[uint64]RPC{}, counters: map[string]uint64{}, done: make(chan struct{})}
	if !active.CompareAndSwap(nil, r) {
		return active.Load()
	}
	go r.sample()
	return r
}
func (r *Recorder) Close() {
	if active.CompareAndSwap(r, nil) {
		close(r.done)
		r.exportMu.Lock()
		exporter := r.exporter
		r.exportMu.Unlock()
		if exporter != nil {
			exporter.Stop()
		}
	}
}

type ring[T any] struct {
	values []T
	next   int
	total  uint64
}

func (r *ring[T]) add(v T, limit int) {
	if len(r.values) < limit {
		r.values = append(r.values, v)
	} else {
		r.values[r.next] = v
	}
	r.next = (r.next + 1) % limit
	r.total++
}
func (r *ring[T]) copy() []T {
	out := make([]T, 0, len(r.values))
	if r.total > uint64(len(r.values)) {
		out = append(out, r.values[r.next:]...)
		out = append(out, r.values[:r.next]...)
	} else {
		out = append(out, r.values...)
	}
	return out
}

type Phase struct {
	Name     string
	Duration time.Duration
	Calls    int
}
type Frame struct {
	Width, Height                        int
	PxPerDp, PxPerSp                     float32
	At                                   time.Time
	Window                               string
	Total, Setup, Update, Layout, Submit time.Duration
	Phases                               []Phase
}
type History struct {
	Environment                                                                                               string
	At                                                                                                        time.Time
	Window                                                                                                    string
	Chat                                                                                                      int64
	Messages, Visible, First, Offset, RowsLaidOut, MeasurementsChanged, RowsCached, MeasurementsCached, Dirty int
	Revision                                                                                                  uint64
	SnapshotFresh, Rebuilt                                                                                    bool
	Reason                                                                                                    string
	Restore                                                                                                   string
	RestoreScanned, LayoutHits                                                                                int
	RebuildTime, RestoreTime                                                                                  time.Duration
	TotalHeight                                                                                               int64
	// The rows kept of messages not loaded: let go of above the loaded
	// ones and the reserve's below them, how many the reserve has in all
	// and whether it ended at a row of no height known; the rows laid out
	// off screen and the leaps of the window, since the chat opened; the
	// rows laid out at an end of what is loaded, not kept in the cache; the
	// skeletons whose bubbles are known, and those whose profiles are to
	// be read.
	Above, Below, Reserve         int
	ReserveTaken, ReserveStopped  bool
	OffScreen, Leaps              int
	Unsaved, Shapes, ShapesWanted int
}
type Event struct {
	At                  time.Time
	Scope, Name, Detail string
	Duration            time.Duration
	Count               int
}
type RPC struct {
	ID                          uint64
	At                          time.Time
	Scope, Method               string
	DC                          int
	Duration                    time.Duration
	RequestBytes, ResponseBytes int64
	Encodes                     int64
	Error                       string
	Pending                     bool
}
type Gauge struct {
	Scope, Name string
	Entries     int
	Bytes       int64
	At          time.Time
}
type Memory struct {
	At                                                                                                         time.Time
	HeapAlloc, HeapInuse, HeapIdle, HeapReleased, HeapObjects, TotalAlloc, Sys, StackInuse, NextGC, PauseTotal uint64
	GC                                                                                                         uint32
	Goroutines                                                                                                 int
}
type Snapshot struct {
	SchemaVersion              int
	GoVersion                  string
	GPU                        string
	InFlight                   int
	At, Started                time.Time
	Frames                     map[string][]Frame
	Histories                  []History
	Events                     []Event
	RPCs                       []RPC
	Pending                    []RPC
	Gauges                     []Gauge
	Memory                     []Memory
	Counters                   map[string]uint64
	DroppedEvents, DroppedRPCs uint64
}
type Recorder struct {
	exportMu  sync.Mutex
	exporter  *AutoExport
	captureMu sync.Mutex
	inFlight  int
	mu        sync.Mutex
	started   time.Time
	gpu       string
	scopes    map[string]string
	frames    map[string]*ring[Frame]
	histories map[string]History
	events    ring[Event]
	rpcs      ring[RPC]
	memory    ring[Memory]
	gauges    map[string]Gauge
	due       map[string]time.Time
	pending   map[uint64]RPC
	counters  map[string]uint64
	seq       uint64
	done      chan struct{}
	capture   atomic.Bool
}

// SetGPU records the device and driver windows are drawn with, as the
// graphics API names them (app.GPUInfo), for Snapshot.GPU.
func (r *Recorder) SetGPU(device string) {
	r.mu.Lock()
	r.gpu = device
	r.mu.Unlock()
}

func (r *Recorder) Scope(id string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name := r.scopes[id]; name != "" {
		return name
	}
	name := fmt.Sprintf("account-%d", len(r.scopes)+1)
	r.scopes[id] = name
	return name
}
func (r *Recorder) RecordFrame(f Frame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	q := r.frames[f.Window]
	if q == nil {
		q = &ring[Frame]{}
		r.frames[f.Window] = q
	}
	q.add(f, FrameLimit)
}
func (r *Recorder) History(h History) {
	h.At = time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()
	r.histories[h.Window] = h
}
func (r *Recorder) Event(scope, name, detail string, duration time.Duration, count int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events.add(Event{time.Now(), scope, name, detail, duration, count}, EventLimit)
	r.counters[scope+"/"+name]++
}
func (r *Recorder) Gauge(scope, name string, entries int, bytes int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gauges[scope+"/"+name] = Gauge{scope, name, entries, bytes, time.Now()}
}
func (r *Recorder) Due(scope, name string, interval time.Duration) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := scope + "/" + name
	now := time.Now()
	if now.Sub(r.due[key]) < interval {
		return false
	}
	r.due[key] = now
	return true
}
func (r *Recorder) Snapshot() Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Snapshot{SchemaVersion: 1, GoVersion: runtime.Version(), GPU: r.gpu, InFlight: r.inFlight, At: time.Now(), Started: r.started, Frames: map[string][]Frame{}, Events: r.events.copy(), RPCs: r.rpcs.copy(), Memory: r.memory.copy(), Counters: map[string]uint64{}, DroppedEvents: r.events.total - uint64(len(r.events.values)), DroppedRPCs: r.rpcs.total - uint64(len(r.rpcs.values))}
	for name, frames := range r.frames {
		s.Frames[name] = frames.copy()
	}
	for _, h := range r.histories {
		s.Histories = append(s.Histories, h)
	}
	for _, g := range r.gauges {
		s.Gauges = append(s.Gauges, g)
	}
	for _, rpc := range r.pending {
		s.Pending = append(s.Pending, rpc)
	}
	for k, v := range r.counters {
		s.Counters[k] = v
	}
	sort.Slice(s.Histories, func(i, j int) bool { return s.Histories[i].Window < s.Histories[j].Window })
	sort.Slice(s.Gauges, func(i, j int) bool { return s.Gauges[i].Scope+s.Gauges[i].Name < s.Gauges[j].Scope+s.Gauges[j].Name })
	sort.Slice(s.Pending, func(i, j int) bool { return s.Pending[i].ID < s.Pending[j].ID })
	return s
}
func (r *Recorder) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.started = time.Now()
	r.frames = map[string]*ring[Frame]{}
	r.events = ring[Event]{}
	r.rpcs = ring[RPC]{}
	r.memory = ring[Memory]{}
	r.histories = map[string]History{}
	r.counters = map[string]uint64{}
}
func (r *Recorder) sample() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		sample := Memory{time.Now(), m.HeapAlloc, m.HeapInuse, m.HeapIdle, m.HeapReleased, m.HeapObjects, m.TotalAlloc, m.Sys, m.StackInuse, m.NextGC, m.PauseTotalNs, m.NumGC, runtime.NumGoroutine()}
		r.mu.Lock()
		r.memory.add(sample, 300)
		r.mu.Unlock()
		select {
		case <-r.done:
			return
		case <-ticker.C:
		}
	}
}

// Trace belongs to one UI frame; only that window's goroutine mutates it.
type Trace struct {
	Recorder *Recorder
	Window   string
	phases   map[string]Phase
	History  History
}

func NewTrace(r *Recorder, window string, values map[string]any) *Trace {
	t := &Trace{Recorder: r, Window: window, phases: map[string]Phase{}}
	values[contextKey] = t
	return t
}
func From(values map[string]any) *Trace { t, _ := values[contextKey].(*Trace); return t }
func (t *Trace) Begin(name string) func() {
	if t == nil {
		return func() {}
	}
	start := time.Now()
	return func() {
		p := t.phases[name]
		p.Name = name
		p.Duration += time.Since(start)
		p.Calls++
		t.phases[name] = p
	}
}
func (t *Trace) Finish(f Frame) {
	for _, p := range t.phases {
		f.Phases = append(f.Phases, p)
	}
	sort.Slice(f.Phases, func(i, j int) bool { return f.Phases[i].Duration > f.Phases[j].Duration })
	t.Recorder.RecordFrame(f)
}

func (r *Recorder) Count(scope, name string) {
	r.mu.Lock()
	r.counters[scope+"/"+name]++
	r.mu.Unlock()
}

// Start avoids reading the clock on unprofiled paths.
func Start() time.Time {
	if Current() == nil {
		return time.Time{}
	}
	return time.Now()
}

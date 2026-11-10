// SPDX-License-Identifier: Unlicense OR MIT

// Package historylog is the journal of how histories load: the pages asked
// for and what they changed, what the history view asked for and why it
// did not. It is off unless Open names a file (the messenger's
// -history-log) or Keep keeps its last lines (the profiler's journal), and
// then it is for finding where a history stopped
// loading: a flag left set, an error kept, a page that never came back.
package historylog

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

var (
	mu     sync.Mutex
	file   *os.File
	start  time.Time
	on     atomic.Bool
	dumped atomic.Bool
	// kept are the last lines, while Keep keeps them: a ring of keepLimit,
	// next the place of the next and total how many came.
	keeping bool
	kept    []Line
	next    int
	total   uint64
)

// keepLimit is how many of the last lines Keep keeps.
const keepLimit = 20000

// Line is a line of the journal: when it was written, how many goroutines
// there were, and what it tells.
type Line struct {
	At         time.Time
	Goroutines int
	Text       string
}

// Keep keeps the journal's last lines in memory, for Lines, or lets them
// go: the profiler's journal keeps them while it is open, whether a file is
// written or not.
func Keep(keep bool) {
	mu.Lock()
	keeping = keep
	if keep && start.IsZero() {
		start = time.Now()
	}
	if !keep {
		kept, next, total = nil, 0, 0
	}
	writing := file != nil
	mu.Unlock()
	on.Store(keep || writing)
}

// Lines are the lines kept, oldest first, and how many lines came while
// they were kept: more than there are when the oldest went.
func Lines() ([]Line, uint64) {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Line, 0, len(kept))
	if total > uint64(len(kept)) {
		out = append(out, kept[next:]...)
		out = append(out, kept[:next]...)
	} else {
		out = append(out, kept...)
	}
	return out, total
}

// Open starts the journal in the file at path, after what it holds.
func Open(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	mu.Lock()
	file, start = f, time.Now()
	mu.Unlock()
	on.Store(true)
	Printf("start pid %d %s/%s", os.Getpid(), runtime.GOOS, runtime.GOARCH)
	return nil
}

// On tells whether the journal is written; callers skip work for it when
// it is not.
func On() bool { return on.Load() }

// Printf writes a line to the journal, with the time and the goroutine
// count, which grows when goroutines wait for ever.
func Printf(format string, args ...any) {
	if !on.Load() {
		return
	}
	line := fmt.Sprintf(format, args...)
	now := time.Now()
	goroutines := runtime.NumGoroutine()
	mu.Lock()
	defer mu.Unlock()
	if file != nil {
		fmt.Fprintf(file, "%s +%.3fs g%d %s\n", now.Format("15:04:05.000"), now.Sub(start).Seconds(), goroutines, line)
	}
	if keeping {
		l := Line{At: now, Goroutines: goroutines, Text: line}
		if len(kept) < keepLimit {
			kept = append(kept, l)
		} else {
			kept[next] = l
		}
		next = (next + 1) % keepLimit
		total++
	}
}

// DumpOnce writes the stacks of every goroutine to the journal the first
// time it is called: where a page that never came back waits.
func DumpOnce(why string) {
	if !on.Load() || !dumped.CompareAndSwap(false, true) {
		return
	}
	buf := make([]byte, 1<<20)
	for {
		n := runtime.Stack(buf, true)
		if n < len(buf) {
			buf = buf[:n]
			break
		}
		buf = make([]byte, 2*len(buf))
	}
	Printf("goroutines (%s):\n%s", why, buf)
}

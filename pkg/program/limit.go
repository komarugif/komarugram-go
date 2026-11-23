// SPDX-License-Identifier: Unlicense OR MIT

package program

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// Limits bound what a program started with StartLimited may take.
type Limits struct {
	// Memory is the most memory the program may take, in bytes; 0 for no
	// bound.
	Memory uint64
	// Reserved is what the program reserves as it starts, before it reads
	// anything (Reserved measures it). Where the system bounds memory
	// reserved, not used, it is allowed over Memory: Linux's RLIMIT_DATA
	// counts a library's .bss whole, though nothing in it is touched, and
	// a distribution's FFmpeg links one of 128 MiB (SVT-AV1's). What the
	// program keeps in memory is still bounded by Memory alone.
	Reserved uint64
}

// ErrMemory is what a program killed for taking more memory than its
// limit failed with.
var ErrMemory = errors.New("the program took more memory than it may")

// StartLimited starts cmd, as cmd.Start does, its memory bounded by
// limits as well as the system allows, and calls exceeded, once, from
// another goroutine, when the program was killed for taking more.
//
//   - Windows: a job object bounds the memory the program commits from its
//     first instruction, the program started suspended until it is in the
//     job; it is killed at the first allocation over the bound. Where the
//     client is in a job that takes no other (Windows 7), its memory is
//     watched instead.
//   - Linux: RLIMIT_DATA, set as soon as it started, makes allocations past
//     the bound fail; its memory is watched as well, and the program
//     killed past it.
//   - macOS and Haiku: its memory is watched, and the program killed past
//     the bound: after it took it, within watchEvery.
//   - Elsewhere it is not bounded.
//
// A program killed so may have taken some more before it was: the bound
// keeps a hostile file from taking the machine's memory, not exactly to
// the byte.
func StartLimited(cmd *exec.Cmd, limits Limits, exceeded func()) error {
	if limits.Memory == 0 {
		return cmd.Start()
	}
	if exceeded == nil {
		exceeded = func() {}
	}
	var once sync.Once
	return startLimited(cmd, limits, func() { once.Do(exceeded) })
}

// watchEvery is how often the memory of a program is looked at where the
// system does not bound it: a program can allocate gigabytes a second, so
// it overshoots by about this much of that.
const watchEvery = 50 * time.Millisecond

// watch looks at the memory of process pid every watchEvery, as used
// tells it, and kills the program with kill once it is over limit, and
// calls exceeded; it stops once used tells the process is gone.
func watch(pid int, limit uint64, used func(pid int) (uint64, bool), kill func(), exceeded func()) {
	go func() {
		tick := time.NewTicker(watchEvery)
		defer tick.Stop()
		for range tick.C {
			n, alive := used(pid)
			if !alive {
				return
			}
			if n > limit {
				kill()
				exceeded()
				return
			}
		}
	}()
}

// Reserved is the memory the program at path reserves as it starts, where
// the system counts it in what it bounds (Limits.Reserved): it runs the
// program with args, which have it wait for its standard input before it
// reads anything, and looks at what it took once it waits. It is 0 where
// nothing counts memory reserved, or the program would not wait. What a
// program reserves depends on the program and its libraries, not on what
// it reads: it is measured once for a path, until the file changes.
func Reserved(path string, args ...string) uint64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	key := reservedKey{path: path, args: strings.Join(args, "\x00"), size: info.Size(), modified: info.ModTime()}
	reservedMu.Lock()
	defer reservedMu.Unlock()
	if n, ok := reservedSeen[key]; ok {
		return n
	}
	n := reserved(path, args)
	reservedSeen[key] = n
	return n
}

// reservedKey is a program as Reserved measured it.
type reservedKey struct {
	path, args string
	size       int64
	modified   time.Time
}

var (
	// reservedMu is held while a program is measured: what starts it
	// again waits for the measure rather than taking another.
	reservedMu   sync.Mutex
	reservedSeen = map[reservedKey]uint64{}
)

// reservedTimeout bounds how long a program measured by Reserved may take
// to come to wait: a flatpak takes about a second the first time.
const reservedTimeout = 5 * time.Second

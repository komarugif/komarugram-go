// SPDX-License-Identifier: Unlicense OR MIT

package program

import (
	"errors"
	"os/exec"
	"sync"
	"time"
)

// Limits bound what a program started with StartLimited may take.
type Limits struct {
	// Memory is the most memory the program may take, in bytes; 0 for no
	// bound.
	Memory uint64
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

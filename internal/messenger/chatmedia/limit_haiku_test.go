// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku

package chatmedia

import (
	"testing"

	"komarugram/pkg/program"
)

// What this package starts with a bound is bound: on Haiku it is watched, by a
// source of the memory a program keeps that pkg/program has none of itself.
func TestMemoryOfProgramsIsWatched(t *testing.T) {
	if !program.MemoryWatched() {
		t.Fatal("nothing tells the memory of a program: a bound on it is none")
	}
}

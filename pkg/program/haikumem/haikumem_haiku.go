// SPDX-License-Identifier: Unlicense OR MIT

//go:build haiku && cgo

// Package haikumem tells how much memory a team of Haiku keeps in RAM.
//
// It is a package of its own for the sake of pkg/program: what is cgo is
// linked by Haiku's ld, which refuses what Go hands it, so a program that has
// cgo anywhere in its imports has to be linked with -ldflags=-linkmode=internal.
// pkg/program is imported by cmd/haiku-build, which should build with no
// flags; the messenger, which has cgo besides, is always built with it.
package haikumem

/*
#include <OS.h>

// teamMemory is the memory the areas of team keep in RAM, or -1 when it
// is gone.
static long long teamMemory(int team) {
	area_info info;
	ssize_t cookie = 0;
	long long sum = 0;
	team_info about;
	if (get_team_info(team, &about) != B_OK) {
		return -1;
	}
	while (get_next_area_info(team, &cookie, &info) == B_OK) {
		sum += info.ram_size;
	}
	return sum;
}
*/
import "C"

// TeamMemory is the memory the areas of the team of the process pid keep in
// RAM, and whether the team is still there.
func TeamMemory(pid int) (uint64, bool) {
	n := C.teamMemory(C.int(pid))
	if n < 0 {
		return 0, false
	}
	return uint64(n), true
}

// SPDX-License-Identifier: Unlicense OR MIT

// Package testenv tells a test what the run allows. go test -short, as it
// runs between commits, runs no test that starts a program of the system:
// a browser, FFmpeg, a player (AGENTS.md, "Tests").
package testenv

import "testing"

// Integration skips the test, which does what, when go test runs with
// -short.
func Integration(t testing.TB, what string) {
	t.Helper()
	if testing.Short() {
		t.Skipf("%s: run without -short", what)
	}
}

// Waits skips the test, which waits on the clock for what, when go test
// runs with -short: one that plays an animation for seconds, to see what it
// does over them.
func Waits(t testing.TB, what string) {
	t.Helper()
	if testing.Short() {
		t.Skipf("waits %s: run without -short", what)
	}
}

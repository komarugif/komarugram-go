// SPDX-License-Identifier: Unlicense OR MIT

package sandbox

import "testing"

// grow grows a memory of budget a page at a time to size, as a module
// does, and returns how many times it was allocated anew and what it holds.
func grow(t *testing.T, budget *Budget, size uint64) (allocations int, m *memory) {
	t.Helper()
	const page = 64 << 10
	m = budget.Allocate(0, 64<<20).(*memory)
	var first *byte
	for n := uint64(page); n <= size; n += page {
		buf := m.Reallocate(n)
		if buf == nil {
			t.Fatalf("memory refused at %d bytes", n)
		}
		if &buf[0] != first {
			first = &buf[0]
			allocations++
		}
	}
	return allocations, m
}

// A memory grows to what the last one of its module grew to at once, when
// it is on its way there, in place of growing by halves, allocated and
// copied over and over; one that stays small takes no more than it did.
func TestMemoryGrowsAsTheLastOne(t *testing.T) {
	budget := NewBudget(256 << 20)
	const large, small = 14 << 20, 2 << 20
	first, m := grow(t, budget, large)
	m.Free()
	second, m := grow(t, budget, large)
	if second >= first || second > 12 {
		t.Fatalf("the second memory was allocated %d times, the first %d", second, first)
	}
	if used := budget.Used(); used != uint64(cap(m.buf)) || used > large+large/2 {
		t.Fatalf("budget holds %d bytes for a memory of %d", used, cap(m.buf))
	}
	m.Free()
	if used := budget.Used(); used != 0 {
		t.Fatalf("budget holds %d bytes after the memory is freed", used)
	}
	// After a large one, a small one stays small.
	_, m = grow(t, budget, small)
	if held := cap(m.buf); held > small+small/2 {
		t.Fatalf("a memory of %d bytes holds %d after a large one", small, held)
	}
	m.Free()
	// A budget too tight for the size remembered gives what there is.
	tight := NewBudget(20 << 20)
	_, m = grow(t, tight, large)
	m.Free()
	_, kept := grow(t, tight, 8<<20)
	if _, m = grow(t, tight, 6<<20); m == nil {
		t.Fatal("no memory within a tight budget")
	}
	kept.Free()
	m.Free()
}

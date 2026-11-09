package ui

import (
	"sort"
	"testing"

	"komarugram/internal/messenger/model"
)

// oldMerge is merge as it was, by a map of every row and a sort, for the
// benchmark to compare with.
func oldMerge(r *reserve, rows []skeletonRow) {
	by := make(map[model.MessageID]skeletonRow, len(r.rows)+len(rows))
	for _, s := range r.rows {
		by[s.id] = s
	}
	for _, s := range rows {
		by[s.id] = s
	}
	r.rows = r.rows[:0]
	for _, s := range by {
		r.rows = append(r.rows, s)
	}
	sort.Slice(r.rows, func(i, j int) bool { return r.rows[i].id < r.rows[j].id })
}

// BenchmarkReserveMerge merges 100 rows let go of into a reserve of 20000,
// as the window lets a page go below what is loaded.
func BenchmarkReserveMerge(b *testing.B) {
	base := make([]skeletonRow, 20000)
	for i := range base {
		base[i] = skeletonRow{id: model.MessageID(1000 + i), height: 60}
	}
	let := make([]skeletonRow, 100)
	for i := range let {
		let[i] = skeletonRow{id: model.MessageID(900 + i), height: 70}
	}
	for _, c := range []struct {
		name  string
		merge func(*reserve, []skeletonRow)
	}{{"map-and-sort", oldMerge}, {"one-pass", (*reserve).merge}} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				r := reserve{rows: append([]skeletonRow(nil), base...), heights: map[model.MessageID]int{}}
				c.merge(&r, let)
			}
		})
	}
}

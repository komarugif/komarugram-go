// SPDX-License-Identifier: Unlicense OR MIT

// Command registration-table writes internal/messenger/model/registrations.go,
// the table model.RegisteredAround estimates when an account was registered
// by: user ids with the days their accounts were, smoothed.
//
//	go run ./cmd/registration-table points.txt more-points.txt
//
// Each file has a user id and a day a line, "id YYYY-MM-DD" or "id
// unix-seconds"; "#" begins a comment. The files users send from the
// client (model.WriteRegistrationPoints) give the second, and the time they
// were collected at in a comment. The points are kept out of the
// repository: those of real accounts tell whose contacts they are, and the
// table smooths them away. The table of 2026-10 was made of:
//
//   - https://raw.githubusercontent.com/Jobians/telegram-id-age/main/dataset.json
//     (2025-11-22), its "id" and "date" fields, without the id 0;
//   - the points of materialgram's findRegistrationTime
//     (Telegram/SourceFiles/info/profile/info_profile_values.cpp), without
//     3318845111 and 4317845111: dated in 2021-04 and 2021-05, they are ids
//     no user had before 2021-12;
//   - the 55 points of the maintainer's account, collected by the client on
//     2026-10-20, the only ones of 2026. Of those with profile photos, none
//     has one older than its "joined Telegram", and 10 of 25 one of that
//     same day: they are registrations, however far from the curve.
//
// Telegram gives ids in order only roughly: of two accounts, the one with the
// larger id may be months older. The table is the closest curve through the
// points that never goes back in time (an isotonic regression), and each of
// its points tells how far the points around it lie from the curve.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"go/format"
	"log"
	"math"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

type point struct {
	id   int64
	days float64 // since the Unix epoch
}

const day = 24 * 60 * 60

func main() {
	out := flag.String("o", "internal/messenger/model/registrations.go", "the Go file to write")
	flag.Parse()
	if flag.NArg() == 0 {
		log.Fatal("registration-table: name the files of points")
	}
	var points []point
	for _, name := range flag.Args() {
		p, err := read(name)
		if err != nil {
			log.Fatalf("registration-table: %s: %v", name, err)
		}
		points = append(points, p...)
	}
	raw := merge(points)
	fit := isotonic(raw)
	table := spreads(fit, raw)
	if err := write(*out, table, len(raw)); err != nil {
		log.Fatal("registration-table: ", err)
	}
	fmt.Printf("%d points, %d in the table\n", len(raw), len(table))
}

func read(name string) ([]point, error) {
	f, err := os.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []point
	s := bufio.NewScanner(f)
	for n := 1; s.Scan(); n++ {
		line, _, _ := strings.Cut(s.Text(), "#")
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if len(fields) != 2 {
			return nil, fmt.Errorf("line %d: want an id and a day", n)
		}
		id, err := strconv.ParseInt(fields[0], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		if id <= 0 {
			continue
		}
		var unix int64
		if t, err := time.Parse(time.DateOnly, fields[1]); err == nil {
			unix = t.Unix()
		} else if unix, err = strconv.ParseInt(fields[1], 10, 64); err != nil {
			return nil, fmt.Errorf("line %d: %w", n, err)
		}
		out = append(out, point{id, float64(unix) / day})
	}
	return out, s.Err()
}

// merge sorts points by id, one point an id: the mean of its days.
func merge(points []point) []point {
	slices.SortFunc(points, func(a, b point) int {
		if a.id != b.id {
			if a.id < b.id {
				return -1
			}
			return 1
		}
		return 0
	})
	var out []point
	for i := 0; i < len(points); {
		j, sum := i, 0.0
		for ; j < len(points) && points[j].id == points[i].id; j++ {
			sum += points[j].days
		}
		out = append(out, point{points[i].id, sum / float64(j-i)})
		i = j
	}
	return out
}

// isotonic is the curve through points, sorted by id, closest to them in
// least squares that never goes back in time, by pooling adjacent
// violators: a run of points pooled becomes one point, at its median id, on
// its mean day.
func isotonic(points []point) []point {
	type block struct {
		from, to int // points[from:to]
		sum      float64
	}
	var blocks []block
	for i, p := range points {
		blocks = append(blocks, block{i, i + 1, p.days})
		for len(blocks) > 1 {
			a, b := blocks[len(blocks)-2], blocks[len(blocks)-1]
			if a.sum/float64(a.to-a.from) <= b.sum/float64(b.to-b.from) {
				break
			}
			blocks = append(blocks[:len(blocks)-2], block{a.from, b.to, a.sum + b.sum})
		}
	}
	out := make([]point, 0, len(blocks))
	for _, b := range blocks {
		mid := points[b.from+(b.to-b.from-1)/2].id
		if (b.to-b.from)%2 == 0 {
			mid = (mid + points[b.from+(b.to-b.from)/2].id) / 2
		}
		out = append(out, point{mid, b.sum / float64(b.to-b.from)})
	}
	return out
}

// entry is a point of the table, with the median distance in days of the
// points around it from the curve.
type entry struct {
	point
	spread float64
}

// spreadOf is how many points, on either side, the spread of a point of the
// table is measured on.
const spreadOf = 6

func spreads(fit, raw []point) []entry {
	at := func(id int64) float64 {
		i := sort.Search(len(fit), func(i int) bool { return fit[i].id >= id })
		switch {
		case i == 0:
			return fit[0].days
		case i == len(fit):
			return fit[len(fit)-1].days
		}
		a, b := fit[i-1], fit[i]
		return a.days + float64(id-a.id)/float64(b.id-a.id)*(b.days-a.days)
	}
	out := make([]entry, len(fit))
	for i, p := range fit {
		k := sort.Search(len(raw), func(i int) bool { return raw[i].id >= p.id })
		var off []float64
		for j := max(0, k-spreadOf); j < min(len(raw), k+spreadOf); j++ {
			off = append(off, math.Abs(raw[j].days-at(raw[j].id)))
		}
		slices.Sort(off)
		out[i] = entry{p, off[len(off)/2]}
	}
	return out
}

func write(name string, table []entry, points int) error {
	var b strings.Builder
	fmt.Fprintf(&b, `// Code generated by cmd/registration-table from %d points; DO NOT EDIT.

package model

// registrations are user ids with the days their accounts were registered,
// smoothed, each with how many days the points around it lie from them.
var registrations = []registration{
`, points)
	for _, e := range table {
		fmt.Fprintf(&b, "\t{%d, %d, %d}, // %s\n", e.id, int64(math.Round(e.days*day)), int64(math.Round(e.spread)),
			time.Unix(int64(e.days*day), 0).UTC().Format(time.DateOnly))
	}
	b.WriteString("}\n")
	src, err := format.Source([]byte(b.String()))
	if err != nil {
		return err
	}
	return os.WriteFile(name, src, 0o644)
}

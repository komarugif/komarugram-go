// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
)

// Edits and reactions resize the real history row without a jump, including
// a second revision while it is still moving. Only settled sizes are cached.
func TestMessageRevisionAnimatesHeight(t *testing.T) {
	for _, kind := range []string{"edit", "reaction", "wrap", "unwrapped"} {
		t.Run(kind, func(t *testing.T) {
			h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
				if kind == "unwrapped" {
					messages[2].Text = "🙂"
				}
				if kind == "wrap" {
					messages[2].Reactions = []model.Reaction{{Emoji: "❤", Count: 1}}
				}
			})
			h.animate = true
			h.frames(2)
			before := h.page.rows[3].resize.height.value
			change := func(grow bool) {
				next := slices.Clone(h.store.h.Messages)
				m := &next[2]
				if kind == "edit" {
					m.Text = "Message 3"
					if grow {
						m.Text = strings.Repeat("An edited message with more text. ", 24)
					}
				} else {
					m.Reactions = nil
					if grow {
						n := 1
						if kind == "wrap" {
							n = 16
						}
						for i := range n {
							m.Reactions = append(m.Reactions, model.Reaction{Emoji: string(rune(0x1F600 + i)), Count: 12})
						}
					}
				}
				m.ContentRevision = model.Revision(*m)
				h.store.h.Messages, h.store.h.Revision = next, h.store.h.Revision+1
				h.frame()
			}
			shot := func(name string) {
				if dir := os.Getenv("MESSAGE_RESIZE_PNG_DIR"); dir != "" {
					if err := os.MkdirAll(dir, 0755); err != nil {
						t.Fatal(err)
					}
					img, err := renderSnapshot(h.ops, image.Pt(500, 700))
					if err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%s-%s.png", kind, name)))
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if err := png.Encode(f, img); err != nil {
						t.Fatal(err)
					}
				}
			}
			shot("before")
			started := h.now
			change(true)
			r := h.page.rows[3]
			if r.resize.height.target <= before || r.resize.height.value != before {
				t.Fatalf("height jumped: before %d, value %d, target %d", before, r.resize.height.value, r.resize.height.target)
			}
			// Half the duration is already near the end of the standard
			// easing curve. Capture half the visible height change instead.
			for elapsed := 4 * time.Millisecond; elapsed < heightDuration; elapsed += 4 * time.Millisecond {
				h.now = started.Add(elapsed)
				h.frame()
				r = h.page.rows[3]
				if 2*(r.resize.height.value-before) >= r.resize.height.target-before {
					break
				}
			}
			r = h.page.rows[3]
			mid, target := r.resize.height.value, r.resize.height.target
			if mid <= before || mid >= target {
				t.Fatalf("no intermediate height: %d -> %d -> %d", before, mid, target)
			}
			t.Logf("middle frame: height %d -> %d -> %d (%.0f%%)", before, mid, target, 100*float64(mid-before)/float64(target-before))
			if got := h.page.measures[3]; got.ContentRevision == r.revision {
				t.Fatal("cached an intermediate height")
			}
			shot("middle")
			h.now = h.now.Add(-16 * time.Millisecond)
			change(false)
			if got := h.page.rows[3].resize.height.value; got != mid {
				t.Fatalf("reversal jumped: %d -> %d", mid, got)
			}
			h.now = h.now.Add(heightDuration + time.Millisecond)
			h.frame()
			if r := h.page.rows[3]; r.resize.changing || r.resize.height.value != r.resize.height.target {
				t.Fatal("height did not settle")
			}
			if r, saved := h.page.rows[3], h.page.measures[3]; saved.ContentRevision != r.revision || saved.HeightPx != r.resize.rowHeight {
				t.Fatal("settled height was not cached")
			}
			shot("after")
			h.animate = false
			change(true)
			if h.page.rows[3].resize.changing {
				t.Fatal("animated with motion disabled")
			}
			shot("expanded")
			h.animate = true
			change(false)
			h.width = 380
			h.frame()
			if h.page.rows[3].resize.changing {
				t.Fatal("animated stale height after a width change")
			}
		})
	}
}

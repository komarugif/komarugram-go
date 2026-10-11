// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

func foundEmoji(query, language string) []string {
	var out []string
	for _, item := range searchEmoji(query, language) {
		out = append(out, item.Emoji)
	}
	return out
}

func emojiAt(list []string, emoji string) int {
	for i, e := range list {
		if strings.ReplaceAll(e, "️", "") == strings.ReplaceAll(emoji, "️", "") {
			return i
		}
	}
	return -1
}

// The search finds an emoji by its name and its keywords, in English and in
// the language of the client, and puts a name that is the query first.
func TestSearchEmoji(t *testing.T) {
	cat := foundEmoji("cat", "en")
	if emojiAt(cat, "🐈") < 0 || emojiAt(cat, "🐱") < 0 {
		t.Fatalf("cat: %v", cat)
	}
	if emojiAt(cat, "🐈") > emojiAt(cat, "🐱") {
		t.Errorf("the cat itself is after the cat's face: %v", cat[:min(len(cat), 8)])
	}
	if strings.Contains(strings.Join(cat, ""), "⛱") {
		t.Error("an umbrella is found for cat, as its name has the letters, not a word of them")
	}
	ru := foundEmoji("кот", "ru")
	if emojiAt(ru, "🐈") < 0 || emojiAt(ru, "🐱") < 0 {
		t.Fatalf("кот: %v", ru)
	}
	// English words are found in Russian, too, and ё is е.
	if emojiAt(foundEmoji("cat", "ru"), "🐈") < 0 {
		t.Error("English is not searched with Russian")
	}
	if got := foundEmoji("елка", "ru"); emojiAt(got, "🎄") < 0 {
		t.Errorf("ёлка with е: %v", got)
	}
	// Words of the query in any order, each the start of a word.
	if got := foundEmoji("face smi", "en"); emojiAt(got, "😀") < 0 {
		t.Errorf("face smi: %v", got[:min(len(got), 8)])
	}
	if got := foundEmoji("flag ukr", "en"); emojiAt(got, "🇺🇦") != 0 {
		t.Errorf("flag ukr: %v", got)
	}
	// A typed emoji is found itself.
	if got := foundEmoji("🐈", "en"); len(got) == 0 || emojiAt(got, "🐈") != 0 {
		t.Errorf("a typed emoji: %v", got)
	}
	if got := foundEmoji("qzxv", "en"); len(got) != 0 {
		t.Errorf("nothing was to be found: %v", got)
	}
	if got := foundEmoji("   ", "en"); got != nil {
		t.Errorf("a query of spaces finds %v", got)
	}
}

// Every emoji of a section has a name to be found by.
func TestEmojiKeywordsCoverSections(t *testing.T) {
	named := map[string]bool{}
	for _, e := range emojiIndexes()["en"] {
		named[strings.ReplaceAll(e.emoji, "️", "")] = true
	}
	missing := 0
	for _, section := range emojiSections {
		for _, e := range strings.Fields(section) {
			if !named[strings.ReplaceAll(e, "️", "")] {
				missing++
			}
		}
	}
	if missing > 0 {
		t.Errorf("%d emoji have no English name", missing)
	}
}

// slowSearch answers slowly, and holds a custom emoji for each search.
type slowSearch struct{}

func (slowSearch) Picker(_ context.Context, r model.PickerRequest) (model.PickerPage, error) {
	time.Sleep(80 * time.Millisecond)
	page := model.PickerPage{Recent: []model.PickerItem{{ID: "emoji/🅰", Emoji: "🅰"}}}
	if r.Query != "" {
		page.Items = []model.PickerItem{{ID: "custom/" + r.Query, Emoji: "🐈"}}
	}
	return page, nil
}
func (slowSearch) Send(context.Context, int64, model.OutgoingMessage) error { return nil }

// A query typed does not empty the list while Telegram answers: the emoji of
// the picker are there in the frame that follows the letter, what was found
// last stays till the next has come, and clearing the query gives back the
// sections at once.
func TestSearchDoesNotBlink(t *testing.T) {
	withEmojiPack(t)
	h := newComposerHarness(t)
	h.chat = 2
	c := h.p.composer
	c.source = slowSearch{}
	l := localization.For("en")
	h.frame()
	c.pickerOpen = true
	c.request(l, false)
	for deadline := time.Now().Add(3 * time.Second); c.loading && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
		h.frame()
	}
	if !c.pageLoaded {
		t.Fatal("the tab did not load")
	}
	shown := func() map[string]bool { return rowsItems(c.pickerRows(320, 40, l)) }
	var last map[string]bool
	for _, typed := range []string{"c", "ca", "cat"} {
		c.search.SetText(typed)
		h.frame()
		got := shown()
		if len(got) == 0 || !got["emoji/🐱"] && typed == "cat" {
			t.Fatalf("%q: %d emoji in the frame after the letter", typed, len(got))
		}
		// While Telegram answers, what it found for the last query is there.
		if last != nil && last["custom/"+string(typed[:len(typed)-1])] && !got["custom/"+typed[:len(typed)-1]] {
			// Only the custom emoji of a query gone stale may stay, or go once
			// the next has come: never in the frame after the letter.
			t.Fatalf("%q: what was found for the last query went as the letter was typed", typed)
		}
		for deadline := time.Now().Add(3 * time.Second); (c.loading || !c.due.IsZero()) && time.Now().Before(deadline); {
			time.Sleep(5 * time.Millisecond)
			h.frame()
			if len(shown()) == 0 {
				t.Fatalf("%q: the list is empty while Telegram answers", typed)
			}
		}
		last = shown()
	}
	if !last["custom/cat"] {
		t.Fatal("Telegram's custom emoji did not come after the emoji of the picker")
	}
	if cats, custom := rowOf(c, l, "emoji/🐈"), rowOf(c, l, "custom/cat"); cats < 0 || custom < 0 || custom < cats {
		t.Fatalf("the picker's emoji at %d and Telegram's at %d, which come after", cats, custom)
	}

	c.search.SetText("")
	h.frame()
	got := shown()
	if !got["emoji/😀"] || !got["emoji/🅰"] {
		t.Fatalf("clearing the query shows %d emoji, without the sections or the recent", len(got))
	}
	// And they stay while the page is asked for again.
	for deadline := time.Now().Add(3 * time.Second); (c.loading || !c.due.IsZero()) && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
		h.frame()
		if got := shown(); !got["emoji/😀"] || !got["emoji/🅰"] {
			t.Fatalf("the sections or the recent went while the page is asked for again: %d emoji", len(got))
		}
	}
}

// rowOf is the number of the row with the item, or -1.
func rowOf(c *messageComposer, l localization.Catalog, id string) int {
	for i, row := range c.pickerRows(320, 40, l) {
		for _, item := range row.items {
			if item.ID == id {
				return i
			}
		}
	}
	return -1
}

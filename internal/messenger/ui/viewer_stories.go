// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"slices"

	"komarugram/internal/messenger/model"
)

// The stories of the whole row over the chats go through the viewer as
// one gallery, as Telegram Desktop goes on from one's stories to the next
// one's: the stories of each, the oldest first, in the order of the row.
// Their ids are each peer's own, the same for several, so the viewer keys
// each by its place in the row and its id (storyKey), below the photos of
// profiles; what it asks of the stories it asks with the story's own
// (realStory). The peers' stories are asked for one peer at a time, as
// the strip comes near either end, as a gallery's pages are.

const (
	// storyKeyBase is the key of the first story of the row's first
	// peer's, those after it below: just under model.ProfilePhotoID's.
	storyKeyBase = model.MessageID(-1<<30 - 1)
	// storyIDBits are what a story's id takes of a key, the rest its
	// row's place, up to storyRows peers: the keys stay within 32 bits.
	storyIDBits = 20
	storyRows   = 1 << 10
	// storyPrefetch is how close to an end of what is known the next
	// peer's stories are asked for: a peer has few.
	storyPrefetch = 2
)

// storyKey is the viewer's key of story id of the peer at row of the row.
func storyKey(row, id int) model.MessageID {
	return storyKeyBase - model.MessageID(row<<storyIDBits|id)
}

// storyOfKey is the row and the story of a key storyKey made.
func storyOfKey(key model.MessageID) (row, id int) {
	n := int(storyKeyBase - key)
	return n >> storyIDBits, n & (1<<storyIDBits - 1)
}

// realStory is m, a story of the gallery, as the stories know it: by its
// own id.
func (v *mediaViewer) realStory(m model.Message) model.Message {
	if v.gallery == galleryStories && m.Key.MessageID <= storyKeyBase {
		_, id := storyOfKey(m.Key.MessageID)
		m.Key.MessageID = model.StoryMessage(id)
	}
	return m
}

// OpenStories shows the stories of the row, from story open of peer, as
// Telegram Desktop's list opens them, in the viewer as any media: the
// arrows and the strip go on to the next one's and back to the one's
// before. It tells whether the store has stories.
func (v *mediaViewer) OpenStories(peer int64, open model.MessageID) bool {
	if v.stories == nil {
		return false
	}
	var rows []int64
	for _, p := range v.stories.StoryPeers() {
		rows = append(rows, p.ID)
	}
	row := slices.Index(rows, peer)
	if row < 0 || row >= storyRows {
		rows, row = []int64{peer}, 0
	}
	return v.openStoryRows(peer, open, rows[:min(len(rows), storyRows)], row)
}

// OpenPeerStories shows peer's stories alone, from open, without those of
// the others of the list beside them: as Telegram Desktop's top bar opens
// a peer's stories from its avatar.
func (v *mediaViewer) OpenPeerStories(peer int64, open model.MessageID) bool {
	if v.stories == nil {
		return false
	}
	return v.openStoryRows(peer, open, []int64{peer}, 0)
}

// openStoryRows shows the stories of rows, those of rows[row], peer, from
// open.
func (v *mediaViewer) openStoryRows(peer int64, open model.MessageID, rows []int64, row int) bool {
	key := storyKey(row, model.StoryID(open))
	v.openList(peer, model.Message{Key: model.MessageKey{ChatID: peer, MessageID: key}}, mediaList{gallery: galleryStories, rows: rows, loaded: [2]int{0, -1}, open: row})
	return true
}

// OpenChatStories shows m, a message of chat carrying a story, among the
// stories the chat's history carries, as its photos go through its photos:
// those of the cache, not the ones the user keeps on the profile. known
// are messages the caller has, of which those are taken.
func (v *mediaViewer) OpenChatStories(chat int64, m model.Message, known []model.Message) {
	var stories []model.Message
	for _, k := range known {
		if k.StoryShown() {
			stories = append(stories, k.AsStory())
		}
	}
	v.openList(chat, m.AsStory(), mediaList{items: stories, gallery: galleryChatStories})
}

// OpenStory shows story id of peer alone, asked of the store, and tells
// when Telegram has it no more: the story a message replies to.
func (v *mediaViewer) OpenStory(peer int64, id int) bool {
	if v.stories == nil {
		return false
	}
	v.openList(peer, model.Message{Key: model.MessageKey{ChatID: peer, MessageID: model.StoryMessage(id)}}, mediaList{gallery: galleryOneStory})
	return true
}

// nextStoryRowLocked is the row whose stories a fetch toward dir asks for,
// ok false for none: the one opened first, then those beside what is
// known. At an end, that side is exhausted. v.mu is held.
func (v *mediaViewer) nextStoryRowLocked(dir int) (int, bool) {
	first, last := v.storyLoaded[0], v.storyLoaded[1]
	if last < first {
		// Nothing yet: the first asks for the peer opened.
		return v.storyOpen, dir > 0
	}
	row := last + 1
	if dir < 0 {
		row = first - 1
	}
	if row < 0 || row >= len(v.storyRows) {
		v.exhausted[(dir+1)/2] = true
		return 0, false
	}
	return row, true
}

// takeStoriesLocked adds stories, the peer's at row, keyed, to the items.
// v.mu is held.
func (v *mediaViewer) takeStoriesLocked(row int, stories []model.Message) {
	if v.storyLoaded[1] < v.storyLoaded[0] {
		v.storyLoaded = [2]int{row, row}
	} else {
		v.storyLoaded[0], v.storyLoaded[1] = min(v.storyLoaded[0], row), max(v.storyLoaded[1], row)
	}
	keyed := make([]model.Message, 0, len(stories))
	for _, m := range stories {
		if id := model.StoryID(m.Key.MessageID); id > 0 && id < 1<<storyIDBits {
			m.Key.MessageID = storyKey(row, id)
			keyed = append(keyed, m)
		}
	}
	v.items = mergeMedia(galleryStories, v.items, keyed)
	v.exhausted = [2]bool{v.storyLoaded[0] == 0, v.storyLoaded[1] == len(v.storyRows)-1}
	v.ended = v.exhausted
}

// storyPlace is where story i of items is among its peer's, and how many
// that peer has: the title counts the stories of one.
func storyPlace(items []model.Message, i int) (n, amount int) {
	peer := items[i].Key.ChatID
	for j, m := range items {
		if m.Key.ChatID != peer {
			continue
		}
		amount++
		if j <= i {
			n++
		}
	}
	return n, amount
}

// readStory tells the stories that m, shown, is seen.
func (v *mediaViewer) readStory(m model.Message) {
	if v.gallery != galleryStories || v.stories == nil || m.Key.MessageID == v.seen {
		return
	}
	v.seen = m.Key.MessageID
	m = v.realStory(m)
	v.stories.ReadStory(m.Key.ChatID, m.Key.MessageID)
}

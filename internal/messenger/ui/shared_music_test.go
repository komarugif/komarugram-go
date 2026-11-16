// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"slices"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

// playlistTitles are the titles of the songs of user's playlist as the
// store has it.
func playlistTitles(t *testing.T, store *mockstore.Store, user int64) []string {
	t.Helper()
	page, err := store.SharedCollection(context.Background(), user, model.SharedSavedMusic, "", 50)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, m := range page.Messages {
		out = append(out, m.Media.Title)
	}
	return out
}

// songTitles are the titles of the songs the info shows.
func songTitles(p *chatInfo) []string {
	var out []string
	for _, m := range p.messages {
		out = append(out, m.Media.Title)
	}
	return out
}

// ownPlaylistHarness is the account's profile page on the demo with its
// playlist open, drawn on a router's frames.
func ownPlaylistHarness(t *testing.T) (*profilePage, *mockstore.Store, *focusHarness) {
	t.Helper()
	store := mockstore.New(time.Now(), 0)
	// A third song, for a drag past one.
	for _, m := range store.History(2).Messages {
		if m.Kind == model.MessageMusic {
			if err := store.SaveMusic(context.Background(), m); err != nil {
				t.Fatal(err)
			}
			break
		}
	}
	p := newOwnProfile(t, store, func() {})
	me := store.Me()
	p.open(me)
	awaitShared(t, p.info)
	p.info.selectSection(model.SharedSavedMusic)
	awaitShared(t, p.info)
	h := &focusHarness{draw: func(gtx layout.Context) {
		p.Update(gtx, me, func() {})
		p.Layout(gtx, me, localization.For("en"), false, false)
	}}
	h.frame()
	h.frame()
	return p, store, h
}

// A song of the account's playlist is dragged by the handle at its right
// to another place, which Telegram is told, as the settings' menus are
// reordered.
func TestOwnPlaylistReorders(t *testing.T) {
	p, store, h := ownPlaylistHarness(t)
	me := store.Me().ID
	before := songTitles(p.info)
	if len(before) < 3 || !p.info.ownPlaylist() {
		t.Fatalf("the playlist %v, own %v", before, p.info.ownPlaylist())
	}
	height := float32(p.info.songHeight)
	// The handle is at the right of the column, under the header.
	x := float32(p.info.margin+(900-2*p.info.margin)) - float32(songHandleWidth)/2
	y := float32(p.info.headerHeight) + height/2
	h.router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(x, y)})
	h.frame()
	for step := float32(1); step <= 4; step++ {
		h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(x, y+step*height/2)})
		h.frame()
	}
	h.router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(x, y+2*height)})
	h.frame()
	want := []string{before[1], before[2], before[0]}
	if got := songTitles(p.info); !slices.Equal(got[:3], want) {
		t.Fatalf("after the drag the playlist shows %v, want %v first", got, want)
	}
	waitAudio(t, "Telegram was not told the order", func() bool {
		return slices.Equal(playlistTitles(t, store, me)[:3], want)
	})
}

// The menu of a song of a playlist has Forward, which sends the song anew,
// and on the account's own Delete, which takes it off, as Telegram
// Desktop's.
func TestPlaylistMenu(t *testing.T) {
	p, store, h := ownPlaylistHarness(t)
	l := localization.For("en")
	me := store.Me().ID
	song := p.info.messages[0]
	if got := p.info.sharedMenuActions(song); !slices.Equal(got, []sharedAction{sharedForward, sharedRemoveMusic}) {
		t.Fatalf("the own playlist's menu %v", got)
	}

	// Forward sends the song to the chat picked.
	target := model.Chat{ID: 2, Kind: model.KindUser, Title: "Анна Смирнова"}
	sent := len(store.History(target.ID).Messages)
	p.info.sharedDo(layout.Context{}, sharedForward, song, l)
	if len(p.info.renderer.forwarding.songs) != 1 {
		t.Fatal("Forward did not take the song")
	}
	p.info.renderer.forward(target)
	waitAudio(t, "the song was not sent", func() bool {
		messages := store.History(target.ID).Messages
		last := messages[len(messages)-1]
		return len(messages) > sent && last.Kind == model.MessageMusic && last.Media.Title == song.Media.Title
	})

	// Delete takes it off at once, and off Telegram's.
	p.info.sharedDo(layout.Context{}, sharedRemoveMusic, song, l)
	if slices.Contains(songTitles(p.info), song.Media.Title) {
		t.Fatal("the song deleted is still shown")
	}
	waitAudio(t, "Telegram still has the song", func() bool {
		return !slices.Contains(playlistTitles(t, store, me), song.Media.Title)
	})
	waitAudio(t, "the deletion was not told", func() bool {
		h.frame()
		return p.info.modal.toast.Text() == l.T("menu.music_removed")
	})

	// Another's playlist has Forward alone.
	other := newChatInfo(store, new(imageOps), func() {})
	t.Cleanup(other.Destroy)
	other.Open(target)
	awaitShared(t, other)
	other.selectSection(model.SharedSavedMusic)
	awaitShared(t, other)
	if got := other.sharedMenuActions(other.messages[0]); !slices.Equal(got, []sharedAction{sharedForward}) {
		t.Fatalf("another's playlist's menu %v", got)
	}
}

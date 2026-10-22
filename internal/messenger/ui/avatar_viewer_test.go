// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// The avatar in a chat's header opens its photo; the rest of the header
// still opens the chat's info.
func TestChatHeaderAvatarOpensPhoto(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	page := newChatPage(store, func() {})
	defer page.Close()
	var router input.Router
	var avatar, header int
	frame := func() {
		ops := new(op.Ops)
		gtx := sharedContext(ops, image.Pt(900, 800))
		gtx.Source = router.Source()
		if page.headAvatar.Clicked(gtx) {
			avatar++
		}
		if page.header.Clicked(gtx) {
			header++
		}
		layoutChatPage(gtx, model.Chat{ID: 2, Title: "Chat"}, localization.For("en"), func(layout.Context, int64, model.ChatKind, string, unit.Dp) layout.Dimensions {
			return layout.Dimensions{}
		}, nil, nil, func(layout.Context) layout.Dimensions { return layout.Dimensions{} }, page)
		router.Frame(ops)
	}
	click := func(x, y float32) {
		router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(x, y)}, pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(x, y)})
		frame()
		frame()
	}
	frame()
	// The avatar is 40 dp at 16, 8 in the 56 dp header.
	click(36, 28)
	if avatar != 1 || header != 0 {
		t.Fatalf("a click on the avatar: avatar %d, header %d", avatar, header)
	}
	click(200, 25)
	if avatar != 1 || header != 1 {
		t.Fatalf("a click beside the avatar: avatar %d, header %d", avatar, header)
	}
}

// The viewer opens the photo a chat shows at once, and has the older ones
// of its profile after them, page by page as the strip comes to its end,
// placed among all of them as Telegram counts them.
func TestViewerOpensProfilePhotos(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	var images imageOps
	changed := make(chan struct{}, 1)
	v := newPhotoViewer(store, &images, func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	defer v.Destroy()
	if !v.OpenProfile(2) || !v.open {
		t.Fatal("the viewer did not open")
	}
	if v.current != model.ProfilePhotoID(0) {
		t.Fatalf("shows %d, not the photo the chat shows", v.current)
	}
	var router input.Router
	frame := func() {
		gtx := sharedContext(new(op.Ops), image.Pt(800, 600))
		gtx.Source = router.Source()
		images.BeginFrame()
		v.Layout(gtx, localization.For("en"), false)
		images.EndFrame()
	}
	until := func(what string, cond func([]model.Message) bool) []model.Message {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			frame()
			items, _, _, _, _ := v.snapshot()
			if cond(items) {
				return items
			}
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s: %d photos", what, len(items))
			}
			select {
			case <-changed:
			case <-time.After(10 * time.Millisecond):
			}
		}
	}
	items := until("the first page", func(items []model.Message) bool { return len(items) > 1 })
	if len(items) != viewerPage {
		t.Fatalf("%d photos in the first page", len(items))
	}
	if n, amount, ok := v.place(items, 0); !ok || n != 1 || amount != 150 {
		t.Fatalf("the photo the chat shows is %d of %d (%v)", n, amount, ok)
	}
	// Going to the last photo known asks for the next page, until all are.
	for len(items) < 150 {
		before := len(items)
		v.current = items[len(items)-1].Key.MessageID
		items = until("the next page", func(items []model.Message) bool { return len(items) > before })
	}
	for i, m := range items {
		if m.Key.MessageID != model.ProfilePhotoID(i) {
			t.Fatalf("photo %d has id %d", i, m.Key.MessageID)
		}
	}
	if n, amount, ok := v.place(items, len(items)-1); !ok || n != 150 || amount != 150 {
		t.Fatalf("the last photo is %d of %d (%v)", n, amount, ok)
	}
	_, _, exhausted, _, _ := v.snapshot()
	if !exhausted[0] || !exhausted[1] {
		t.Fatalf("all photos known, yet paging on: %v", exhausted)
	}
	// A store that has no profile photos opens nothing.
	plain := newPhotoViewer(newGalleryStore(), &images, func() {})
	defer plain.Destroy()
	if plain.OpenProfile(2) || plain.open {
		t.Fatal("a viewer without profile photos opened")
	}
}

// The viewer of a window of their own goes on paging the photos of a
// profile from where the one that opened it stopped.
func TestProfilePhotosGoOnInWindow(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	var images imageOps
	v := newPhotoViewer(store, &images, func() {})
	defer v.Destroy()
	page, err := store.ProfilePhotos(t.Context(), 2, "", viewerPage)
	if err != nil {
		t.Fatal(err)
	}
	v.openList(2, page.Messages[0], photoList{items: page.Messages, total: page.Total, ended: [2]bool{true, false}, gallery: galleryProfile, offset: page.Next})
	deadline := time.Now().Add(3 * time.Second)
	// Opening asks for the page after those it was given.
	for ; ; time.Sleep(time.Millisecond) {
		items, loading, _, _, _ := v.snapshot()
		if !loading[1] {
			if len(items) != 2*viewerPage || items[viewerPage].Key.MessageID != model.ProfilePhotoID(viewerPage) {
				t.Fatalf("%d photos after the next page", len(items))
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for the next page")
		}
	}
}

// What a profile photo is saved as tells the chat and its place, since it
// is no message.
func TestProfilePhotoFileName(t *testing.T) {
	m := model.Message{Key: model.MessageKey{ChatID: 7, MessageID: model.ProfilePhotoID(2)}}
	if got := photoFileName(m); got != "komarugram-go-avatar-7-3" {
		t.Fatalf("file name %q", got)
	}
	if !model.IsProfilePhoto(m.Key.MessageID) || model.IsProfilePhoto(-5) || model.IsProfilePhoto(42) {
		t.Fatal("profile photos told from messages and stories wrongly")
	}
}

// The avatar at the top of a chat's info opens the photo too.
func TestChatInfoAvatarOpensPhoto(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	var images imageOps
	p := newChatInfo(store, &images, func() {})
	defer p.Destroy()
	var asked int64
	p.openAvatar = func(chat int64) { asked = chat }
	p.drawAvatar = func(gtx layout.Context, _ int64, _ model.ChatKind, _ string, size unit.Dp) layout.Dimensions {
		return layout.Dimensions{Size: image.Pt(gtx.Dp(size), gtx.Dp(size))}
	}
	var router input.Router
	frame := func() {
		ops := new(op.Ops)
		gtx := sharedContext(ops, image.Pt(900, 800))
		gtx.Source = router.Source()
		p.Layout(gtx, localization.For("en"), false)
		router.Frame(ops)
	}
	p.Open(model.Chat{ID: 2, Title: "Chat"})
	awaitShared(t, p)
	for range 30 {
		frame()
		time.Sleep(10 * time.Millisecond)
	}
	// The dialog is centred: the avatar is at its top, under its title.
	for y := float32(60); y < 300 && asked == 0; y += 4 {
		router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(450, y)})
		frame()
		router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: f32.Pt(450, y)}, pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: f32.Pt(450, y)})
		frame()
		frame()
	}
	if asked != 2 {
		t.Fatalf("the avatar asked for the photos of chat %d", asked)
	}
}

// The avatar on the profile page opens the photos of the account's own
// profile.
func TestProfileAvatarOpensPhoto(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	p := newProfilePage(store)
	var asked int64
	p.openAvatar = func(chat int64) { asked = chat }
	h := &focusHarness{draw: func(gtx layout.Context) {
		p.Update(gtx, store.Me(), func() {})
		p.Layout(gtx, store.Me(), localization.For("en"), func(gtx layout.Context, _ int64, _ model.ChatKind, _ string, size unit.Dp) layout.Dimensions {
			return layout.Dimensions{Size: image.Pt(gtx.Dp(size), gtx.Dp(size))}
		}, false, false)
	}}
	h.frame()
	// The avatar is 96 dp, centred, under the page's 24 dp of padding.
	at := f32.Pt(450, 24+48)
	h.router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: at}, pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: at})
	h.frame()
	h.frame()
	if asked != store.Me().ID || asked == 0 {
		t.Fatalf("the avatar asked for the photos of %d, not of the account %d", asked, store.Me().ID)
	}
}

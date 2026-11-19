// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"maps"
	"os"
	"slices"
	"testing"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
)

type albumForwardStore struct {
	*tileStore
	forwarded chan albumForward
}

type albumForward struct {
	ids     []model.MessageID
	options model.ForwardOptions
}

func (s *albumForwardStore) ForwardMessagesWithOptions(_ context.Context, _ int64, ids []model.MessageID, _ int64, options model.ForwardOptions) error {
	s.forwarded <- albumForward{ids: ids, options: options}
	return nil
}

func TestHistoryAlbumSelection(t *testing.T) {
	skipFrames(t)
	checkAlbumSelection(t, nil)
}

func TestHistoryAttachmentSelectionAcrossMessages(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
		for i := 1; i <= 5; i++ {
			m := &messages[i]
			m.Kind, m.SenderName, m.Text = model.MessagePhoto, "", ""
			m.Media = &model.MessageMedia{ID: "photo/selection", MIMEType: "image/jpeg", Width: 400, Height: 120}
		}
		messages[1].GroupedID, messages[2].GroupedID = 7, 7
		messages[3].GroupedID, messages[4].GroupedID = 8, 8
	})
	p := h.page
	var opened []model.MessageID
	p.openPhoto = func(m model.Message) { opened = append(opened, m.Key.MessageID) }
	click := func(id model.MessageID, quarter int) {
		box := h.rowBox(id)
		at := box.Min.Add(image.Pt(box.Dx()*quarter/4, box.Dy()/2))
		h.press(pointer.ButtonPrimary, layout.FPt(at))
	}

	click(6, 2)
	if !slices.Equal(opened, []model.MessageID{6}) {
		t.Fatalf("a photo click outside selection mode opened %v", opened)
	}
	opened = nil
	h.openMenu(1)
	h.choose(actionSelect)
	click(2, 1)
	click(4, 3)
	click(6, 2)
	if len(opened) != 0 {
		t.Fatalf("attachment clicks during selection opened the viewer for %v", opened)
	}
	var selected []model.MessageID
	for _, part := range p.selectedParts() {
		selected = append(selected, part.Key.MessageID)
	}
	if !slices.Equal(selected, []model.MessageID{1, 2, 5, 6}) {
		t.Fatalf("attachment clicks in other messages selected %v", selected)
	}
	click(6, 2)
	if len(opened) != 0 || p.selection.selected[6] {
		t.Fatal("clicking a chosen photo must deselect it")
	}
	p.clearSelection()
	h.frame()
	click(6, 2)
	if !slices.Equal(opened, []model.MessageID{6}) {
		t.Fatalf("a photo click after clearing selection opened %v", opened)
	}
}

// TestRenderAlbumSelection shows a partially selected album and the
// confirmation, including its disabled caption checkbox.
func TestRenderAlbumSelection(t *testing.T) {
	dir := os.Getenv("ALBUM_SELECTION_PNG_DIR")
	if dir == "" {
		t.Skip("set ALBUM_SELECTION_PNG_DIR to a directory")
	}
	skipFrames(t)
	checkAlbumSelection(t, func(h *menuHarness, name string) {
		s := newEffectsShots(t, h, dir)
		s.save(name, s.take())
	})
}

func checkAlbumSelection(t *testing.T, render func(*menuHarness, string)) {
	t.Helper()
	data, err := os.ReadFile(boxPaths(t, "photo")[0])
	if err != nil {
		t.Fatal(err)
	}
	forwarded := make(chan albumForward, 1)
	h := newMenuHarnessOn(t, func(_ *menuStore, messages []model.Message) {
		for i := range messages[:3] {
			m := &messages[i]
			m.Kind, m.GroupedID, m.SenderName = model.MessagePhoto, 7, ""
			m.Media = &model.MessageMedia{ID: "photo/album", MIMEType: "image/jpeg", Width: 400, Height: 225}
			m.Text = ""
		}
		messages[1].Text = "Album caption"
	}, func(s *menuStore) model.ConversationStore {
		return &albumForwardStore{tileStore: &tileStore{menuStore: s, data: data}, forwarded: forwarded}
	})
	p := h.page
	h.still = true
	if render != nil {
		h.settle(t)
	}
	box := h.rowBox(1)
	origin := box.Min.Add(image.Pt(int(mediaInset), int(mediaInset)))
	tiles := p.rows[1].tiles
	tilePoint := func(i int) image.Point { return origin.Add(tiles[i].Min).Add(tiles[i].Size().Div(2)) }
	pick := func(i int) { h.press(pointer.ButtonPrimary, layout.FPt(tilePoint(i))) }
	expect := func(ids ...model.MessageID) {
		t.Helper()
		parts := p.selectedParts()
		got := make([]model.MessageID, 0, len(parts))
		for _, part := range parts {
			got = append(got, part.Key.MessageID)
		}
		if !slices.Equal(got, ids) || p.selectionCount() != len(ids) {
			t.Fatalf("selected %v and counted %d, want %v", got, p.selectionCount(), ids)
		}
	}
	h.press(pointer.ButtonSecondary, layout.FPt(tilePoint(1)))
	h.choose(actionSelect)
	h.frame()
	expect(2)
	pick(2)
	expect(2, 3)
	pick(1)
	expect(3)
	if captions := p.selectedAlbumCaptions(); captions == nil || len(captions) != 0 {
		t.Fatalf("captionless part changes forwarding flags: %v", captions)
	}
	pick(1)
	expect(2, 3)
	gap := image.Pt(origin.X+tiles[1].Max.X+1, tilePoint(1).Y)
	h.press(pointer.ButtonPrimary, layout.FPt(gap))
	expect(1, 2, 3)
	if p.selectedAlbumCaptions() != nil {
		t.Fatal("selecting every part still treats the album as partial")
	}
	h.press(pointer.ButtonPrimary, layout.FPt(gap))
	expect()

	h.press(pointer.ButtonSecondary, layout.FPt(tilePoint(1)))
	h.choose(actionSelect)
	h.frame()
	h.press(pointer.ButtonPrimary, layout.FPt(image.Pt(box.Min.X-4, tilePoint(1).Y)))
	expect(1, 2, 3)
	pick(0)
	expect(2, 3)
	if render != nil {
		render(h, "album-selection")
	}
	h.press(pointer.ButtonPrimary, layout.FPt(image.Pt(box.Min.X-4, int(h.messageAt(4).Y))))
	expect(2, 3, 4)
	p.chats = func() []model.Chat { return []model.Chat{{ID: 9, Kind: model.KindUser, Title: "Friend"}} }
	p.openForward(layout.Context{Ops: new(op.Ops)})
	f := &p.forwarding
	h.frames(3)
	f.rows[9].click.Click()
	h.frames(2)
	if !f.confirming || !slices.Equal(f.dropCaptions.GetValues(), []string{"captions"}) {
		t.Fatal("partial album confirmation does not require omitting captions")
	}
	if render != nil {
		render(h, "album-forward")
	}
	clickOption := func(option string) {
		t.Helper()
		label := localization.For("en").T(option)
		var find func([]input.SemanticNode) (image.Rectangle, bool)
		find = func(nodes []input.SemanticNode) (image.Rectangle, bool) {
			for _, n := range nodes {
				if n.Desc.Label == label {
					return n.Desc.Bounds, true
				}
				if b, ok := find(n.Children); ok {
					return b, true
				}
			}
			return image.Rectangle{}, false
		}
		bounds, ok := find(h.router.AppendSemantics(nil))
		if !ok {
			t.Fatalf("the confirmation has no %q checkbox", label)
		}
		// Gio's label bounds can include the remaining space below the text.
		h.press(pointer.ButtonPrimary, layout.FPt(bounds.Min.Add(image.Pt(8, 8))))
		if !f.modal.Shown() || f.modal.closing {
			t.Fatalf("clicking %q closed the confirmation", label)
		}
	}
	clickOption("history.forward_no_author")
	if !f.options.DropAuthor {
		t.Fatal("other selected messages cannot opt out of attribution")
	}
	clickOption("history.forward_no_caption")
	if !slices.Equal(f.dropCaptions.GetValues(), []string{"captions"}) {
		t.Fatal("the required caption checkbox could be unchecked")
	}
	clickOption("history.forward_no_author")
	f.confirm.click.Click()
	var sent albumForward
	waitFor(t, "forward confirmed", h.frame, func() bool {
		select {
		case sent = <-forwarded:
			return true
		default:
			return false
		}
	})
	if !slices.Equal(sent.ids, []model.MessageID{2, 3, 4}) || !maps.Equal(sent.options.DropAlbumCaptions, map[model.MessageID]bool{2: true, 3: true}) || sent.options.DropCaptions || sent.options.DropAuthor {
		t.Fatalf("forwarded %#v with %+v", sent.ids, sent.options)
	}
}

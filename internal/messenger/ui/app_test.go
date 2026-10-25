// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"runtime"
	"testing"
	"time"
	"weak"

	"gioui.org/layout"
	"gioui.org/op"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/miniapp"
)

type noSavedDialogStore struct{ *mockstore.Store }

func (s noSavedDialogStore) Chats() []model.Chat {
	var chats []model.Chat
	for _, chat := range s.Store.Chats() {
		if chat.Kind != model.KindSaved {
			chats = append(chats, chat)
		}
	}
	return chats
}

func (s noSavedDialogStore) History(chat int64) model.History {
	if chat == s.Me().ID {
		return model.History{}
	}
	return s.Store.History(chat)
}

func (s noSavedDialogStore) HistorySince(chat int64, revision uint64) (model.History, bool) {
	if chat == s.Me().ID {
		return model.History{}, true
	}
	return s.Store.HistorySince(chat, revision)
}

func TestSavedMessagesOpensBeforeDialogExists(t *testing.T) {
	w := &appwindow.Window{Motion: motion.New(func() {})}
	defer w.Motion.Close()
	store := noSavedDialogStore{mockstore.New(time.Now(), 0)}
	a := New(w, store, Services{MiniApps: miniappprefs.New(miniapp.Shared)})
	a.openSection(layout.Context{Ops: new(op.Ops)}, section{kind: sectionSaved})
	if a.selected != store.Me().ID {
		t.Fatalf("selected %d, want own chat %d", a.selected, store.Me().ID)
	}
	chat, ok := a.selectedChat()
	if !ok || chat.ID != store.Me().ID || chat.Kind != model.KindSaved || chat.Title == "" {
		t.Fatalf("saved chat missing: %+v, %t", chat, ok)
	}
}

func TestSearchToggle(t *testing.T) {
	w := &appwindow.Window{Motion: motion.New(func() {})}
	defer w.Motion.Close()
	a := New(w, mockstore.New(time.Now(), 0), Services{MiniApps: miniappprefs.New(miniapp.Shared)})
	gtx := layout.Context{Ops: new(op.Ops)}

	folder := section{kind: sectionFolder, folder: 2}
	a.openSection(gtx, folder)
	a.selected = 3
	a.openSection(gtx, section{kind: sectionSearch})
	a.chats.search.SetText("ко")
	a.selected = 9 // A chat opened from the results stays open.

	a.openSection(gtx, section{kind: sectionSearch})
	if a.section != folder {
		t.Errorf("second search press: section %+v, want back to %+v", a.section, folder)
	}
	if got := a.chats.search.GetText(); got != "" {
		t.Errorf("query %q left after closing search, want it cleared", got)
	}
	if a.selected != 9 {
		t.Errorf("open chat %d, want 9 to stay open", a.selected)
	}

	// From settings, search goes back to settings.
	a.openSection(gtx, section{kind: sectionSettings})
	a.openSection(gtx, section{kind: sectionSearch})
	a.openSection(gtx, section{kind: sectionSearch})
	if a.section.kind != sectionSettings {
		t.Errorf("section %+v, want settings", a.section)
	}
}

// A closed window is not kept by what outlives it: the formula cache of the
// process took the window's ReleaseMemoryLater, and with it the window and
// its whole view, until another window replaced it (a window closed to the
// tray stayed in memory).
func TestClosedWindowIsCollected(t *testing.T) {
	w := &appwindow.Window{Motion: motion.New(func() {})}
	gone := weak.Make(w)
	a := New(w, mockstore.New(time.Now(), 0), Services{MiniApps: miniappprefs.New(miniapp.Shared)})
	a.Close()
	w.Motion.Close()
	a, w = nil, nil
	for i := 0; i < 5 && gone.Value() != nil; i++ {
		runtime.GC()
	}
	if gone.Value() != nil {
		t.Fatal("the window of a closed view is still referenced")
	}
}

// A video of the info's section of videos plays where the same one of the
// chat does: in the viewer, when the settings choose it, not always in
// the external player.
func TestSharedVideoPlaysInViewer(t *testing.T) {
	w := &appwindow.Window{Motion: motion.New(func() {})}
	defer w.Motion.Close()
	a := New(w, mockstore.New(time.Now(), 0), Services{MiniApps: miniappprefs.New(miniapp.Shared)})
	m := model.Message{Key: model.MessageKey{ChatID: 2, MessageID: 7}, Kind: model.MessageVideo,
		Media: &model.MessageMedia{ID: "demo/video", MIMEType: "video/mp4", Width: 1280, Height: 720, Duration: 36 * time.Second}}
	if a.videoEngine(m) == videoExternal {
		t.Skip("the settings play videos outside")
	}
	gtx := layout.Context{Ops: new(op.Ops)}
	a.info.renderer.playVideo(gtx, m, -1, localization.For("en"))
	if !a.viewer.open || a.viewer.current != m.Key.MessageID || a.viewer.autoplay != m.Key.MessageID {
		t.Fatalf("viewer open %v on %d, to play %d", a.viewer.open, a.viewer.current, a.viewer.autoplay)
	}
}

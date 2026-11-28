// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"slices"
	"testing"

	"gioui.org/io/pointer"
	"komarugram/internal/messenger/model"
)

func TestChatRowFolderActions(t *testing.T) {
	f := newFolderMenuFixture(t, "chat-row", image.Pt(1100, 850))
	a := f.app
	a.section = section{kind: sectionAll}
	c := model.Chat{ID: 3, Title: "Команда разработки", Kind: model.KindGroup}
	// Leave a different conversation open throughout the row menu actions.
	a.selected = 2
	f.frame()
	f.frame()
	find := func(id int64) model.Folder {
		t.Helper()
		for _, folder := range a.store.Folders() {
			if id == folder.ID {
				return folder
			}
		}
		t.Fatalf("missing folder %d", id)
		return model.Folder{}
	}
	m := &a.chats.menu
	open := func() {
		t.Helper()
		f.click(c.Title, pointer.ButtonSecondary)
		if !m.open || m.id != c.ID || a.selected != 2 {
			t.Fatalf("wrong row target %d; selected %d", m.id, a.selected)
		}
	}
	settle := func() {
		waitFor(t, "folder write", f.frame, func() bool { return m.folderSave == nil && !m.folderChoice.modal.Shown() })
		f.frame()
	}
	mainPins := model.PinnedOrder(a.store.Chats())
	open()
	f.click("Добавить в папку", pointer.ButtonPrimary)
	f.frame()
	if !m.folderChoice.modal.Shown() || !slices.Equal(model.FolderIDs(m.folderChoice.folders), []int64{1, 3, 4}) {
		t.Fatal("incorrect add destinations")
	}
	f.capture("add-to-folder")
	f.click("Личные", pointer.ButtonPrimary)
	settle()
	if !find(1).Contains(c) || !slices.Contains(find(1).Included, c.ID) {
		t.Fatal("chat was not explicitly included")
	}
	a.openSection(*f.gtx, section{kind: sectionFolder, folder: 1})
	f.frame()
	open()
	f.capture("folder-pin-menu")
	f.click("Закрепить в этой папке", pointer.ButtonPrimary)
	settle()
	if !slices.Equal(find(1).Pinned, []int64{3}) || slices.Contains(find(1).Included, 3) || !slices.Equal(model.PinnedOrder(a.store.Chats()), mainPins) {
		t.Fatal("folder pin changed the wrong list")
	}
	open()
	f.click("Открепить в этой папке", pointer.ButtonPrimary)
	settle()
	if !find(1).Contains(c) || len(find(1).Pinned) != 0 {
		t.Fatal("unpin lost membership")
	}
	open()
	f.click("Убрать из папки", pointer.ButtonPrimary)
	f.frame()
	if !slices.Equal(model.FolderIDs(m.folderChoice.folders), []int64{1, 2}) {
		t.Fatal("incorrect remove destinations")
	}
	f.click("Личные", pointer.ButtonPrimary)
	settle()
	if find(1).Contains(c) || !find(2).Contains(c) || len(find(1).Excluded) != 0 {
		t.Fatal("explicit removal created a spurious exclusion")
	}
	a.openSection(*f.gtx, section{kind: sectionFolder, folder: 2})
	f.frame()
	open()
	f.click("Закрепить в этой папке", pointer.ButtonPrimary)
	settle()
	open()
	f.click("Убрать из папки", pointer.ButtonPrimary)
	f.frame()
	f.click("Группы", pointer.ButtonPrimary)
	settle()
	if find(2).Contains(c) || len(find(2).Pinned) != 0 || !slices.Contains(find(2).Excluded, 3) {
		t.Fatal("rule-based removal did not unpin and exclude")
	}
	a.openSection(*f.gtx, section{kind: sectionAll})
	f.frame()
	open()
	f.click("Добавить в папку", pointer.ButtonPrimary)
	f.frame()
	f.click("Группы", pointer.ButtonPrimary)
	settle()
	if !find(2).Contains(c) || len(find(2).Excluded)+len(find(2).Included) != 0 {
		t.Fatal("restoring the rule added an unnecessary exception")
	}
}

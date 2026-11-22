// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"slices"
	"strings"
	"testing"

	"gioui.org/io/pointer"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
)

type archiveMuteWithoutPremium struct{ model.ArchiveMuteStore }

func (s archiveMuteWithoutPremium) ArchiveMute() model.ArchiveMute {
	state := s.ArchiveMuteStore.ArchiveMute()
	state.CanEnable = false
	return state
}

// Both list layouts switch in place and show the same stories before and
// after filtering the archive. Moving a chat updates the preview and list.
func TestChatArchive(t *testing.T) {
	for _, tc := range []struct {
		name    string
		size    image.Point
		compact bool
	}{
		{"sidebar", image.Pt(1150, 850), false},
		{"compact", image.Pt(900, 650), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFolderMenuFixture(t, tc.name, tc.size)
			a := f.app
			if !tc.compact {
				a.currentAccount = func() string { return "demo" }
			}
			a.openSection(*f.gtx, section{kind: sectionAll})
			f.frame()
			if !a.chatArchiveEnabled() || a.chats.archiveSwitchShown {
				t.Fatal("archive should start enabled with no card for an empty list")
			}
			a.openSection(*f.gtx, section{kind: sectionSettings})
			f.frame()
			a.settings.items[settingsVisibility].click.Click()
			f.frame()
			mute := a.store.(model.ArchiveMuteStore)
			waitFor(t, "server archive rule load", f.frame, func() bool { return a.settings.archiveMute.pending == nil })
			if !tc.compact {
				f.click(a.catalog().T("visibility.pin"), pointer.ButtonPrimary)
				if !a.preferences.VisibilityPinned(a.accountID()) {
					t.Fatal("visibility section did not pin")
				}
				a.settings.archiveMute.source = archiveMuteWithoutPremium{mute}
				f.click("В архив и без уведомлений", pointer.ButtonPrimary)
				if mute.ArchiveMute().Enabled || a.settings.archiveMute.pending != nil || a.settings.toast.Text() != a.catalog().T("privacy.archive_mute_premium_required") {
					t.Fatal("non-Premium attempt did not show the requirement or started a write")
				}
				for range 12 {
					f.frame()
				}
				f.capture("archive-premium-required")
				a.settings.archiveMute.source = mute
				a.settings.toast.Hide()
			}
			f.click("Архив чатов", pointer.ButtonPrimary)
			f.click("В архив и без уведомлений", pointer.ButtonPrimary)
			waitFor(t, "server archive rule save", f.frame, func() bool { return mute.ArchiveMute().Enabled && a.settings.archiveMute.pending == nil })
			if a.chatArchiveEnabled() {
				t.Fatal("server archive rule enabled the local archive card")
			}
			if !tc.compact && !a.preferences.VisibilityOf("other").ChatArchive {
				t.Fatal("the pinned archive switch changed the shared setting")
			}
			f.capture("archive-setting")
			f.click("Архив чатов", pointer.ButtonPrimary)
			if !a.chatArchiveEnabled() {
				t.Fatal("switch did not enable the archive")
			}
			for _, menu := range []string{menuSidebar, menuDrawer} {
				if slices.ContainsFunc(a.navigationSlots(menu), func(e drawerEntry) bool { return e.key.section.kind == sectionArchive }) {
					t.Fatalf("archive is still in %s", menu)
				}
			}
			a.openSection(*f.gtx, section{kind: sectionAll})
			f.frame()
			f.frame()
			if a.compact != tc.compact {
				t.Fatal("wrong navigation layout")
			}
			if !a.chats.storiesShown || a.chats.archiveSwitchShown {
				t.Fatal("missing stories or visible empty archive")
			}
			const group = "Команда разработки"
			currentGroups := func() model.Folder {
				folders := a.store.Folders()
				return folders[slices.IndexFunc(folders, func(f model.Folder) bool { return f.ID == 2 })]
			}
			base := currentGroups()
			draft := base
			draft.ExcludeArchived = true
			if err := a.store.(model.FolderManager).SaveFolder(t.Context(), &base, draft); err != nil {
				t.Fatal(err)
			}
			archive := a.store.(model.ChatArchiveActions)
			if err := archive.SetChatArchived(t.Context(), 4, true); err != nil {
				t.Fatal(err)
			}
			f.frame()
			f.click(group, pointer.ButtonSecondary)
			f.click("В архив", pointer.ButtonPrimary)
			waitFor(t, "archive move in the preview", f.frame, func() bool {
				return a.chats.menu.archiveSave == nil && strings.Contains(a.chats.archiveSwitch.summary, group)
			})
			if _, ok := chatByID(a.store.Chats(), 3); ok {
				t.Fatal("archived group is still in All chats")
			}
			f.click("Группы", pointer.ButtonPrimary)
			if a.section != folderSection(2, false) || slices.Contains(a.chats.shown, 3) {
				t.Fatal("the ordinary folder did not exclude the archived group")
			}
			f.click("Все чаты", pointer.ButtonPrimary)
			if got := a.chats.archiveSwitch.summary; got != group+", Новости Go" {
				t.Fatalf("archive preview: %q", got)
			}
			f.capture("archive-entry-card")
			f.click("Архив чатов", pointer.ButtonPrimary)
			f.frame()
			if a.section.kind != sectionArchive || len(a.chats.shown) != 2 || !slices.Contains(a.chats.shown, 3) || !slices.Contains(a.chats.shown, 4) {
				t.Fatalf("archive has %v in %+v", a.chats.shown, a.section)
			}
			if !a.chats.storiesShown || a.chats.archiveSwitch.archived || strings.Contains(a.chats.archiveSwitch.summary, group) {
				t.Fatal("missing stories or wrong preview on the return card")
			}
			if !tc.compact && !slices.ContainsFunc(a.sidebar.shown, func(e drawerEntry) bool {
				return e.key.section.kind == sectionAll && e.title == "Весь архив" && e.unread == 1
			}) {
				t.Fatal("sidebar did not switch to the archive's folders and counters")
			}
			f.capture("archive-all")
			if tc.compact {
				a.drawer.open(a.overlay, a.layoutDrawer)
				f.frame()
				f.click("Каналы", pointer.ButtonPrimary)
				f.frame()
				if a.section != folderSection(3, true) || !slices.Equal(a.chats.shown, []int64{4}) {
					t.Fatalf("drawer selected %+v with chats %v", a.section, a.chats.shown)
				}
			}
			f.click("Группы", pointer.ButtonPrimary)
			if a.section.kind != sectionArchive || a.section.folder == 0 || !a.chats.storiesShown || !slices.Equal(a.chats.shown, []int64{3}) {
				t.Fatalf("archive folder has %v in %+v", a.chats.shown, a.section)
			}
			f.capture("archive-folder")
			f.click(group, pointer.ButtonSecondary)
			f.click("Закрепить в этой папке", pointer.ButtonPrimary)
			waitFor(t, "archive folder pin", f.frame, func() bool { return a.chats.menu.folderSave == nil })
			if folder := currentGroups(); !folder.ExcludeArchived || !slices.Equal(folder.Pinned, []int64{3}) {
				t.Fatal("pinning inside the archive lost the folder rule or pin")
			}
			f.click(group, pointer.ButtonSecondary)
			f.click("Вернуть из архива", pointer.ButtonPrimary)
			waitFor(t, "chat leaving the archive list", f.frame, func() bool {
				return a.chats.menu.archiveSave == nil && len(a.chats.shown) == 0
			})
			if _, ok := chatByID(a.store.Chats(), 3); !ok || len(a.chats.shown) != 0 {
				t.Fatal("restored group is missing from All chats or still in the archive")
			}
			f.capture("archive-empty-folder")
			f.click("Весь архив", pointer.ButtonPrimary)
			if a.section != (section{kind: sectionArchive}) || !slices.Equal(a.chats.shown, []int64{4}) {
				t.Fatalf("clearing the folder filter shows %v in %+v", a.chats.shown, a.section)
			}
			f.click("Все чаты", pointer.ButtonPrimary)
			if a.section.kind != sectionAll || !a.chats.storiesShown || a.chats.archiveSwitch.summary != "Новости Go" {
				t.Fatal("return card did not restore the main list")
			}
			if err := archive.SetChatArchived(t.Context(), 4, false); err != nil {
				t.Fatal(err)
			}
			f.frame()
			if a.chats.archiveSwitchShown {
				t.Fatal("the card stayed after the last chat left the archive")
			}
			if err := archive.SetChatArchived(t.Context(), 4, true); err != nil {
				t.Fatal(err)
			}
			f.frame()
			f.click("Архив чатов", pointer.ButtonPrimary)
			if err := a.preferences.ChangeVisibility(a.accountID(), func(v *preferences.Visibility) { v.ChatArchive = false }); err != nil {
				t.Fatal(err)
			}
			f.frame()
			if a.section.kind != sectionAll || a.chats.archiveSwitch.summary != "" {
				t.Fatal("disabling the archive left it open or accessible")
			}
			if !mute.ArchiveMute().Enabled {
				t.Fatal("disabling the local archive card disabled the account rule")
			}
		})
	}
}

// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"gio-mw/wdk"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
	"strconv"
)

const (
	menuSidebar    = "sidebar"
	menuDrawer     = "drawer"
	menuMessage    = "message"
	menuChatRow    = "chat_row"
	menuChatHeader = "chat_header"
	menuAttachment = "attachment"
	menuRecent     = "recent_search"
	menuStickerSet = "sticker_set"
	menuPicker     = "picker"
)

type menuPreferences func() preferences.Menus

func (get menuPreferences) layout(id string) preferences.MenuLayout {
	if get == nil {
		return preferences.MenuLayout{}
	}
	return get()[id]
}
func arrangeMenu[T any](get menuPreferences, menu string, available []T, id func(T) string) []T {
	ids := make([]string, len(available))
	byID := make(map[string]T, len(available))
	for i, v := range available {
		ids[i] = id(v)
		byID[ids[i]] = v
	}
	ordered := get.layout(menu).Arrange(ids, false)
	out := make([]T, 0, len(ordered))
	for _, key := range ordered {
		out = append(out, byID[key])
	}
	return out
}

func navigationID(k drawerKey, account string) string {
	if k.theme {
		return "theme"
	}
	switch k.section.kind {
	case sectionAll:
		return "all"
	case sectionFolder:
		return "folder:" + account + ":" + strconv.FormatInt(k.section.folder, 10)
	case sectionSearch:
		return "search"
	case sectionProfile:
		return "profile"
	case sectionSaved:
		return "saved"
	case sectionSettings:
		return "settings"
	case sectionDownloads:
		return "downloads"
	}
	return ""
}

var messageActionIDs = [...]string{"reply", "edit", "copy", "copy", "copy_link", "forward", "forward", "delete", "delete", "select", "select", "emoji_packs", "reacted", "read", "edits", "filter", "translate", "repeat", "save_html", "save", "save_gif"}

func (p *chatPage) messageActionID(a menuAction) string {
	return messageActionIDs[a]
}
func chatRowActionID(a chatRowAction) string {
	switch a {
	case chatRowRead:
		return "read"
	case chatRowUngroup:
		return "ungroup"
	case chatRowCommunity:
		return "community"
	}
	return "pin"
}
func chatHeaderActionID(a chatMenuAction) string {
	return [...]string{"search", "info", "theme", "beginning", "filtered", "leave"}[a]
}

type menuChoice struct {
	id, title string
	icon      wdk.IconWidget
}
type menuCatalog struct {
	id, title string
	items     []menuChoice
}

func menuCatalogs(folders []model.Folder, account string, l localization.Catalog) []menuCatalog {
	var nav []menuChoice
	for _, e := range drawerEntries(folders, nil, false, l) {
		nav = append(nav, menuChoice{navigationID(e.key, account), e.title, e.icon})
	}
	out := []menuCatalog{{menuSidebar, l.T("menus.sidebar"), nav}, {menuDrawer, l.T("menus.drawer"), nav}}
	p := &chatPage{}
	message := menuCatalog{menuMessage, l.T("menus.message"), nil}
	seen := map[string]bool{}
	for a := menuAction(0); a < menuActions; a++ {
		id := messageActionIDs[a]
		if seen[id] {
			continue
		}
		seen[id] = true
		title := p.menuLabel(a, l)
		switch a {
		case actionCopySelected:
			title = l.T("menu.copy_text")
		case actionReacted:
			title = l.T("menus.reactions")
		case actionEmojiPacks:
			title = l.T("menus.emoji_packs")
		}
		message.items = append(message.items, menuChoice{id, title, menuIcon(a)})
	}
	out = append(out, message)
	out = append(out, menuCatalog{menuChatRow, l.T("menus.chat_row"), []menuChoice{{"pin", l.T("chat_row.pin"), iconPinChat}, {"read", l.T("chat_row.read"), iconRead}, {"ungroup", l.T("chat_row.ungroup"), iconUngroup}, {"community", l.T("chat_row.community"), iconKindCommunity}}})
	header := menuCatalog{menuChatHeader, l.T("menus.chat_header"), nil}
	for a := chatMenuAction(0); a < chatMenuActions; a++ {
		title := p.chatMenuLabel(a, l)
		if a == chatMenuInfo {
			title = l.T("menus.chat_info")
		}
		if a == chatMenuLeave {
			title = l.T("menus.leave")
		}
		header.items = append(header.items, menuChoice{chatHeaderActionID(a), title, chatMenuIcon(a)})
	}
	out = append(out, header)
	out = append(out, menuCatalog{menuAttachment, l.T("menus.attachment"), []menuChoice{{"photo", l.T("composer.photo"), iconAttachPhoto}, {"file", l.T("composer.file"), iconAttachFile}, {"tasks", l.T("composer.tasks"), iconAttachTasks}}})
	out = append(out, menuCatalog{menuRecent, l.T("menus.recent"), []menuChoice{{"remove", l.T("search.recent_remove"), iconClear}, {"all", l.T("search.recent_clear_all"), iconDelete}}})
	var picker []menuChoice
	for a := pickerAction(0); a < pickerActions; a++ {
		picker = append(picker, menuChoice{a.id(), a.label(l), a.icon()})
	}
	out = append(out, menuCatalog{menuPicker, l.T("menus.picker"), picker})
	out = append(out, menuCatalog{menuStickerSet, l.T("menus.stickers"), []menuChoice{{"download", l.T("stickers.download_zip"), iconDownload}, {"author", l.T("stickers.author"), iconPersonal}}})
	for _, kind := range []string{"phone", "bank_card", "date"} {
		c := menuCatalog{"entity_" + kind, l.T("menus." + kind), nil}
		switch kind {
		case "phone":
			c.items = []menuChoice{{"copy", l.T("phone.copy"), iconCopy}, {"profile", l.T("phone.profile"), iconProfile}}
		case "bank_card":
			c.items = []menuChoice{{"copy", l.T("card.copy"), iconCopy}, {"links", l.T("menus.bank_links"), iconOpenInBrowser}, {"title", l.T("menus.bank_title"), iconCopy}}
		case "date":
			c.items = []menuChoice{{"copy", l.T("date.copy"), iconCopy}, {"calendar", l.T("date.calendar"), iconCalendar}}
		}
		out = append(out, c)
	}
	return out
}

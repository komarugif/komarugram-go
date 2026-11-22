// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"slices"
	"sort"

	"gio-mw/token"
	"gioui.org/layout"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// chatSwitchRow opens the other list and previews its latest conversations.
type chatSwitchRow struct {
	surface
	snapshot []model.Chat
	archived bool
	catalog  localization.Catalog
	summary  string
}

func (l *chatList) showsArchiveSwitch(sec section) bool {
	return (sec.kind == sectionAll || sec.kind == sectionArchive) && l.archiveEnabled != nil && l.archiveEnabled()
}

func (l *chatList) archiveDestination(gtx layout.Context, sec section) (section, bool) {
	if l.archiveSwitchShown && l.showsArchiveSwitch(sec) && l.archiveSwitch.Clicked(gtx) {
		if sec.kind == sectionArchive {
			return section{kind: sectionAll}, true
		}
		return section{kind: sectionArchive}, true
	}
	return section{}, false
}

func (r *chatSwitchRow) preview(chats []model.Chat, archived bool, l localization.Catalog) string {
	if (r.summary != "" || archived) && r.archived == archived && r.catalog == l && sameChatSnapshot(r.snapshot, chats) {
		return r.summary
	}
	const limit = 8
	var names [limit]*model.Chat
	recent := names[:0]
	count := 0
	for i := range chats {
		c := &chats[i]
		if c.Archived != archived {
			continue
		}
		count++
		// List pins and drafts can change row order without bringing a new message.
		at := sort.Search(len(recent), func(j int) bool { return c.LastTime.After(recent[j].LastTime) })
		if at < limit {
			if len(recent) == limit {
				recent = recent[:limit-1]
			}
			recent = slices.Insert(recent, at, c)
		}
	}

	summary := ""
	if len(recent) > 0 {
		summary = l.ChatTitle(*recent[0])
		for _, c := range recent[1:] {
			summary = l.Format("chat_archive.preview_list", map[string]string{"accumulated": summary, "chat": l.ChatTitle(*c)})
		}
		if more := count - len(recent); more > 0 {
			summary = l.Count("chat_archive.preview_more", more, map[string]string{"chats": summary})
		}
	} else if !archived {
		summary = l.T("chat_archive.main_empty")
	}
	r.snapshot, r.archived, r.catalog, r.summary = chats, archived, l, summary
	return summary
}

func (r *chatSwitchRow) layout(gtx layout.Context, chats []model.Chat, toArchive, narrow bool, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	title, icon := l.T("nav.all"), iconAllChats
	if toArchive {
		title, icon = l.T("nav.archive"), iconArchive
	}
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(chatRowHeight))
	margin := gtx.Dp(chatRowMargin)
	style := surfaceStyle{
		area:   image.Rect(margin, 0, size.X-margin, size.Y),
		radius: gtx.Dp(chatSelectRadius), background: sc.Primary.Color.SetOpacity(0), content: sc.Surface.OnColor,
		button: title,
	}
	return r.surface.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		avatarPx := gtx.Dp(chatAvatarSize)
		avatarAt := image.Pt(gtx.Dp(chatAvatarInset), (size.Y-avatarPx)/2)
		offset(gtx, avatarAt, func(gtx layout.Context) layout.Dimensions {
			return exact(gtx, image.Pt(avatarPx, avatarPx), func(gtx layout.Context) layout.Dimensions {
				fillRounded(gtx, sc.SecondaryContainer.Color, gtx.Constraints.Max, avatarPx/2)
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					px := gtx.Dp(24)
					return exact(gtx, image.Pt(px, px), func(gtx layout.Context) layout.Dimensions {
						return icon(gtx, sc.SecondaryContainer.OnColor)
					})
				})
			})
		})
		if !narrow {
			pad := gtx.Dp(chatRowPadding)
			x := avatarAt.X + avatarPx + pad
			gtx.Constraints = layout.Constraints{Max: image.Pt(max(size.X-x-margin-pad, 0), size.Y)}
			offset(gtx, image.Pt(x, gtx.Dp(11)), func(gtx layout.Context) layout.Dimensions {
				return label(gtx, title, token.TypestyleTitleSmallEmphasized, sc.Surface.OnColor, 1)
			})
			offset(gtx, image.Pt(x, gtx.Dp(33)), func(gtx layout.Context) layout.Dimensions {
				return label(gtx, r.preview(chats, toArchive, l), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 1)
			})
		}
		return layout.Dimensions{Size: size}
	})
}

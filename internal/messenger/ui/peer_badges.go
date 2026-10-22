// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image/color"
	"strings"
	"sync/atomic"
	"time"

	"gioui.org/layout"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/peerbadges"
)

// badgeKey tells one mark of one peer from the others; each has the area of
// its click.
type badgeKey struct {
	peer int64
	kind uint8
}

const (
	badgeKomaruDeveloper uint8 = iota
	badgeKomaruSupporter
	badgeExtera
)

// badgeClickable is the click of the mark badgeKey names. They are kept
// between frames, as a press and its release are in different ones.
func (a *App) badgeClickable(key badgeKey) *widget.Clickable {
	if c := a.badgeClicks[key]; c != nil {
		return c
	}
	if a.badgeClicks == nil || len(a.badgeClicks) > 512 {
		a.badgeClicks = map[badgeKey]*widget.Clickable{}
	}
	c := new(widget.Clickable)
	a.badgeClicks[key] = c
	return c
}

// peerMarks are the marks of the forks of Telegram clients for the peer b
// belongs to, in the order they are drawn: the Komaru faces, then the arrow
// of exteraGram and AyuGram, or the custom emoji it gives. A click on one
// tells what it means in a toast.
func (a *App) peerMarks(b model.Badges, name string, size unit.Dp) []layout.Widget {
	if a.peerBadges == nil || a.preferences.Global().HidePeerBadges {
		return nil
	}
	id := b.User
	if id == 0 {
		id = b.Channel
	}
	mark := a.peerBadges.Lookup(id)
	if mark.Empty() {
		return nil
	}
	channel := b.Channel != 0
	var marks []layout.Widget
	if face := mark.Developer; face != nil {
		text := "badge.komaru_developer"
		if channel {
			text = "badge.komaru_official"
		}
		col := faceColor(*face)
		marks = append(marks, a.badgeButton(badgeKey{id, badgeKomaruDeveloper}, name, text, "", func(gtx layout.Context) layout.Dimensions {
			return a.layoutKomaruMark(gtx, size, true, col)
		}))
	}
	if face := mark.Supporter; face != nil {
		text := "badge.komaru_supporter"
		if mark.Partner {
			text = "badge.komaru_partner"
		}
		col := faceColor(*face)
		marks = append(marks, a.badgeButton(badgeKey{id, badgeKomaruSupporter}, name, text, "", func(gtx layout.Context) layout.Dimensions {
			return a.layoutKomaruMark(gtx, size, false, col)
		}))
	}
	if mark.Arrow != peerbadges.NoArrow || mark.Emoji != 0 {
		official := mark.Arrow == peerbadges.Official
		text := "badge.extera_supporter"
		switch {
		case official && channel:
			text = "badge.extera_official"
		case official:
			text = "badge.extera_developer"
		}
		marks = append(marks, a.badgeButton(badgeKey{id, badgeExtera}, name, text, mark.Text, func(gtx layout.Context) layout.Dimensions {
			return a.layoutExteraMark(gtx, size, official, mark.Emoji)
		}))
	}
	return marks
}

// faceColor is the color a Komaru face is painted in: the list's, or the
// color of the badges around it, with the list's opacity.
func faceColor(f peerbadges.Face) color.NRGBA {
	if f.Theme {
		c := telegramBlue
		c.A = f.Color.A
		return c
	}
	return f.Color
}

// badgeButton makes mark a click that tells, in a toast, what the mark means
// for the peer called name: the text of key, or custom, which the list may
// bring in its place.
func (a *App) badgeButton(key badgeKey, name, text, custom string, mark layout.Widget) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		click := a.badgeClickable(key)
		if click.Clicked(gtx) {
			message := custom
			if message == "" {
				message = a.catalog().Format(text, map[string]string{"name": name})
			}
			a.chats.toast.Show(strings.TrimSpace(message))
		}
		return click.Layout(gtx, mark)
	}
}

// The Premium star, the Komaru faces and the arrow are not drawn by the
// client but taken from emoji sets of Telegram, as the icons of Telegram's own
// clients are. Until a set is known, or where it cannot be had, nothing is
// drawn in their place.
const (
	// premiumIconSet has Telegram's Android icons; its first star is the
	// Premium star.
	premiumIconSet = "TgAndroidIcons"
	premiumIconAlt = "⭐"
	// komaruIconSet has the faces of KomaruGram, in the order of
	// komaruFaceDeveloper and komaruFaceSupporter.
	komaruIconSet       = "KomaruGramAdaptive"
	komaruFaceDeveloper = 0
	komaruFaceSupporter = 1
	// exteraIconSet has the arrow of exteraGram and AyuGram, in the order of
	// exteraArrowSupporter and exteraArrowOfficial: the arrow alone, and the
	// arrow in a rosette.
	exteraIconSet       = "ExteraArrowAdaptive"
	exteraArrowSupport  = 0
	exteraArrowOfficial = 1
	iconSetRetry        = time.Minute
)

// emojiSet is an emoji set kept for drawing marks, loaded in the background:
// from the cache first, and again a minute after a failure.
type emojiSet struct {
	items atomic.Pointer[[]model.PickerItem]
	try   atomic.Int64
}

// load returns the emoji of the set called name, nil until they are loaded.
func (s *emojiSet) load(a *App, name string) []model.PickerItem {
	if items := s.items.Load(); items != nil {
		return *items
	}
	source, ok := a.store.(model.StickerSetStore)
	if !ok {
		return nil
	}
	now := time.Now().UnixNano()
	last := s.try.Load()
	if last != 0 && now-last < int64(iconSetRetry) || !s.try.CompareAndSwap(last, now) {
		return nil
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ref := model.StickerSetRef{Type: "short_name", ShortName: name}
		var items []model.PickerItem
		if cache, ok := a.store.(model.StickerSetCache); ok {
			if set, ok := cache.CachedStickerSet(ctx, ref); ok {
				items = set.Items
			}
		}
		if len(items) == 0 {
			if set, err := source.StickerSet(ctx, ref); err == nil {
				items = set.Items
			}
		}
		if len(items) > 0 {
			s.items.Store(&items)
			if a.window != nil {
				a.window.Invalidate()
			}
		}
	}()
	return nil
}

// layoutPremiumMark draws the Premium star beside a name in a square of size,
// nothing until it is loaded.
func (a *App) layoutPremiumMark(gtx layout.Context, size unit.Dp) layout.Dimensions {
	if id := a.premiumEmoji(); id != 0 {
		return a.layoutCustomEmoji(gtx, id, size)
	}
	return layout.Dimensions{}
}

// premiumEmoji is the custom emoji of the Premium star, 0 until the set is
// loaded, or if it has no star.
func (a *App) premiumEmoji() int64 {
	for _, item := range a.premiumSet.load(a, premiumIconSet) {
		if item.DocumentID != 0 && strings.Contains(item.Emoji, premiumIconAlt) {
			return item.DocumentID
		}
	}
	return 0
}

// komaruEmoji is the custom emoji of a Komaru face, a developer's or a
// supporter's, 0 until the set is loaded.
func (a *App) komaruEmoji(developer bool) int64 {
	items := a.komaruSet.load(a, komaruIconSet)
	i := komaruFaceSupporter
	if developer {
		i = komaruFaceDeveloper
	}
	if i < len(items) {
		return items[i].DocumentID
	}
	return 0
}

// layoutKomaruMark draws a Komaru face in a square of size, in col, nothing
// until it is loaded.
func (a *App) layoutKomaruMark(gtx layout.Context, size unit.Dp, developer bool, col color.NRGBA) layout.Dimensions {
	if id := a.komaruEmoji(developer); id != 0 {
		return a.layoutTintedEmoji(gtx, id, size, col)
	}
	return layout.Dimensions{}
}

// layoutExteraMark draws the arrow of exteraGram and AyuGram in a square of
// size, in Telegram's blue: the arrow alone, or in a rosette for an official
// one. emoji is the custom emoji exteraGram gives a peer in its place, 0 for
// none. Nothing is drawn until what is to be drawn is loaded.
func (a *App) layoutExteraMark(gtx layout.Context, size unit.Dp, official bool, emoji int64) layout.Dimensions {
	if emoji == 0 {
		emoji = a.arrowEmoji(official)
	}
	if emoji == 0 {
		return layout.Dimensions{}
	}
	return a.layoutCustomEmoji(gtx, emoji, size)
}

// arrowEmoji is the custom emoji of the arrow, 0 until the set is loaded.
func (a *App) arrowEmoji(official bool) int64 {
	items := a.arrowSet.load(a, exteraIconSet)
	i := exteraArrowSupport
	if official {
		i = exteraArrowOfficial
	}
	if i < len(items) {
		return items[i].DocumentID
	}
	return 0
}

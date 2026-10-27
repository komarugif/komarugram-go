// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"image/color"
	"math"
	"strings"
	"sync/atomic"
	"time"

	"gioui.org/f32"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/crash"
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
	badgeKomaruStatus
	badgeExtera
	badgeStatus
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
// belongs to, in the order they are drawn: the Komaru faces and the emoji
// status KomaruGram gives, then the arrow of exteraGram and AyuGram, or the
// custom emoji it gives. A click on one tells what it means through tell,
// nil for the chat list's toast.
func (a *App) peerMarks(b model.Badges, name string, size unit.Dp, tell func(text string)) []layout.Widget {
	if a.peerBadges == nil {
		return nil
	}
	g := a.preferences.Global()
	hideArrows, hideFaces := g.HidesPeerArrows(), g.HidesPeerFaces()
	if hideArrows && hideFaces {
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
	if hideFaces {
		mark.Developer, mark.Supporter, mark.Status = nil, nil, nil
	}
	if hideArrows {
		mark.Arrow, mark.Emoji = peerbadges.NoArrow, 0
	}
	if face := mark.Developer; face != nil {
		text := "badge.komaru_developer"
		if channel {
			text = "badge.komaru_official"
		}
		face := *face
		marks = append(marks, a.badgeButton(badgeKey{id, badgeKomaruDeveloper}, tell, name, text, "", func(gtx layout.Context) layout.Dimensions {
			return a.layoutKomaruMark(gtx, size, true, face)
		}))
	}
	if face := mark.Supporter; face != nil {
		face := *face
		marks = append(marks, a.badgeButton(badgeKey{id, badgeKomaruSupporter}, tell, name, "badge.komaru_supporter", "", func(gtx layout.Context) layout.Dimensions {
			return a.layoutKomaruMark(gtx, size, false, face)
		}))
	}
	if status := mark.Status; status != nil {
		status := *status
		marks = append(marks, a.badgeButton(badgeKey{id, badgeKomaruStatus}, tell, name, "badge.komaru_supporter", status.Text, func(gtx layout.Context) layout.Dimensions {
			return a.layoutKomaruStatus(gtx, size, status)
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
		marks = append(marks, a.badgeButton(badgeKey{id, badgeExtera}, tell, name, text, mark.Text, func(gtx layout.Context) layout.Dimensions {
			return a.layoutExteraMark(gtx, size, official, mark.Emoji)
		}))
	}
	return marks
}

// faceColor is the color a part of a Komaru face is painted in: the list's,
// or with theme the color of the badges around it, with the list's opacity.
func faceColor(c color.NRGBA, theme bool) color.NRGBA {
	if theme {
		t := telegramBlue
		t.A = c.A
		return t
	}
	return c
}

// badgeButton makes mark a click that tells, through tell or in the chat
// list's toast when it is nil, what the mark means for the peer called
// name: the text of key, or custom, which the list may bring in its place.
func (a *App) badgeButton(key badgeKey, tell func(text string), name, text, custom string, mark layout.Widget) layout.Widget {
	return a.badgeClick(key, mark, func() {
		// The lists write their texts in Markdown, which a toast does not
		// draw: their bold is left out.
		message := strings.ReplaceAll(custom, "**", "")
		if strings.TrimSpace(message) == "" {
			message = a.catalog().Format(text, map[string]string{"name": name})
		}
		if tell == nil {
			tell = a.chats.toast.Show
		}
		tell(strings.TrimSpace(message))
	})
}

// badgeClick makes mark a click that calls click.
func (a *App) badgeClick(key badgeKey, mark layout.Widget, click func()) layout.Widget {
	return func(gtx layout.Context) layout.Dimensions {
		c := a.badgeClickable(key)
		if c.Clicked(gtx) {
			click()
		}
		return c.Layout(gtx, mark)
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
	// komaruFaceStill, komaruFaceSupporter and komaruFaceDeveloper: the
	// developer's face in a rosette, the supporter's alone, and the
	// developer's again, its rosette turning, which is drawn, the still one
	// only where the set lacks it.
	komaruIconSet       = "KomaruGramAdaptive"
	komaruFaceStill     = 0
	komaruFaceSupporter = 1
	komaruFaceDeveloper = 2
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
		defer crash.Recover("badge icons", nil)
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
		if i >= len(items) {
			i = komaruFaceStill
		}
	}
	if i < len(items) {
		return items[i].DocumentID
	}
	return 0
}

// How a Komaru face is drawn, as KomaruGram does: the face in the middle of
// the mark, a supporter's smaller than a developer's, whose rosette leaves
// a margin of its own, and four sparkles circling it, growing and fading.
const (
	komaruFaceShare      = 0.74
	komaruDeveloperShare = 0.92
	komaruSparkles       = 4
	komaruSparkleOrbit   = 0.47
	komaruSparkleRadius  = 0.13
	// komaruSparkleRoom is how far the sparkles reach out of the face's
	// square, at their largest: the room the mark leaves around it, so that
	// they do not run into the name, the next mark or the lines around.
	komaruSparkleRoom = komaruSparkleOrbit + komaruSparkleRadius - 0.5
	komaruOrbit       = 6 * time.Second
	komaruTwinkle     = 1400 * time.Millisecond
	komaruFrame       = 50 * time.Millisecond
)

// layoutKomaruMark draws a Komaru face in a square of size, in the colors
// of face, with room for the sparkles around it, nothing until it is
// loaded. The sparkles stand still while animations are off.
func (a *App) layoutKomaruMark(gtx layout.Context, size unit.Dp, developer bool, face peerbadges.Face) layout.Dimensions {
	id := a.komaruEmoji(developer)
	if id == 0 {
		return layout.Dimensions{}
	}
	side := gtx.Dp(size)
	share := float32(komaruFaceShare)
	if developer {
		share = komaruDeveloperShare
	}
	inner := unit.Dp(float32(size) * share)
	skip := (side - gtx.Dp(inner)) / 2
	faceGtx := gtx
	faceGtx.Constraints = layout.Exact(image.Pt(gtx.Dp(inner), gtx.Dp(inner)))
	room := int(math.Ceil(float64(side) * komaruSparkleRoom))
	defer op.Offset(image.Pt(room, room)).Push(gtx.Ops).Pop()
	t := op.Offset(image.Pt(skip, skip)).Push(gtx.Ops)
	a.layoutTintedEmoji(faceGtx, id, inner, faceColor(face.Color, face.Theme))
	t.Pop()
	var now time.Duration
	if a.window != nil && a.window.Motion.AnimationsEnabled() {
		now = time.Duration(gtx.Now.UnixNano())
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(komaruFrame)})
	}
	drawKomaruSparkles(gtx.Ops, side, faceColor(face.Particles, face.ParticlesTheme), now)
	return layout.Dimensions{Size: image.Pt(side+2*room, side+2*room)}
}

// drawKomaruSparkles draws the sparkles around a Komaru face in a square
// of side pixels, as they are at now: they go round once in komaruOrbit,
// and each grows and brightens, then shrinks and fades, in komaruTwinkle.
func drawKomaruSparkles(ops *op.Ops, side int, col color.NRGBA, now time.Duration) {
	phase := func(period time.Duration) float64 {
		return float64(now%period) / float64(period)
	}
	s := float64(side)
	center := s / 2
	turn, twinkle := phase(komaruOrbit), phase(komaruTwinkle)
	for i := range komaruSparkles {
		angle := 2 * math.Pi * (turn + (float64(i)+0.5)/komaruSparkles)
		grow := 0.5 + 0.5*math.Sin(2*math.Pi*(twinkle+float64(i)*0.27))
		radius := s * komaruSparkleRadius * (0.55 + 0.45*grow)
		orbit := s * komaruSparkleOrbit
		at := f32.Pt(float32(center+math.Cos(angle)*orbit), float32(center+math.Sin(angle)*orbit))
		c := col
		c.A = uint8(float64(c.A) * (0.35 + 0.65*grow))
		paint.FillShape(ops, c, clip.Outline{Path: sparkle(ops, at, float32(radius))}.Op())
	}
}

// sparkle is a star of four points around center, radius from a point to
// the center.
func sparkle(ops *op.Ops, center f32.Point, radius float32) clip.PathSpec {
	inner := radius * 0.3
	var p clip.Path
	p.Begin(ops)
	p.MoveTo(center.Add(f32.Pt(0, -radius)))
	for _, d := range []f32.Point{{X: inner, Y: -inner}, {X: radius}, {X: inner, Y: inner}, {Y: radius}, {X: -inner, Y: inner}, {X: -radius}, {X: -inner, Y: -inner}} {
		p.LineTo(center.Add(d))
	}
	p.Close()
	return p.End()
}

// layoutKomaruStatus draws the emoji status KomaruGram gives in a square of
// size, nothing until its emoji, or the set it is in, is loaded.
func (a *App) layoutKomaruStatus(gtx layout.Context, size unit.Dp, status peerbadges.Status) layout.Dimensions {
	if id := a.komaruStatusEmoji(status); id != 0 {
		return a.layoutCustomEmoji(gtx, id, size)
	}
	return layout.Dimensions{}
}

// komaruStatusEmoji is the custom emoji of status, 0 until the set it is
// taken from is loaded, or if the set has no emoji at its place.
func (a *App) komaruStatusEmoji(status peerbadges.Status) int64 {
	if status.Emoji != 0 {
		return status.Emoji
	}
	set := a.statusSets[status.Set]
	if set == nil {
		if a.statusSets == nil {
			a.statusSets = map[string]*emojiSet{}
		}
		set = new(emojiSet)
		a.statusSets[status.Set] = set
	}
	if items := set.load(a, status.Set); status.Index <= len(items) {
		return items[status.Index-1].DocumentID
	}
	return 0
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

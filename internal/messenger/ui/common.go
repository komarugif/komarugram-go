// SPDX-License-Identifier: Unlicense OR MIT

// Package ui is the messenger's desktop-like interface: a sidebar of
// sections, a resizable chat list and the page of the open chat, profile or
// settings.
package ui

import (
	"fmt"
	"image"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/text"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/program"
)

func scheme(gtx layout.Context) *token.Scheme {
	return wdk.GetMaterialTheme(gtx).Scheme
}

// label draws single or multi line text; maxLines 1 truncates with an
// ellipsis.
func label(gtx layout.Context, txt string, style token.Typestyle, color token.MatColor, maxLines int) layout.Dimensions {
	return wdk.LayoutLabel(gtx, wdk.LabelStyle{Typestyle: style, Color: color, MaxLines: maxLines}, txt)
}

// centeredLabel draws text centered in the available width.
func centeredLabel(gtx layout.Context, txt string, style token.Typestyle, color token.MatColor, maxLines int) layout.Dimensions {
	return wdk.LayoutLabel(gtx, wdk.LabelStyle{Typestyle: style, Color: color, MaxLines: maxLines, Alignment: text.Middle}, txt)
}

// fillRect fills a rectangle of the given size at the current origin.
func fillRect(gtx layout.Context, color token.MatColor, size image.Point) {
	paint.FillShape(gtx.Ops, color.AsNRGBA(), clip.Rect{Max: size}.Op())
}

// fillRounded fills a rounded rectangle of the given size.
func fillRounded(gtx layout.Context, color token.MatColor, size image.Point, radius int) {
	paint.FillShape(gtx.Ops, color.AsNRGBA(), clip.UniformRRect(image.Rectangle{Max: size}, radius).Op(gtx.Ops))
}

// offset lays out w at off.
func offset(gtx layout.Context, off image.Point, w layout.Widget) layout.Dimensions {
	defer op.Offset(off).Push(gtx.Ops).Pop()
	return w(gtx)
}

// exact lays out w with exact constraints of the given size.
func exact(gtx layout.Context, size image.Point, w layout.Widget) layout.Dimensions {
	gtx.Constraints = layout.Exact(size)
	return w(gtx)
}

// Avatar colors, as used for user pictures by Telegram Desktop.
var avatarColors = []token.MatColor{
	token.NewMatColorFromHexRGB(0xE17076),
	token.NewMatColorFromHexRGB(0xFAA774),
	token.NewMatColorFromHexRGB(0xA695E7),
	token.NewMatColorFromHexRGB(0x7BC862),
	token.NewMatColorFromHexRGB(0x6EC9CB),
	token.NewMatColorFromHexRGB(0x65AADD),
	token.NewMatColorFromHexRGB(0xEE7AAE),
}

var white = token.NewMatColorFromHexRGB(0xFFFFFF)

// avatarColorIndex picks a color for an id. Ids of groups and channels are
// negative, so the remainder is brought into range.
func avatarColorIndex(id int64) int {
	n := int64(len(avatarColors))
	return int((id%n + n) % n)
}

// avatar draws a round picture of the given size: initials on a color picked
// by id, or the bookmark for Saved Messages.
func avatar(gtx layout.Context, id int64, kind model.ChatKind, title string, size unit.Dp) layout.Dimensions {
	px := gtx.Dp(size)
	sz := image.Pt(px, px)
	color := avatarColors[avatarColorIndex(id)]
	if kind == model.KindSaved {
		color = avatarColors[5]
	}
	paint.FillShape(gtx.Ops, color.AsNRGBA(), avatarShape(gtx, sz).Op(gtx.Ops))
	gtx.Constraints = layout.Exact(sz)
	if kind == model.KindSaved {
		iconPx := px / 2
		off := (px - iconPx) / 2
		offset(gtx, image.Pt(off, off), func(gtx layout.Context) layout.Dimensions {
			return exact(gtx, image.Pt(iconPx, iconPx), func(gtx layout.Context) layout.Dimensions {
				return iconSaved(gtx, white)
			})
		})
		return layout.Dimensions{Size: sz}
	}
	style := token.TypestyleTitleMediumEmphasized
	if size >= 72 {
		style = token.TypestyleHeadlineMedium
	} else if size < 40 {
		style = token.TypestyleLabelLargeEmphasized
	}
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = image.Point{}
		return label(gtx, initials(title), style, white, 1)
	})
	return layout.Dimensions{Size: sz}
}

// initialsCache keeps the initials of titles: an avatar without a photo
// draws them every frame, in every row of a list. Safe for the windows'
// goroutines; it starts over once it is large.
var initialsCache struct {
	sync.RWMutex
	of map[string]string
}

// initials returns up to two initial letters of a title.
func initials(title string) string {
	initialsCache.RLock()
	out, ok := initialsCache.of[title]
	initialsCache.RUnlock()
	if ok {
		return out
	}
	out = titleInitials(title)
	initialsCache.Lock()
	if initialsCache.of == nil || len(initialsCache.of) >= 4096 {
		initialsCache.of = map[string]string{}
	}
	initialsCache.of[title] = out
	initialsCache.Unlock()
	return out
}

// titleInitials are the initials of title, made.
func titleInitials(title string) string {
	var out []rune
	for _, word := range strings.Fields(title) {
		for _, r := range word {
			if unicode.IsLetter(r) || unicode.IsDigit(r) {
				out = append(out, unicode.ToUpper(r))
				break
			}
		}
		if len(out) == 2 {
			break
		}
	}
	return string(out)
}

// chatTime formats the time of the last message like chat lists do: the
// time today, the weekday this week, the date before.
func chatTime(t, now time.Time, l localization.Catalog) string {
	y1, m1, d1 := t.Date()
	y2, m2, d2 := now.Date()
	switch {
	case y1 == y2 && m1 == m2 && d1 == d2:
		return t.Format("15:04")
	case now.Sub(t) < 6*24*time.Hour:
		return l.T(fmt.Sprintf("weekday.%d", t.Weekday()))
	default:
		return t.Format("02.01.06")
	}
}

// groupDigits formats n with spaces between thousands: 48 210.
func groupDigits(n int) string {
	s := fmt.Sprint(n)
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// chatStatus returns the line under a chat title.
func chatStatus(c model.Chat, l localization.Catalog) string {
	switch c.Kind {
	case model.KindGroup:
		return l.Count("status.members", c.Members, map[string]string{"count": groupDigits(c.Members)})
	case model.KindChannel:
		return l.Count("status.subscribers", c.Members, map[string]string{"count": groupDigits(c.Members)})
	case model.KindBot:
		return l.T("status.bot")
	case model.KindSaved:
		return l.T("status.saved")
	case model.KindCommunity:
		return l.Count("community.status", c.Members, map[string]string{"count": groupDigits(c.Members)})
	default:
		return l.T("status.recently")
	}
}

// openBrowser opens target in the user's browser. It blocks until the
// system has taken the link. The tests replace it: a test that clicks
// would open the user's browser.
var openBrowser = openSystemBrowser

// openSystemBrowser is openBrowser. macOS and Haiku both have open, which
// takes a link or a file to the program the system names for it.
func openSystemBrowser(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin", "haiku":
		cmd = program.Command("open", target)
	case "windows":
		cmd = program.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = program.Command("xdg-open", target)
	}
	return cmd.Run()
}

// pill draws text on a rounded plate, like service messages in chats.
func pill(gtx layout.Context, txt string) layout.Dimensions {
	sc := scheme(gtx)
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			fillRounded(gtx, sc.SurfaceContainerHigh, gtx.Constraints.Min, gtx.Constraints.Min.Y/2)
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 6, Bottom: 6, Left: 14, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, txt, token.TypestyleLabelLarge, sc.SurfaceVariant.OnColor, 1)
			})
		}),
	)
}

// card draws content on a rounded surface.
func card(gtx layout.Context, content layout.Widget, padding unit.Dp) layout.Dimensions {
	sc := scheme(gtx)
	return layout.Stack{}.Layout(gtx,
		layout.Expanded(func(gtx layout.Context) layout.Dimensions {
			fillRounded(gtx, sc.Surface.Color, gtx.Constraints.Min, gtx.Dp(16))
			return layout.Dimensions{Size: gtx.Constraints.Min}
		}),
		layout.Stacked(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.UniformInset(padding).Layout(gtx, content)
		}),
	)
}

func vspace(dp unit.Dp) layout.FlexChild {
	if i := int(dp); float32(i) == float32(dp) && i >= 0 && i < len(vspaces) {
		return layout.Rigid(vspaces[i])
	}
	return layout.Rigid(layout.Spacer{Height: dp}.Layout)
}

// vspaces are the spacers of whole dp up to 32, made once: a method value
// made for each spacer, in every frame, was a closure on the heap.
var vspaces = func() (spaces [33]layout.Widget) {
	for i := range spaces {
		spaces[i] = layout.Spacer{Height: unit.Dp(i)}.Layout
	}
	return spaces
}()

// inRect lays w out to fill rect exactly.
func inRect(gtx layout.Context, rect image.Rectangle, w layout.Widget) layout.Dimensions {
	child := gtx
	child.Constraints = layout.Exact(rect.Size())
	return offset(child, rect.Min, w)
}

// selectionMark is the round mark of a thing that can be picked, d wide: a
// check on the primary color when it is picked, an outline when not.
func selectionMark(gtx layout.Context, d int, picked bool) layout.Dimensions {
	sc := scheme(gtx)
	size := image.Pt(d, d)
	if picked {
		paint.FillShape(gtx.Ops, sc.Primary.Color.AsNRGBA(), clip.Ellipse{Max: size}.Op(gtx.Ops))
		inset := d / 6
		offset(gtx, image.Pt(inset, inset), func(gtx layout.Context) layout.Dimensions {
			return exact(gtx, image.Pt(d-2*inset, d-2*inset), func(gtx layout.Context) layout.Dimensions {
				return iconDone(gtx, sc.Primary.OnColor)
			})
		})
		return layout.Dimensions{Size: size}
	}
	width := max(gtx.Dp(2), 1)
	ring := clip.Ellipse{Min: image.Pt(width/2, width/2), Max: size.Sub(image.Pt(width/2, width/2))}
	paint.FillShape(gtx.Ops, sc.SurfaceVariant.OnColor.AsNRGBA(), clip.Stroke{Path: ring.Path(gtx.Ops), Width: float32(width)}.Op())
	return layout.Dimensions{Size: size}
}

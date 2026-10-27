// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"image"
	"image/color"
	_ "image/jpeg"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/font"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"github.com/gotd/td/telegram/thumbnail"

	"komarugram/internal/messenger/chatmedia"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

const (
	// previewThumbSize is the side of a thumbnail before a last message,
	// a little over Telegram Desktop's 16px for the larger text here.
	previewThumbSize = unit.Dp(18)
	// previewEmojiPlay is how long the custom emoji of a last message
	// play from when it is first shown: twice, as Telegram Desktop's list
	// plays them, an emoji lasting three seconds at most.
	previewEmojiPlay = 6 * time.Second
	// previewThumbRetry is how long a thumbnail that failed waits before it
	// is asked for again, doubled each time it fails again, up to
	// previewThumbRetryMost: shown again and again, it is not asked of
	// Telegram each time.
	previewThumbRetry     = 30 * time.Second
	previewThumbRetryMost = 30 * time.Minute
)

// previewMedia is what draws the pictures of the list's last messages:
// their thumbnails and custom emoji.
type previewMedia struct {
	media  *chatmedia.Manager
	images *imageOps
	// failed are the thumbnails that failed to load, by media, and when
	// they may be asked for again.
	failed map[string]thumbFailure
}

type thumbFailure struct {
	retry time.Time
	times int
	// asked is set once it was asked for again.
	asked bool
}

// previewLine is the line of a chat under its title in the list: its last
// message as Telegram Desktop's list draws it (Dialogs::Ui::MessageView):
// who wrote it, the mark of a forward, thumbnails of its media, the
// interface's words for what it is in the color of links, and its text
// with italic, underline, strike, code, spoilers and custom emoji. What it
// is drawn of is made again only when the message or the language changes.
type previewLine struct {
	// made, catalog, chat, channel and emojiPx are what the line was made
	// for.
	made    model.Preview
	catalog localization.Catalog
	chat    string
	channel bool
	emojiPx int
	ok      bool
	// draft is the draft shown in place of the message, nil for none.
	draft *model.Draft

	// sender is who wrote the message, or the mark of a draft, drafted
	// set then, as its line begins; forwarded marks a forward.
	sender             string
	drafted, forwarded bool
	// runs are the line's text: the media words first, words runs of
	// them, then the message's text.
	runs  []model.TextRun
	words int
	// emoji are the custom emoji of runs, the zero Message for the others.
	emoji []model.Message
	// thumbs are the media of the thumbnails, downloaded only for those
	// without an inline picture, and blurred their inline pictures,
	// decoded once when first drawn, decoded set then.
	thumbs  []model.Message
	blurred []image.Image
	decoded []bool
	// shown is when the message was first drawn, from when its custom emoji
	// play.
	shown time.Time
	// act is what someone does in the chat now, drawn in the line's place.
	act activityLine
}

// update makes the line again when c's last message, or what it is worded
// with, changed since it was made.
func (p *previewLine) update(c model.Chat, title string, catalog localization.Catalog, emojiPx int) {
	channel := c.Kind == model.KindChannel
	// A draft takes the message's place, as Telegram Desktop's list shows
	// it, unless there are messages unread; a forum's are its topics'.
	draft := c.Draft
	if c.Unread > 0 || c.Forum {
		draft = nil
	}
	if p.ok && p.made.Equal(c.Last) && p.draft.Equal(draft) && p.catalog == catalog && p.chat == title && p.channel == channel && p.emojiPx == emojiPx {
		return
	}
	if !p.ok || !p.made.Equal(c.Last) || !p.draft.Equal(draft) {
		p.shown = time.Time{}
	}
	p.made, p.draft, p.catalog, p.chat, p.channel, p.emojiPx, p.ok = c.Last, draft, catalog, title, channel, emojiPx, true
	sender, words, text := catalog.PreviewLine(c.Last, title, channel)
	entities, thumbs := c.Last.Entities, c.Last.Thumbs
	p.drafted, p.forwarded = draft != nil, c.Last.Forwarded
	if draft != nil {
		sender, words, thumbs, p.forwarded = catalog.T("preview.draft"), "", nil, false
		text, entities = model.PreviewText(draft.Text, draft.Entities)
	}
	p.sender = ""
	if sender != "" {
		p.sender = sender + ": "
	}
	p.runs = p.runs[:0]
	if words != "" {
		if text != "" {
			words += ", "
		}
		p.runs = append(p.runs, model.TextRun{Text: words})
	}
	p.words = len(p.runs)
	if text != "" {
		p.runs = append(p.runs, model.TextRuns(text, entities)...)
	}
	p.emoji = zeroed(p.emoji, len(p.runs))
	for i, r := range p.runs {
		if r.Emoji != 0 {
			// Decoded in the pixels it takes, as the badges' are.
			p.emoji[i] = customEmojiMessage(r.Emoji, emojiPx)
		}
	}
	p.thumbs = p.thumbs[:0]
	for _, t := range thumbs {
		p.thumbs = append(p.thumbs, model.Message{Key: model.MessageKey{ChatID: c.ID, MessageID: t.Message}, Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: t.ID, MIMEType: "image/jpeg"}})
	}
	p.blurred, p.decoded = zeroed(p.blurred, len(p.thumbs)), zeroed(p.decoded, len(p.thumbs))
}

// decodeBlurred is the picture of b, a preview's blurred one: a stripped
// thumbnail, as Telegram sends it, or a JPEG. It is nil for none.
func decodeBlurred(b []byte) image.Image {
	if len(b) > 0 && b[0] == 1 {
		var err error
		if b, err = thumbnail.Expand(b); err != nil {
			return nil
		}
	}
	if len(b) == 0 {
		return nil
	}
	im, _, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		return nil
	}
	return im
}

// layout draws the line in the width of the constraints, from their top
// left: the sender and the media words in accent, the mark of a draft in
// drafted, the text in fg.
func (p *previewLine) layout(gtx layout.Context, pm *previewMedia, fg, accent, drafted token.MatColor, animate bool) layout.Dimensions {
	width := gtx.Constraints.Max.X
	gtx.Constraints.Min = image.Point{}
	x := 0
	if p.sender != "" {
		senderGtx := gtx
		senderGtx.Constraints.Max.X = max(width/2, 0)
		col := accent
		if p.drafted {
			col = drafted
		}
		x += label(senderGtx, p.sender, token.TypestyleBodyMedium, col, 1).Size.X
	}
	if p.forwarded {
		// As Telegram Desktop marks a forward before the text.
		px := gtx.Dp(16)
		if x+px <= width {
			offset(gtx, image.Pt(x, gtx.Dp(2)), func(gtx layout.Context) layout.Dimensions {
				return exact(gtx, image.Pt(px, px), func(gtx layout.Context) layout.Dimensions {
					return iconForward(gtx, fg)
				})
			})
			x += px + gtx.Dp(2)
		}
	}
	if len(p.thumbs) > 0 {
		px, gap := gtx.Dp(previewThumbSize), gtx.Dp(2)
		for i := range p.thumbs {
			if x+px+gap > width {
				break
			}
			offset(gtx, image.Pt(x, gtx.Dp(1)), func(gtx layout.Context) layout.Dimensions {
				return p.layoutThumb(gtx, pm, i, px, fg)
			})
			x += px + gap
		}
		x += gtx.Dp(3)
	}
	if len(p.runs) == 0 || x >= width {
		return layout.Dimensions{Size: image.Pt(x, gtx.Dp(20))}
	}
	if p.shown.IsZero() {
		p.shown = gtx.Now
	}
	textGtx := gtx
	textGtx.Constraints.Max.X = width - x
	dims := offset(textGtx, image.Pt(x, 0), func(gtx layout.Context) layout.Dimensions {
		return p.layoutText(gtx, pm, fg, accent, animate && gtx.Now.Before(p.shown.Add(previewEmojiPlay)))
	})
	return layout.Dimensions{Size: image.Pt(x+dims.Size.X, dims.Size.Y)}
}

// layoutThumb draws thumbnail i in a square of px: rounded, a video
// message's round, a video's with a play mark.
func (p *previewLine) layoutThumb(gtx layout.Context, pm *previewMedia, i, px int, fg token.MatColor) layout.Dimensions {
	size := image.Pt(px, px)
	t := p.made.Thumbs[i]
	var shape clip.Op
	if t.Round {
		shape = clip.Ellipse{Max: size}.Op(gtx.Ops)
	} else {
		shape = clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(3)).Op(gtx.Ops)
	}
	defer shape.Push(gtx.Ops).Pop()
	im := p.thumbImage(gtx, pm, i, size)
	if im == nil {
		paint.Fill(gtx.Ops, fg.SetOpacity(.12).AsNRGBA())
	} else {
		gtx.Constraints = layout.Exact(size)
		widget.Image{Src: pm.images.Op(im), Fit: widget.Cover}.Layout(gtx)
	}
	if t.Spoiler {
		// Telegram Desktop covers it with its spoiler's dots; here the
		// blurred picture is dimmed.
		paint.Fill(gtx.Ops, color.NRGBA{A: 0x50})
	}
	if t.Video {
		drawMiniPlay(gtx, size)
	}
	return layout.Dimensions{Size: size}
}

// thumbImage is the picture of thumbnail i in size: Telegram's inline
// picture of the media, which comes with the message, and its media only
// for one that has none, once loaded. At the size of a list's thumbnail
// the inline picture, a few dozen pixels, is as much as the media shows,
// and a list of new chats does not cost a download a row. Media under a
// spoiler is never loaded.
func (p *previewLine) thumbImage(gtx layout.Context, pm *previewMedia, i int, size image.Point) image.Image {
	t := p.made.Thumbs[i]
	if !p.decoded[i] {
		p.blurred[i], p.decoded[i] = decodeBlurred(t.Blurred), true
	}
	if p.blurred[i] != nil || pm == nil || t.Spoiler || t.ID == "" {
		return p.blurred[i]
	}
	return pm.thumb(gtx, p.thumbs[i], size)
}

// thumb is the picture of m in size, nil until it has loaded. One that
// failed is not asked for again until its time comes.
func (pm *previewMedia) thumb(gtx layout.Context, m model.Message, size image.Point) image.Image {
	id := m.Media.ID
	f, failed := pm.failed[id]
	if failed {
		if gtx.Now.Before(f.retry) {
			return nil
		}
		if !f.asked {
			// Its time came: asked again, the error it left forgotten.
			pm.media.Retry(m)
			f.asked = true
			pm.failed[id] = f
		}
	}
	st := pm.media.StatusFit(m, false, size, true)
	if st.Err != nil {
		if pm.failed == nil {
			pm.failed = map[string]thumbFailure{}
		}
		wait := min(previewThumbRetry<<min(f.times, 10), previewThumbRetryMost)
		pm.failed[id] = thumbFailure{retry: gtx.Now.Add(wait), times: f.times + 1}
		// Drawn again then, to ask.
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(wait)})
		return nil
	}
	if failed && st.Frame != nil {
		delete(pm.failed, id)
	}
	if st.Frame != nil {
		return st.Frame
	}
	return st.Preview
}

// drawMiniPlay draws the mark of a video over its thumbnail of size: a
// white triangle on a dark circle, as Telegram Desktop's dialogsMiniPlay.
func drawMiniPlay(gtx layout.Context, size image.Point) {
	d := float32(size.X) * .6
	c := f32.Pt(float32(size.X)/2, float32(size.Y)/2)
	circle := clip.Ellipse{Min: image.Pt(int(c.X-d/2), int(c.Y-d/2)), Max: image.Pt(int(c.X+d/2), int(c.Y+d/2))}
	paint.FillShape(gtx.Ops, color.NRGBA{A: 0x80}, circle.Op(gtx.Ops))
	s := d / 2.6
	var path clip.Path
	path.Begin(gtx.Ops)
	path.MoveTo(f32.Pt(c.X-s*.45, c.Y-s*.6))
	path.LineTo(f32.Pt(c.X+s*.65, c.Y))
	path.LineTo(f32.Pt(c.X-s*.45, c.Y+s*.6))
	path.Close()
	paint.FillShape(gtx.Ops, color.NRGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}, clip.Outline{Path: path.End()}.Op())
}

// layoutText draws the runs on one line, one after the other, cut with an
// ellipsis where they no longer fit. Each is a label of its own, which the
// shaper keeps between frames, as the plain line of a chat is.
func (p *previewLine) layoutText(gtx layout.Context, pm *previewMedia, fg, accent token.MatColor, animate bool) layout.Dimensions {
	theme := wdk.GetMaterialTheme(gtx)
	ty := theme.Typescale[token.TypestyleBodyMedium]
	lineHeight := gtx.Sp(ty.LineHeight)
	width := gtx.Constraints.Max.X
	height, x := lineHeight, 0
	for i, run := range p.runs {
		room := width - x
		if room <= 0 {
			break
		}
		col := fg
		if i < p.words {
			col = accent
		}
		f := font.Font{Typeface: ty.Font, Weight: ty.Weight}
		if run.Italic {
			f.Style = font.Italic
		}
		if run.Code {
			f.Typeface = theme.Typescale[token.TypestylePreformatted].Font
		}
		if msg := p.emoji[i]; msg.Media != nil && pm != nil && !run.Spoiler {
			// Drawn in place of its fallback emoji, which shows until it
			// has loaded.
			px := p.emojiPx
			if frame := pm.media.StatusFit(msg, animate, image.Pt(px, px), false).Frame; frame != nil {
				if px > room {
					x += p.ellipsis(gtx, x, f, ty, col)
					break
				}
				if pm.media.TextColored(msg) {
					frame = pm.images.Colored(frame, col.AsNRGBA())
				}
				offset(gtx, image.Pt(x, (lineHeight-px)/2), func(gtx layout.Context) layout.Dimensions {
					return drawImage(gtx, pm.images, frame, image.Pt(px, px))
				})
				x += px
				continue
			}
		}
		// The last is laid out in the room left, which cuts it with an
		// ellipsis. Another is laid out whole first, for where the next
		// begins: when it fits, it is drawn as it is; otherwise again in
		// the room, and is the last.
		last := i == len(p.runs)-1
		measure := gtx
		measure.Constraints = layout.Constraints{Max: image.Pt(room, gtx.Constraints.Max.Y)}
		if !last {
			measure.Constraints.Max.X = 1 << 20
		}
		macro := op.Record(gtx.Ops)
		dims := runLabel(measure, run.Text, f, ty, col)
		call := macro.Stop()
		cut := dims.Size.X > room
		if cut {
			fit := gtx
			fit.Constraints = layout.Constraints{Max: image.Pt(room, gtx.Constraints.Max.Y)}
			macro = op.Record(gtx.Ops)
			dims = runLabel(fit, run.Text, f, ty, col)
			call = macro.Stop()
		}
		height = max(height, dims.Size.Y)
		size := dims.Size
		offset(gtx, image.Pt(x, 0), func(gtx layout.Context) layout.Dimensions {
			if run.Spoiler {
				// Covered, as the history covers a spoiler not revealed:
				// the list reveals none. The cover is as high as the line,
				// whatever the run's own height, a custom emoji's fallback
				// lower than the text, and goes on in the runs beside it.
				join := spoilerJoin{left: i > 0 && p.runs[i-1].Spoiler, right: i+1 < len(p.runs) && p.runs[i+1].Spoiler}
				paint.FillShape(gtx.Ops, fg.SetOpacity(.28).AsNRGBA(), spoilerShape(gtx, image.Pt(size.X, lineHeight), join).Op(gtx.Ops))
				return dims
			}
			call.Add(gtx.Ops)
			if run.Underline || run.Strike {
				y := size.Y - gtx.Dp(3)
				if run.Strike {
					y = size.Y / 2
				}
				paint.FillShape(gtx.Ops, col.AsNRGBA(), clip.Rect(image.Rect(0, y, size.X, y+max(gtx.Dp(1), 1))).Op())
			}
			return dims
		})
		x += size.X
		if cut {
			break
		}
	}
	return layout.Dimensions{Size: image.Pt(x, height)}
}

// ellipsis draws the ellipsis that ends a line cut at x, and returns its
// width.
func (p *previewLine) ellipsis(gtx layout.Context, x int, f font.Font, ty token.TypeInfo, col token.MatColor) int {
	return offset(gtx, image.Pt(x, 0), func(gtx layout.Context) layout.Dimensions {
		return runLabel(gtx, "…", f, ty, col)
	}).Size.X
}

// runLabel draws txt on one line in f, of ty's size and line, in col.
func runLabel(gtx layout.Context, txt string, f font.Font, ty token.TypeInfo, col token.MatColor) layout.Dimensions {
	gtx.Constraints.Min = image.Point{}
	macro := op.Record(gtx.Ops)
	paint.ColorOp{Color: col.AsNRGBA()}.Add(gtx.Ops)
	color := macro.Stop()
	l := widget.Label{MaxLines: 1, LineHeight: ty.LineHeight}
	return l.Layout(gtx, wdk.GetMaterialTheme(gtx).TextShaper, f, ty.Size, txt, color)
}

// previewEmojiPx is the side of a custom emoji in a last message: a little
// over the size of its letters.
func previewEmojiPx(gtx layout.Context) int {
	return gtx.Sp(wdk.GetMaterialTheme(gtx).Typescale[token.TypestyleBodyMedium].Size) * 6 / 5
}

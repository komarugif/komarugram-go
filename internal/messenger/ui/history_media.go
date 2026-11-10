// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"errors"
	"image"
	"image/color"
	"math"
	"strings"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/mp4"

	"gio-mw/token"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"
)

// fitMediaSize reserves exactly the same aspect ratio before and after loading.
func fitMediaSize(w, h, maxW, maxH int) image.Point {
	if w <= 0 || h <= 0 {
		w, h = 4, 3
	}
	maxW = max(1, maxW)
	maxH = max(1, maxH)
	width := maxW
	height := max(1, int(int64(width)*int64(h)/int64(w)))
	if height > maxH {
		height = maxH
		width = max(1, int(int64(height)*int64(w)/int64(h)))
	}
	return image.Pt(width, height)
}
func (p *chatPage) mediaLayout(gtx layout.Context, r *messageRow, m model.Message, l localization.Catalog, animate bool) layout.Dimensions {
	if m.Kind == model.MessageRound && m.Media != nil {
		return p.roundLayout(gtx, r, m, l, animate)
	}
	if m.Kind != model.MessagePhoto && m.Kind != model.MessageVideo && m.Kind != model.MessageGIF && m.Kind != model.MessageSticker {
		return p.fileLayout(gtx, r, m, l)
	}
	size := mediaBox(gtx.Metric, m.Kind, m.Media.Width, m.Media.Height, gtx.Constraints.Max.X)
	return p.mediaTile(gtx, r, m, size, false, l, animate)
}

// mediaBox is how large a photo, a video, a GIF or a sticker w by h is
// drawn within maxX.
func mediaBox(m unit.Metric, kind model.MessageKind, w, h, maxX int) image.Point {
	// As high as Telegram Desktop's maxMediaSize lets a tall one be: at
	// 360 dp a video shot upright took half the width of the bubble.
	maxW, maxH := min(maxX, m.Dp(420)), m.Dp(mediaMaxHeight)
	if kind == model.MessageSticker {
		maxW, maxH = min(maxW, m.Dp(stickerMax)), m.Dp(stickerMax)
	}
	return fitMediaSize(w, h, maxW, maxH)
}

// tileAnimates tells whether a tile of kind plays: as animations do, and,
// with them off, a sticker or a GIF while the pointer is on it, as Telegram
// Desktop's do with autoplay off.
func tileAnimates(kind model.MessageKind, animate, hovered bool) bool {
	switch kind {
	case model.MessageSticker, model.MessageGIF:
		return animate || hovered
	}
	return animate
}

func mediaErrorText(err error) string {
	if err == nil {
		return ""
	}
	s := strings.Join(strings.Fields(err.Error()), " ")
	if len([]rune(s)) > 220 {
		s = string([]rune(s)[:220]) + "…"
	}
	return s
}
func (p *chatPage) mediaTile(gtx layout.Context, r *messageRow, m model.Message, size image.Point, crop bool, l localization.Catalog, animate bool) layout.Dimensions {
	end := p.trace.Begin("history.media")
	defer end()
	stickerSet := m.Kind == model.MessageSticker && m.Media.StickerSet != nil
	clicked := r.media.Clicked(gtx)
	if stickerSet {
		clicked = r.sticker.Clicked(gtx)
	}
	animate = tileAnimates(m.Kind, animate, r.media.Hovered() || r.sticker.click.Hovered())
	target := m
	// A GIF too large to be read whole plays as a video does, streamed in
	// the player.
	gif := m.Kind == model.MessageGIF && !m.Media.Whole()
	video := m.Kind == model.MessageVideo || gif
	// One that plays here pauses on a click, and opens in the viewer on
	// one of the middle button.
	inline := m.Kind == model.MessageGIF && !gif
	if inline && r.gif.update(gtx, p, m) && p.openGIF != nil {
		p.openGIF(m)
	}
	if video {
		target.Kind = model.MessagePhoto
		target.Media = m.Media.Thumbnail
	} else if m.Kind == model.MessagePhoto {
		target = r.tileMedia(m, size)
	}
	var frame, preview image.Image
	var err error
	loading, cancelled := false, false
	// progress is below 0 while the size is not known.
	progress := float32(-1)
	if target.Media != nil {
		fit := p.media.StatusFit
		if m.Kind == model.MessageSticker {
			// As Telegram Desktop plays stickers in messages: up to 60
			// pictures a second.
			fit = p.media.StatusFullRate
		}
		status := fit(target, animate, size, crop)
		frame, preview, err = status.Frame, status.Preview, status.Err
		loading, cancelled = status.Loading, status.Cancelled
		if status.Total > 0 {
			progress = min(1, float32(status.Downloaded)/float32(status.Total))
		}
	}
	// A file on its way draws the ring of a download, of how far it went up,
	// whatever reads its picture meanwhile; a click cancels it.
	upload, uploading := p.uploading(gtx, m)
	if uploading {
		err, loading, cancelled, progress = nil, true, false, uploadProgress(upload)
		if clicked {
			p.cancelUpload(m)
			clicked = false
		}
	}
	// A GIF the sandbox cannot play, not in H.264, is no failure to tell:
	// a click asks where else to play it (gifUnplayable).
	unplayable := inline && errors.Is(err, mp4.ErrUnsupported)
	if err != nil && err != r.mediaTold && !cancelled && !unplayable {
		p.toast.Show(mediaErrorText(err))
	}
	r.mediaTold = err
	if clicked {
		if m.StoryOf != nil && p.openStory != nil && err == nil && !cancelled {
			// The stories of the history go through the viewer apart.
			p.openStory(m)
		} else if video {
			p.playVideo(gtx, m, -1, l)
		} else if m.Kind == model.MessagePhoto && r.alone && p.openAlone != nil {
			p.openAlone(m)
		} else if m.Kind == model.MessagePhoto && p.openPhoto != nil {
			p.openPhoto(m)
		} else if unplayable {
			p.gifUnplayable(m, l)
		} else if inline && err == nil && !cancelled && !loading {
			p.media.ToggleGIF(m)
			gtx.Execute(op.InvalidateCmd{})
		} else if err != nil || cancelled {
			p.media.Retry(target)
		} else if stickerSet {
			p.stickers.open(p, *m.Media.StickerSet)
		} else if loading {
			p.media.Cancel(target)
		}
	}
	draw := func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(size)
		// The time of the message, where its bubble asked for it on the
		// tile and the tile has room for it; the tile then ends the bubble,
		// and its bottom corners are round as the bubble's.
		inset := gtx.Dp(8)
		t := &r.time
		t.drawn = timeFits(*t, size, inset)
		corners := r.corners
		if t.size.X > 0 && !t.drawn {
			corners.se, corners.sw = 0, 0
		}
		shape := corners.rrect(gtx, size)
		defer shape.Push(gtx.Ops).Pop()
		// A sticker lies on the chat's background once it is drawn; the
		// plate only shows where it is going to be.
		if m.Kind != model.MessageSticker || (frame == nil && preview == nil) {
			paint.FillShape(gtx.Ops, scheme(gtx).SurfaceContainerHigh.AsNRGBA(), shape.Op(gtx.Ops))
		}
		im := frame
		if im == nil {
			im = preview
		}
		if im != nil {
			fit := tileFit(im.Bounds().Size(), size, crop || frame == nil)
			widget.Image{Src: p.images.Op(im), Fit: fit, Position: layout.Center}.Layout(gtx)
		}
		if video || loading || cancelled || err != nil {
			diameter := min(gtx.Dp(48), min(size.X, size.Y))
			origin := image.Pt((size.X-diameter)/2, (size.Y-diameter)/2)
			offset(gtx, origin, func(gtx layout.Context) layout.Dimensions {
				paint.FillShape(gtx.Ops, color.NRGBA{A: 145}, clip.Ellipse{Max: image.Pt(diameter, diameter)}.Op(gtx.Ops))
				// White on the dark circle in either theme.
				white := token.NewMatColorFromHexRGB(0xffffff)
				if (video || unplayable) && !uploading {
					// An icon, not "▶": the text's fonts lack it, and the
					// emoji font drew it as the colored emoji.
					inset := diameter / 6
					offset(gtx, image.Pt(inset, inset), func(gtx layout.Context) layout.Dimensions {
						return exact(gtx, image.Pt(diameter-2*inset, diameter-2*inset), func(gtx layout.Context) layout.Dimensions {
							return iconPlayFile(gtx, white)
						})
					})
					return layout.Dimensions{Size: image.Pt(diameter, diameter)}
				}
				// A click cancels it, or loads it again; the toast told
				// why it failed.
				mark := iconClear
				switch {
				case cancelled:
					mark = iconDownload
				case err != nil:
					mark = iconRefresh
				}
				return r.ring.layout(gtx, diameter, loading, progress, mark, white, animate)
			})
		}
		// How far a file on its way went, under its ring, once its size is
		// known; bottom is where that plate ends.
		bottom := 0
		if text := uploadPlateText(upload); uploading && text != "" {
			macro := op.Record(gtx.Ops)
			pgtx := gtx
			pgtx.Constraints = layout.Constraints{Max: size}
			dims := mediaPlate(pgtx, text)
			call := macro.Stop()
			at := uploadPlateAt(size, dims.Size, min(gtx.Dp(48), min(size.X, size.Y)), gtx.Dp(4))
			offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
				call.Add(gtx.Ops)
				return dims
			})
			bottom = at.Y + dims.Size.Y
		}
		reserve := 0
		if t.drawn {
			reserve = t.size.X
		}
		lift := 0
		if inline && !loading && !cancelled && err == nil {
			paused := p.media.GIFPaused(m)
			lift = r.gif.layout(gtx, size, paused, r.media.Hovered(), p.media.GIFPosition(m), m.Media.Duration, reserve)
		}
		if t.drawn {
			// It fades out while the pointer is over the tile, for the
			// picture to be seen whole, as a video's plate does; on a GIF
			// that plays here it fades in, as the GIF's time does.
			target := boolFloat(!r.media.Hovered())
			if inline {
				target = boolFloat(r.media.Hovered() || r.gif.seeking)
			}
			r.timeFade.Duration = token.DurationShort2
			if shown := r.timeFade.Animate(gtx, target); shown > 0 {
				func() {
					defer paint.PushOpacity(gtx.Ops, shown).Pop()
					offset(gtx, image.Pt(size.X-inset-t.size.X, size.Y-inset-lift-t.size.Y), func(gtx layout.Context) layout.Dimensions {
						t.call.Add(gtx.Ops)
						return layout.Dimensions{Size: t.size}
					})
				}()
			}
		}
		// The plate of a video fades out while the pointer is over it, for
		// the picture to be seen whole, and back in when it leaves.
		shown := float32(0)
		if video {
			shown = 1
			if r.media.Hovered() {
				shown = 0
			}
			r.plate.Duration = token.DurationShort2
			shown = r.plate.Animate(gtx, shown)
		}
		if shown > 0 {
			defer paint.PushOpacity(gtx.Ops, shown).Pop()
			// In the corner, as far from the bottom as from the left, on
			// the plate of a GIF's time.
			macro := op.Record(gtx.Ops)
			pgtx := gtx
			pgtx.Constraints.Min = image.Point{}
			dims := mediaPlate(pgtx, videoPlateText(m, gif, p.videoTimeOnly, l))
			call := macro.Stop()
			top := max(0, size.Y-inset-dims.Size.Y)
			if reserve > 0 && 2*inset+dims.Size.X+gtx.Dp(mediaPlateGap)+reserve > size.X {
				// Beside the message's time it does not go: in the top
				// corner, as Telegram for Android puts a video's.
				top = inset
			} else if top < bottom {
				// In a tile too low for both, how far it went is what
				// matters.
				top = -1
			}
			if top >= 0 {
				offset(gtx, image.Pt(inset, top), func(gtx layout.Context) layout.Dimensions {
					call.Add(gtx.Ops)
					return dims
				})
			}
		}
		return layout.Dimensions{Size: size}
	}
	if stickerSet {
		sc := scheme(gtx)
		style := surfaceStyle{radius: gtx.Dp(8), background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: l.T("stickers.open")}
		return r.sticker.Layout(gtx, size, style, draw)
	}
	return r.media.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		// A press opens or plays it, as a button.
		pointer.CursorPointer.Add(gtx.Ops)
		return draw(gtx)
	})
}

// videoPlateText is what the plate of m, a video or a GIF played as one,
// tells: the time and the word, or the time alone where all are videos.
func videoPlateText(m model.Message, gif, timeOnly bool, l localization.Catalog) string {
	if gif {
		return l.T("history.gif")
	}
	text := playTime(m.Media.Duration)
	if !timeOnly {
		text += " · " + l.T("history.video")
	}
	return text
}

// mediaPlateGap is the least space between two plates side by side at the
// bottom of a tile.
const mediaPlateGap unit.Dp = 6

// mediaPlate draws text on the plate of a tile's corner, small and
// translucent over the picture, as a GIF's time and a video message's are:
// a video's time and a GIF's mark too.
func mediaPlate(gtx layout.Context, text string) layout.Dimensions {
	sc := scheme(gtx)
	gtx.Constraints.Min = image.Point{}
	macro := op.Record(gtx.Ops)
	plate := layout.Inset{Top: 2, Bottom: 2, Left: 7, Right: 7}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return label(gtx, text, token.TypestyleLabelMedium, sc.InverseSurface.OnColor, 1)
	})
	call := macro.Stop()
	fillRounded(gtx, sc.InverseSurface.Color.SetOpacity(0.55), plate.Size, plate.Size.Y/2)
	call.Add(gtx.Ops)
	return plate
}

// tileMedia returns the message showing the smallest variant of a photo
// that covers a tile of size pixels. It is kept per row, so drawing a tile
// does not allocate; the blurred preview is the original's.
func (r *messageRow) tileMedia(m model.Message, size image.Point) model.Message {
	if r.tile.Media == nil || r.tileSize != size || r.tile.Key != m.Key {
		variant := *m.Media.Variant(size.X, size.Y)
		if variant.Preview == nil {
			variant.Preview = m.Media.Preview
		}
		r.tile, r.tileSize = m.WithMedia(&variant), size
	}
	return r.tile
}
func ring(gtx layout.Context, diameter int, progress float32, animate bool) {
	start := -math.Pi / 2
	if progress <= 0 {
		progress = .28
		if animate {
			start += float64(gtx.Now.UnixMilli()%1400) * 2 * math.Pi / 1400
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(time.Second / 30)})
		}
	}
	center := float32(diameter) / 2
	radius := center - float32(gtx.Dp(3))
	var path clip.Path
	path.Begin(gtx.Ops)
	steps := max(2, int(progress*64))
	for i := 0; i <= steps; i++ {
		angle := start + float64(progress)*2*math.Pi*float64(i)/float64(steps)
		point := f32.Pt(center+radius*float32(math.Cos(angle)), center+radius*float32(math.Sin(angle)))
		if i == 0 {
			path.MoveTo(point)
		} else {
			path.LineTo(point)
		}
	}
	paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 240}, clip.Stroke{Path: path.End(), Width: float32(gtx.Dp(2))}.Op())
}

// mediaMaxHeight is the highest a photo or a video in the history is.
const mediaMaxHeight unit.Dp = 430

// albumGap is the space between the tiles of an album.
const albumGap unit.Dp = 3

// tileRadius is how round a tile of media is.
const tileRadius unit.Dp = 8

// tileCorners are the radii of the corners of a tile at the top or the
// bottom of its bubble, round as the bubble's corners there, less the space
// between: zero for one of its own, tileRadius.
type tileCorners struct{ nw, ne, se, sw int }

// rrect is the shape of a tile of size with the corners.
func (c tileCorners) rrect(gtx layout.Context, size image.Point) clip.RRect {
	radius := gtx.Dp(tileRadius)
	pick := func(r int) int {
		if r > 0 {
			return r
		}
		return radius
	}
	return clip.RRect{Rect: image.Rectangle{Max: size}, NW: pick(c.nw), NE: pick(c.ne), SE: pick(c.se), SW: pick(c.sw)}
}

// timeFits tells whether the plate t goes in the corner of a tile of size,
// inset from its edges.
func timeFits(t timePlate, size image.Point, inset int) bool {
	return t.size.X > 0 && t.size.X+2*inset <= size.X && t.size.Y+2*inset <= size.Y
}

func albumRow(gtx layout.Context, r *messageRow, m model.Message, l localization.Catalog) *messageRow {
	if r.album == nil {
		r.album = map[model.MessageID]*messageRow{}
	}
	child := r.album[m.Key.MessageID]
	if child == nil || child.revision != m.ContentRevision {
		child = newMessageRow(m, l, gtx.Now)
		r.album[m.Key.MessageID] = child
	}
	child.refreshDates(gtx, m, l)
	return child
}

// tiledAlbum tells whether the album of attachments is laid out as a grid
// of tiles: of photos and videos. One of files or music is a list of them,
// as Telegram Desktop's: a tile shows a file's own picture, read whole.
func tiledAlbum(attachments []model.Message) bool {
	for _, m := range attachments {
		switch m.Kind {
		case model.MessagePhoto, model.MessageVideo, model.MessageGIF:
		default:
			return false
		}
	}
	return true
}

func (p *chatPage) albumLayout(gtx layout.Context, r *messageRow, m model.Message, l localization.Catalog, animate bool) layout.Dimensions {
	if !tiledAlbum(m.Attachments) {
		children := make([]layout.FlexChild, 0, 2*len(m.Attachments))
		for i, member := range m.Attachments {
			if i > 0 {
				children = append(children, vspace(8))
			}
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				child := albumRow(gtx, r, member, l)
				p.partPresses(gtx, m, child, member)
				dims := p.mediaLayout(gtx, child, member, l, animate)
				partPressArea(gtx, child, dims.Size)
				return dims
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	}
	// The tiles are placed first: whether the last carries the message's
	// time tells the bottom row its corners.
	sizes := r.albumSizes[:0]
	for _, member := range m.Attachments {
		var size image.Point
		if meta := member.Media; meta != nil {
			size = image.Pt(meta.Width, meta.Height)
		}
		sizes = append(sizes, size)
	}
	r.albumSizes = sizes
	tiles, album := albumTiles(gtx.Metric, gtx.Constraints.Max.X, sizes, r.tiles[:0])
	defer func() { r.tiles = tiles[:0] }()
	width, height := album.X, album.Y
	// The last tile, at the album's bottom right, carries the message's
	// time, as a single one would; the album then ends the bubble.
	last := len(tiles) - 1
	timed := last >= 0 && timeFits(r.time, tiles[last].Size(), gtx.Dp(8))
	r.time.drawn = timed
	for k, tile := range tiles {
		member := m.Attachments[k]
		// The tiles at the album's corners round them as the album is
		// rounded.
		var corners tileCorners
		if tile.Min.Y == 0 && tile.Min.X == 0 {
			corners.nw = r.corners.nw
		}
		if tile.Min.Y == 0 && tile.Max.X == width {
			corners.ne = r.corners.ne
		}
		if timed && tile.Max.Y == height && tile.Min.X == 0 {
			corners.sw = r.corners.sw
		}
		if timed && tile.Max.Y == height && tile.Max.X == width {
			corners.se = r.corners.se
		}
		tileGtx := gtx
		tileGtx.Constraints = layout.Exact(tile.Size())
		offset(tileGtx, tile.Min, func(gtx layout.Context) layout.Dimensions {
			child := albumRow(gtx, r, member, l)
			child.corners = corners
			child.time = timePlate{}
			if k == last {
				child.time = r.time
			}
			p.partPresses(gtx, m, child, member)
			dims := p.mediaTile(gtx, child, member, tile.Size(), true, l, animate)
			partPressArea(gtx, child, tile.Size())
			return dims
		})
	}
	return layout.Dimensions{Size: image.Pt(width, height)}
}

// albumTiles places the tiles of a tiled album of pictures of sizes (0 by
// 0 where one is not known) within maxX, into tiles: in rows of two, the
// first alone where they are odd, each row as high as the aspects of its
// pictures make it across, 90 to 300 dp. size is the album's.
func albumTiles(m unit.Metric, maxX int, sizes []image.Point, tiles []image.Rectangle) (_ []image.Rectangle, size image.Point) {
	width := min(maxX, m.Dp(500))
	gap := m.Dp(albumGap)
	y := 0
	for i := 0; i < len(sizes); {
		count := 2
		if len(sizes)%2 == 1 && i == 0 {
			count = 1
		}
		count = min(count, len(sizes)-i)
		var aspects [2]float64
		sum := float64(0)
		for j := 0; j < count; j++ {
			a := 1.
			if s := sizes[i+j]; s.X > 0 && s.Y > 0 {
				a = float64(s.X) / float64(s.Y)
			}
			a = max(.6, min(a, 1.8))
			aspects[j] = a
			sum += a
		}
		height := min(m.Dp(300), max(m.Dp(90), int(float64(width-gap*(count-1))/sum)))
		x := 0
		for j := 0; j < count; j++ {
			w := int(float64(width-gap*(count-1)) * aspects[j] / sum)
			if j == count-1 {
				w = width - x
			}
			tiles = append(tiles, image.Rect(x, y, x+w, y+height))
			x += w + gap
		}
		y += height + gap
		i += count
	}
	return tiles, image.Pt(width, max(0, y-gap))
}

// tileProportion is how far, as a share, a picture's proportions may be
// from its tile's for it to fill the tile: a thumbnail of the media's are
// rounded to its few pixels, and laid in the middle, it left strips of the
// plate at the sides.
const tileProportion = 0.08

// tileFit is how a picture of size pic is drawn in a tile size large: to
// fill it, where crop asks, or the picture is a blurred preview, or of
// about the tile's proportions; in its middle, whole, where they are other.
func tileFit(pic, size image.Point, crop bool) widget.Fit {
	if crop || pic.X <= 0 || pic.Y <= 0 || size.X <= 0 || size.Y <= 0 {
		return widget.Cover
	}
	a := float64(pic.X) / float64(pic.Y)
	b := float64(size.X) / float64(size.Y)
	if a/b-1 <= tileProportion && b/a-1 <= tileProportion {
		return widget.Cover
	}
	return widget.Contain
}

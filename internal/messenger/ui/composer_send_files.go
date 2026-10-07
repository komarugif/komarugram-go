// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"gio-mw/token"
	"gio-mw/widget/checkbox"
	"gio-mw/widget/scroll"

	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/sendfiles"
	"komarugram/pkg/video"
)

// filesBox is the box for sending files, as Telegram Desktop's: what was
// chosen, shown as it will go, a caption, and how to send it: photos as
// photos or as documents, in albums or one by one.
type filesBox struct {
	modal modal
	files []*boxFile
	way   sendfiles.Way
	// caption is the text of the message the files go with.
	caption widget.Editor
	list    scroll.List
	// focusCaption gives the caption the keyboard on the next frame.
	focusCaption              bool
	add, cancel, send         surface
	group, documents, quality *checkbox.Checkboxes[string]
	// results bring what the reading of files found out; generation tells
	// which box opening they are for.
	results    chan boxResult
	generation int
}

// boxFile is a file in the box.
type boxFile struct {
	sendfiles.File
	// thumb is the picture the file shows, nil for a file without one or
	// until it is read; ready is set once the file has been looked at.
	thumb           *image.RGBA
	ready           bool
	restrictionTold bool
	remove          surface
}

// boxResult is what looking at a file found.
type boxResult struct {
	generation int
	file       *boxFile
	info       sendfiles.File
	thumb      *image.RGBA
	err        error
}

// Sizes of the box, in dp.
const (
	filesBoxWidth   = 440
	filesBoxPreview = 300
	// boxThumbSide is how large the pictures of the previews are read.
	boxThumbSide = 640
	// boxMaxFiles bounds what one box takes, as a message list does.
	boxMaxFiles = 100
)

func newFilesBox() filesBox {
	b := filesBox{results: make(chan boxResult, 32)}
	b.caption.Submit = true
	b.group = checkbox.NewCheckboxes([]string{"group"}, nil, func(values []string) { b.way.Group = len(values) == 1 })
	b.documents = checkbox.NewCheckboxes([]string{"documents"}, nil, func(values []string) { b.way.Documents = len(values) == 1 })
	b.quality = checkbox.NewCheckboxes([]string{"quality"}, nil, func(values []string) { b.way.HighQuality = len(values) == 1 })
	b.list.Axis = layout.Vertical
	return b
}

// Shown reports whether the box is on screen.
func (b *filesBox) Shown() bool { return b.modal.Shown() }

// addPaths puts the files at paths in the box, opening it if it is not
// open. It starts with the composer's text as the caption; documents starts
// it with the files to go as documents, as the "File" of the attachment menu
// does.
func (b *filesBox) addPaths(c *messageComposer, paths []string, documents bool) {
	if !b.modal.Shown() || b.modal.closing {
		b.generation++
		b.files = nil
		b.way = sendfiles.Way{Group: true, Documents: documents}
		b.caption.SetText(c.draft(c.chat).editor.Text())
		b.group.SetValues([]string{"group"})
		b.documents.SetValues(nil)
		if documents {
			b.documents.SetValues([]string{"documents"})
		}
		b.quality.SetValues(nil)
		b.modal.Open()
		b.focusCaption = true
	}
	var added []*boxFile
	for _, path := range paths {
		if len(b.files)+len(added) >= boxMaxFiles {
			break
		}
		added = append(added, &boxFile{File: sendfiles.File{Path: path, Name: baseName(path)}})
	}
	b.files = append(b.files, added...)
	ctx, generation, ffmpeg := c.ctx, b.generation, video.ResolveFFmpeg(c.ffmpegPath())
	results, invalidate := b.results, c.invalidate
	go func() {
		for _, f := range added {
			res := boxResult{generation: generation, file: f}
			res.info, res.err = sendfiles.Inspect(f.Path)
			if res.err == nil {
				res.thumb = boxThumbnail(ctx, ffmpeg, res.info)
			}
			select {
			case results <- res:
				invalidate()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// baseName is the last element of a path of any system.
func baseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// boxThumbnail reads the picture a file shows in the box: a photo or a GIF
// as it is, a video as its first frame when there is an FFmpeg to say it,
// music as its cover.
func boxThumbnail(ctx context.Context, ffmpeg string, f sendfiles.File) *image.RGBA {
	switch f.Kind {
	case sendfiles.KindMusic:
		if thumb, err := sendfiles.CoverThumbnail(f.Cover, boxThumbSide); err == nil {
			return thumb
		}
	case sendfiles.KindPhoto, sendfiles.KindAnimation:
		if thumb, err := sendfiles.Thumbnail(f.Path, boxThumbSide); err == nil {
			return thumb
		}
	case sendfiles.KindVideo:
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if thumb, err := sendfiles.VideoThumbnail(ctx, ffmpeg, f.Path, boxThumbSide); err == nil {
			return thumb
		}
	}
	return nil
}

// close takes the box away at once and forgets its files.
func (b *filesBox) close() {
	b.generation++
	b.files = nil
	b.modal.Hide()
}

// take reads what looking at files found.
func (b *filesBox) take(l localization.Catalog) {
	for {
		select {
		case r := <-b.results:
			if r.generation != b.generation || !slices.Contains(b.files, r.file) {
				continue
			}
			if r.err != nil {
				b.files = slices.DeleteFunc(b.files, func(f *boxFile) bool { return f == r.file })
				b.modal.Toast(problemOf(r.err, r.file.Name, l))
				if len(b.files) == 0 {
					b.modal.Close()
				}
				continue
			}
			r.file.File, r.file.thumb, r.file.ready = r.info, r.thumb, true
		default:
			return
		}
	}
}

// problemOf tells why a file cannot be sent.
func problemOf(err error, name string, l localization.Catalog) string {
	if errors.Is(err, sendfiles.ErrEmpty) {
		return l.Format("files.empty", map[string]string{"name": name})
	}
	return l.T("files.invalid")
}

// ready reports whether every file has been looked at.
func (b *filesBox) ready() bool {
	for _, f := range b.files {
		if !f.ready {
			return false
		}
	}
	return len(b.files) > 0
}

// captionLimit is how long a caption may be: more with Premium.
func (c *messageComposer) captionLimit() int {
	if source, ok := c.source.(model.PremiumSource); ok {
		if limit := source.Premium().Limit("caption_length_limit"); limit > 0 {
			return limit
		}
	}
	return 1024
}

// captionLength is a text's length as Telegram counts it.
func captionLength(text string) int { return len(utf16.Encode([]rune(text))) }

// sendFiles sends what the box holds, once it can.
func (c *messageComposer) sendFiles(l localization.Catalog) {
	b := &c.files
	if !b.ready() || b.modal.closing {
		return
	}
	caption := strings.TrimSpace(b.caption.Text())
	if over := captionLength(caption) - c.captionLimit(); over > 0 {
		b.modal.Toast(l.Count("files.caption_limit", over, nil))
		return
	}
	paths := make([]string, len(b.files))
	for i, f := range b.files {
		paths[i] = f.Path
	}
	way := b.way
	for _, f := range b.files {
		if err := c.permissions(c.chat).Check(sendfiles.Permission(f.File, way.Documents)); err != nil {
			b.modal.Toast(composerErrorText(err, l))
			return
		}
	}
	c.submit(c.chat, model.OutgoingMessage{Text: caption, Files: &model.OutgoingFiles{Paths: paths, Documents: way.Documents, Group: way.Group, HighQuality: way.HighQuality}})
	for _, path := range paths {
		if c.pasted[path] {
			c.uploading[path] = true
		}
	}
	b.modal.Close()
}

// layoutFilesBox draws the box over the page, and does what its buttons ask.
func (c *messageComposer) layoutFilesBox(gtx layout.Context, p *chatPage, l localization.Catalog) {
	b := &c.files
	b.take(l)
	for _, f := range b.files {
		if f.ready {
			if err := c.permissions(c.chat).Check(sendfiles.Permission(f.File, b.way.Documents)); err != nil {
				if !f.restrictionTold {
					b.modal.Toast(composerErrorText(err, l))
					f.restrictionTold = true
				}
			} else {
				f.restrictionTold = false
			}
		}
	}
	if !b.modal.Shown() {
		if len(c.pasted) > len(c.uploading) {
			c.forgetPasted(nil)
		}
		return
	}
	// The checkboxes follow the way; a click on one changes it.
	b.group.Update(gtx)
	b.documents.Update(gtx)
	b.quality.Update(gtx)
	if !b.modal.closing {
		for {
			ev, ok := b.caption.Update(gtx)
			if !ok {
				break
			}
			if _, ok := ev.(widget.SubmitEvent); ok {
				c.sendFiles(l)
			}
		}
		if b.cancel.Clicked(gtx) {
			b.modal.Close()
		}
		if b.send.Clicked(gtx) {
			c.sendFiles(l)
		}
		if b.add.Clicked(gtx) {
			c.chooseMore()
		}
		for i, f := range b.files {
			if f.remove.Clicked(gtx) {
				b.files = slices.Delete(b.files, i, i+1)
				if len(b.files) == 0 {
					b.modal.Close()
				}
				break
			}
		}
	}
	if b.focusCaption {
		b.focusCaption = false
		gtx.Execute(key.FocusCmd{Tag: &b.caption})
	}
	shown := b.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		width := min(gtx.Constraints.Max.X-2*gtx.Dp(16), gtx.Dp(filesBoxWidth))
		gtx.Constraints.Min.X, gtx.Constraints.Max.X = width, width
		return b.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions { return c.layoutFilesContent(gtx, p, l) }, defaultCardPadding)
	})
	if !shown {
		b.close()
	}
}

// chooseAttachments asks for the files of the attachment menu's item form,
// 1 for photos and videos and 2 for files.
func (c *messageComposer) chooseAttachments(form int) {
	if c.choosing {
		return
	}
	c.choosing = true
	ctx, chat, choose := c.ctx, c.chat, c.chooser
	go func() {
		var filter *fileFilter
		if form == 1 {
			filter = &mediaFilter
		}
		f := choose(ctx, filter, true)
		f.chat, f.form, f.menu = chat, form, true
		select {
		case c.fileResults <- f:
			c.invalidate()
		case <-ctx.Done():
		}
	}()
}

// chooseMore asks for more files to send.
func (c *messageComposer) chooseMore() {
	if c.choosing {
		return
	}
	c.choosing = true
	ctx, chat, choose := c.ctx, c.chat, c.chooser
	go func() {
		f := choose(ctx, nil, true)
		f.chat, f.menu = chat, true
		select {
		case c.fileResults <- f:
			c.invalidate()
		case <-ctx.Done():
		}
	}()
}

// title says what is being sent.
func (b *filesBox) title(l localization.Catalog) string {
	files := make([]sendfiles.File, len(b.files))
	for i, f := range b.files {
		files[i] = f.File
	}
	switch sendfiles.TitleOf(files, b.way) {
	case sendfiles.TitleImage:
		return l.T("files.image")
	case sendfiles.TitleVideo:
		return l.T("files.video")
	case sendfiles.TitleImages:
		return l.Count("files.images_selected", len(files), nil)
	case sendfiles.TitleFiles:
		return l.Count("files.files_selected", len(files), nil)
	}
	return l.T("files.file")
}

func (c *messageComposer) layoutFilesContent(gtx layout.Context, p *chatPage, l localization.Catalog) layout.Dimensions {
	b := &c.files
	sc := scheme(gtx)
	all := make([]sendfiles.File, len(b.files))
	for i, f := range b.files {
		all[i] = f.File
	}
	limit := c.captionLimit()
	length := captionLength(strings.TrimSpace(b.caption.Text()))
	options := []layout.FlexChild{}
	option := func(box *checkbox.Checkboxes[string], key, text string) {
		options = append(options, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return box.Layout(gtx, map[string]string{key: text})
		}))
	}
	if sendfiles.HasGroupOption(all) {
		option(b.group, "group", l.T("files.group"))
	}
	if sendfiles.HasDocumentsOption(all) {
		text := l.T("files.documents")
		if len(all) == 1 {
			text = l.T("files.document")
		}
		option(b.documents, "documents", text)
	}
	if sendfiles.HasHighQualityOption(all, b.way) {
		option(b.quality, "quality", l.T("files.quality"))
	}
	preview := min(gtx.Dp(filesBoxPreview), gtx.Constraints.Max.Y/2)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, b.title(l), token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
		}),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.Y = preview
			gtx.Constraints.Min = image.Point{X: gtx.Constraints.Max.X}
			return c.layoutFilesPreview(gtx, p, l)
		}),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Stack{}.Layout(gtx,
				layout.Expanded(func(gtx layout.Context) layout.Dimensions {
					fillRounded(gtx, sc.SurfaceContainerHigh, gtx.Constraints.Min, gtx.Dp(12))
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Stacked(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Inset{Top: 10, Bottom: 10, Left: 14, Right: 14}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Max.Y = gtx.Dp(112)
						return flatEditor(gtx, &b.caption, l.T("files.caption"))
					})
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			// The count of what is left of the caption shows as it runs out.
			if length < limit*4/5 {
				return layout.Dimensions{}
			}
			color := sc.SurfaceVariant.OnColor
			if length > limit {
				color = sc.Error.Color
			}
			return layout.Inset{Top: 4, Right: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return label(gtx, strconv.Itoa(limit-length), token.TypestyleLabelMedium, color, 1)
				})
			})
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if len(options) == 0 {
				return layout.Dimensions{}
			}
			return layout.Inset{Top: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx, options...)
			})
		}),
		vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			sendGtx := gtx
			if !b.ready() {
				sendGtx = gtx.Disabled()
			}
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			return layout.Flex{Alignment: layout.Middle, Spacing: layout.SpaceBetween}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &b.add, l.T("files.add")) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &b.cancel, l.T("history.cancel")) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							sendGtx.Constraints = gtx.Constraints
							return textButton(sendGtx, &b.send, l.T("composer.send"))
						}),
					)
				}),
			)
		}),
	)
}

// isMediaTile reports whether f is shown as a picture in the grid of the
// box: media sent as media.
func (b *filesBox) isMediaTile(f *boxFile) bool {
	if b.way.Documents {
		return false
	}
	switch f.Kind {
	case sendfiles.KindPhoto, sendfiles.KindVideo, sendfiles.KindAnimation:
		return f.ready
	}
	return false
}

// layoutFilesPreview draws the files: media as a grid of pictures, the rest
// as rows, in a list that scrolls when it is taller than the box allows.
func (c *messageComposer) layoutFilesPreview(gtx layout.Context, p *chatPage, l localization.Catalog) layout.Dimensions {
	b := &c.files
	var tiles, rows []*boxFile
	for _, f := range b.files {
		if b.isMediaTile(f) {
			tiles = append(tiles, f)
		} else {
			rows = append(rows, f)
		}
	}
	return b.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		var children []layout.FlexChild
		if len(tiles) > 0 {
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return c.layoutTiles(gtx, p, tiles, l)
			}))
		}
		for i, f := range rows {
			if i > 0 || len(tiles) > 0 {
				children = append(children, vspace(4))
			}
			children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return c.layoutFileRow(gtx, p, f, l)
			}))
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
	})
}

// tileColumns is how many pictures a row of the grid holds.
func tileColumns(n int) int {
	switch {
	case n <= 3:
		return max(n, 1)
	case n == 4:
		return 2
	}
	return 3
}

// layoutTiles draws pictures side by side, in squares, as the messages'
// albums do; one picture alone keeps its shape.
func (c *messageComposer) layoutTiles(gtx layout.Context, p *chatPage, tiles []*boxFile, l localization.Catalog) layout.Dimensions {
	width := gtx.Constraints.Max.X
	gap := gtx.Dp(4)
	if len(tiles) == 1 {
		f := tiles[0]
		w, h := f.Width, f.Height
		if f.thumb != nil {
			w, h = f.thumb.Bounds().Dx(), f.thumb.Bounds().Dy()
		}
		if w <= 0 || h <= 0 {
			w, h = 4, 3
		}
		size := image.Pt(width, width*h/w)
		if limit := gtx.Dp(filesBoxPreview); size.Y > limit {
			size = image.Pt(limit*w/h, limit)
		}
		x := (width - size.X) / 2
		offset(gtx, image.Pt(x, 0), func(gtx layout.Context) layout.Dimensions { return c.layoutTile(gtx, p, f, size, l) })
		return layout.Dimensions{Size: image.Pt(width, size.Y)}
	}
	columns := tileColumns(len(tiles))
	cell := (width - gap*(columns-1)) / columns
	rows := (len(tiles) + columns - 1) / columns
	for i, f := range tiles {
		at := image.Pt(i%columns*(cell+gap), i/columns*(cell+gap))
		offset(gtx, at, func(gtx layout.Context) layout.Dimensions { return c.layoutTile(gtx, p, f, image.Pt(cell, cell), l) })
	}
	return layout.Dimensions{Size: image.Pt(width, rows*cell+(rows-1)*gap)}
}

// boxOverlay is the color under the marks drawn over a picture.
var boxOverlay = token.NewMatColorFromHexRGBA(0x000000a6)

// layoutTile draws one picture of the grid with what marks its kind and the
// button that takes it out.
func (c *messageComposer) layoutTile(gtx layout.Context, p *chatPage, f *boxFile, size image.Point, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	radius := gtx.Dp(8)
	defer clip.UniformRRect(image.Rectangle{Max: size}, radius).Push(gtx.Ops).Pop()
	fillRect(gtx, sc.SurfaceContainerHighest, size)
	if f.thumb != nil && p.images != nil {
		gtx.Constraints = layout.Exact(size)
		widget.Image{Src: p.images.Op(f.thumb), Fit: widget.Cover, Position: layout.Center}.Layout(gtx)
	}
	white := token.NewMatColorFromHexRGB(0xffffff)
	switch f.Kind {
	case sendfiles.KindVideo:
		d := gtx.Dp(44)
		offset(gtx, image.Pt((size.X-d)/2, (size.Y-d)/2), func(gtx layout.Context) layout.Dimensions {
			fillRounded(gtx, boxOverlay, image.Pt(d, d), d/2)
			return offset(gtx, image.Pt(d/8, d/8), func(gtx layout.Context) layout.Dimensions {
				return exact(gtx, image.Pt(d*3/4, d*3/4), func(gtx layout.Context) layout.Dimensions { return iconPlayFile(gtx, white) })
			})
		})
	case sendfiles.KindAnimation:
		offset(gtx, image.Pt(gtx.Dp(6), size.Y-gtx.Dp(30)), func(gtx layout.Context) layout.Dimensions {
			return layoutTag(gtx, "GIF")
		})
	}
	d := gtx.Dp(28)
	offset(gtx, image.Pt(size.X-d-gtx.Dp(6), gtx.Dp(6)), func(gtx layout.Context) layout.Dimensions {
		return removeButton(gtx, &f.remove, d, l.T("files.remove"))
	})
	return layout.Dimensions{Size: size}
}

// layoutTag draws a short word on a dark plate, as a mark over a picture.
func layoutTag(gtx layout.Context, text string) layout.Dimensions {
	white := token.NewMatColorFromHexRGB(0xffffff)
	macro := op.Record(gtx.Ops)
	dims := layout.Inset{Top: 2, Bottom: 2, Left: 6, Right: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = image.Point{}
		return label(gtx, text, token.TypestyleLabelSmall, white, 1)
	})
	call := macro.Stop()
	fillRounded(gtx, boxOverlay, dims.Size, dims.Size.Y/2)
	call.Add(gtx.Ops)
	return dims
}

// removeButton is the round button that takes a file out of the box.
func removeButton(gtx layout.Context, s *surface, d int, name string) layout.Dimensions {
	white := token.NewMatColorFromHexRGB(0xffffff)
	size := image.Pt(d, d)
	style := surfaceStyle{radius: d / 2, content: white, background: boxOverlay, button: name}
	return s.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		inner := d * 5 / 9
		return offset(gtx, image.Pt((d-inner)/2, (d-inner)/2), func(gtx layout.Context) layout.Dimensions {
			return exact(gtx, image.Pt(inner, inner), func(gtx layout.Context) layout.Dimensions { return iconClear(gtx, white) })
		})
	})
}

// layoutFileRow draws a file as a row: its picture or icon, its name and
// size, and the button that takes it out.
func (c *messageComposer) layoutFileRow(gtx layout.Context, p *chatPage, f *boxFile, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	side := gtx.Dp(44)
	height := gtx.Dp(56)
	gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, height)
	gtx.Constraints.Max.Y = height
	return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			size := image.Pt(side, side)
			defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(8)).Push(gtx.Ops).Pop()
			if f.thumb != nil && p.images != nil {
				gtx.Constraints = layout.Exact(size)
				return widget.Image{Src: p.images.Op(f.thumb), Fit: widget.Cover, Position: layout.Center}.Layout(gtx)
			}
			fillRect(gtx, sc.Primary.Color, size)
			icon := iconFileRow
			switch f.Kind {
			case sendfiles.KindVideo:
				icon = iconPlayFile
			case sendfiles.KindMusic:
				icon = iconAudiotrack
			}
			inner := side * 5 / 9
			offset(gtx, image.Pt((side-inner)/2, (side-inner)/2), func(gtx layout.Context) layout.Dimensions {
				return exact(gtx, image.Pt(inner, inner), func(gtx layout.Context) layout.Dimensions { return icon(gtx, sc.Primary.OnColor) })
			})
			return layout.Dimensions{Size: size}
		}),
		layout.Rigid(layout.Spacer{Width: 12}.Layout),
		layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = 0
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, songName(f.File), token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					details := l.T("files.loading")
					if f.ready {
						details = sendfiles.Size(f.Size)
					}
					return label(gtx, details, token.TypestyleBodySmall, sc.SurfaceVariant.OnColor, 1)
				}),
			)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			d := gtx.Dp(32)
			return layout.Inset{Left: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return quietRemove(gtx, &f.remove, d, sc.SurfaceVariant.OnColor, l.T("files.remove"))
			})
		}),
	)
}

// songName is how a row names a file, music as Telegram Desktop names it:
// "Performer – Title", its title alone, or its file's name when its tags
// say neither.
func songName(f sendfiles.File) string {
	switch {
	case f.Kind != sendfiles.KindMusic || f.Title == "" && f.Performer == "":
		return f.Name
	case f.Performer == "":
		return f.Title
	case f.Title == "":
		return f.Performer + " – Unknown Track"
	}
	return f.Performer + " – " + f.Title
}

// quietRemove is the button of a row that takes its file out: a cross
// without a plate.
func quietRemove(gtx layout.Context, s *surface, d int, color token.MatColor, name string) layout.Dimensions {
	size := image.Pt(d, d)
	style := surfaceStyle{radius: d / 2, content: color, background: color.SetOpacity(0), button: name}
	return s.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		inner := d * 5 / 9
		return offset(gtx, image.Pt((d-inner)/2, (d-inner)/2), func(gtx layout.Context) layout.Dimensions {
			return exact(gtx, image.Pt(inner, inner), func(gtx layout.Context) layout.Dimensions { return iconClear(gtx, color) })
		})
	})
}

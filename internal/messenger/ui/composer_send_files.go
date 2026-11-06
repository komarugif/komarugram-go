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
	"gio-mw/wdk"
	"gio-mw/widget/checkbox"
	"gio-mw/widget/scroll"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/widget"

	"komarugram/internal/crash"
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
	caption                   widget.Editor
	list                      scroll.List
	add, cancel, send         surface
	group, documents, quality *checkbox.Checkboxes[string]
	// viewing is set while the viewer shows the files.
	viewing bool
	// convert asks what to do with videos Telegram would not show as videos.
	convert boxConvert
	// results bring what the reading of files found out; generation tells
	// which box opening they are for.
	results    chan boxResult
	generation int
	// drag moves a picture of the grid to another place, as Telegram
	// Desktop's AlbumPreview does: the order the files go in.
	drag boxDrag
	// tips shows what the buttons over the files do: see tooltip.go.
	tips tooltips
}

// boxDrag is a picture of the box's grid being dragged.
type boxDrag struct {
	// file is the one pressed, nil for none; active is set once it moved
	// far enough to be dragged, and target is the place it would take.
	file   *boxFile
	active bool
	target int
	// start and at are where it was pressed and where the pointer is, grab
	// where in the picture it was taken, in the grid's pixels.
	start, at, grab f32.Point
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
	// spoiler hides the photo or video under a spoiler, which hide toggles;
	// blurred is its picture as shown then, made of blurredOf.
	spoiler            bool
	hide               surface
	blurred, blurredOf *image.RGBA
	// open opens it in the viewer. shown and long are a video's size and
	// length, as FFmpeg tells them.
	open  widget.Clickable
	shown image.Point
	long  time.Duration
}

// boxResult is what looking at a file found.
type boxResult struct {
	generation int
	file       *boxFile
	info       sendfiles.File
	thumb      *image.RGBA
	shown      image.Point
	long       time.Duration
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
		// What is typed goes on after it.
		end := b.caption.Len()
		b.caption.SetCaret(end, end)
		b.group.SetValues([]string{"group"})
		b.documents.SetValues(nil)
		if documents {
			b.documents.SetValues([]string{"documents"})
		}
		b.quality.SetValues(nil)
		// The caption takes the keyboard from the message field.
		b.modal.field = &b.caption
		b.modal.Open()
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
			func() {
				// A file is anything the user picked: one that panics is
				// told as one that failed, and the others go on.
				defer crash.Recover("inspect file", func(e *crash.Panic) { res.err = e })
				res.info, res.thumb, res.shown, res.long, res.err = readBoxFile(ctx, ffmpeg, f.Path)
			}()
			select {
			case results <- res:
				invalidate()
			case <-ctx.Done():
				return
			}
		}
	}()
}

// readBoxFile looks at the file at path, as the box shows it. A GIF has a length
// too, for its bar in the viewer.
func readBoxFile(ctx context.Context, ffmpeg, path string) (info sendfiles.File, picture *image.RGBA, shown image.Point, long time.Duration, err error) {
	info, err = sendfiles.Inspect(path)
	if err != nil {
		return info, nil, image.Point{}, 0, err
	}
	picture = boxThumbnail(ctx, ffmpeg, info)
	if info.Kind == sendfiles.KindVideo || info.Kind == sendfiles.KindAnimation {
		if c, err := video.ReadContainer(info.Path); err == nil {
			shown, long = image.Pt(c.Width, c.Height), c.Duration
		} else if m, err := video.ProbeChosenFile(ctx, info.Path, ffmpeg); err == nil {
			shown, long = image.Pt(m.Width, m.Height), m.Duration
		}
	}
	return info, picture, shown, long, nil
}

// baseName is the last element of a path of any system.
func baseName(path string) string {
	if i := strings.LastIndexAny(path, `/\`); i >= 0 {
		return path[i+1:]
	}
	return path
}

// boxThumbnail reads the picture a file shows in the box: a photo or a GIF
// as it is, a video as its first frame when there is something to decode it,
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
	if b.convert.job != nil {
		b.convert.job.stop()
	}
	b.convert.modal = modal{}
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
			r.file.File, r.file.thumb, r.file.shown, r.file.long, r.file.ready = r.info, r.thumb, r.shown, r.long, true
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
	way := b.way
	for _, f := range b.files {
		if err := c.permissions(c.chat).Check(sendfiles.Permission(f.File, way.Documents)); err != nil {
			b.modal.Toast(composerErrorText(err, l))
			return
		}
	}
	if len(c.toConvert()) > 0 {
		b.convert.askConvert()
		return
	}
	c.submitFiles()
}

// submitFiles sends what the box holds, as it is.
func (c *messageComposer) submitFiles() {
	b := &c.files
	caption := strings.TrimSpace(b.caption.Text())
	paths := make([]string, len(b.files))
	for i, f := range b.files {
		paths[i] = f.Path
	}
	way := b.way
	c.submit(c.chat, model.OutgoingMessage{Text: caption, Files: &model.OutgoingFiles{Paths: paths, Documents: way.Documents, Group: way.Group, HighQuality: way.HighQuality, Animated: boxAnimated(b.files), Spoilers: boxSpoilers(b.files, way), SilentAsVideo: c.silentAsVideo != nil && c.silentAsVideo()}})
	for _, path := range paths {
		if c.pasted[path] {
			c.uploading[path] = true
		}
	}
	b.modal.Close()
}

// boxAnimated are the places of the videos that go as GIFs: what the editor made
// of one. A .gif file is one by its type.
func boxAnimated(files []*boxFile) []int {
	var places []int
	for i, f := range files {
		if f.Kind == sendfiles.KindAnimation && strings.HasPrefix(f.MIME, "video/") {
			places = append(places, i)
		}
	}
	return places
}

// boxSpoilers are the places of the photos and videos that go under a spoiler:
// only media sent as media shows a picture to hide.
func boxSpoilers(files []*boxFile, way sendfiles.Way) []int {
	if way.Documents {
		return nil
	}
	var places []int
	for i, f := range files {
		switch f.Kind {
		case sendfiles.KindPhoto, sendfiles.KindVideo, sendfiles.KindAnimation:
			if f.spoiler {
				places = append(places, i)
			}
		}
	}
	return places
}

// replaceBoxPath keeps a result of the editor, temporary as a picture pasted is,
// in place of what the file was: removed with the box or once sent. What it was
// is removed only if it was temporary too, never what the user chose.
func (c *messageComposer) replaceBoxPath(old, path string) {
	c.pasted[path] = true
	if c.pasted[old] {
		c.forgetPasted([]string{old})
	}
}

// layoutFilesBox draws the box over the page, and does what its buttons ask.
func (c *messageComposer) layoutFilesBox(gtx layout.Context, p *chatPage, l localization.Catalog) {
	b := &c.files
	top := gtx
	if b.viewing || b.convert.modal.Shown() {
		// Under the viewer, it takes no input: an event goes to who asks for
		// it first, and this comes first, Escape closing the box. So under the
		// dialog that asks about converting.
		gtx = gtx.Disabled()
	}
	b.take(l)
	c.takeConverted(l)
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
		b.tips = tooltips{}
		return
	}
	// What the buttons over the files do shows over all of the box.
	tipArea := b.tips.begin(top)
	defer func() {
		if b.modal.Shown() {
			b.tips.layout(top)
		}
		tipArea.Pop()
	}()
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
		// A picture dragged and let go where it was is not a click on it.
		dragged := b.drag.active
		for _, f := range b.files {
			if f.hide.Clicked(gtx) && !dragged {
				f.spoiler = !f.spoiler
			}
		}
		for i, f := range b.files {
			if f.remove.Clicked(gtx) && !dragged {
				b.files = slices.Delete(b.files, i, i+1)
				if len(b.files) == 0 {
					b.modal.Close()
				}
				break
			}
		}
		for _, f := range b.files {
			if f.open.Clicked(gtx) && !dragged && boxViewable(f) && p.openBoxFiles != nil {
				media, items, current := boxGallery(p.source, c.chat, b.files, f)
				media.ffmpeg, media.replaced = c.ffmpegPath, c.replaceBoxPath
				b.viewing = true
				// The caption goes on being typed once the viewer has gone.
				p.openBoxFiles(media, c.chat, current, items, func() { b.viewing, b.modal.focus = false, true })
			}
		}
	}
	shown := b.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		width := min(gtx.Constraints.Max.X-2*gtx.Dp(16), gtx.Dp(filesBoxWidth))
		gtx.Constraints.Min.X, gtx.Constraints.Max.X = width, width
		return b.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions { return c.layoutFilesContent(gtx, p, l) }, defaultCardPadding)
	})
	if !shown {
		b.close()
		return
	}
	if !b.viewing {
		c.layoutConvert(top, l)
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
		defer crash.Recover("choose files", func(e *crash.Panic) {
			select {
			case c.fileResults <- fileChoice{chat: chat, form: form, menu: true, err: e}:
				c.invalidate()
			case <-ctx.Done():
			}
		})
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
		defer crash.Recover("choose files", func(e *crash.Panic) {
			select {
			case c.fileResults <- fileChoice{chat: chat, menu: true, err: e}:
				c.invalidate()
			case <-ctx.Done():
			}
		})
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
	size := image.Pt(width, rows*cell+(rows-1)*gap)
	slot := func(i int) image.Point { return image.Pt(i%columns*(cell+gap), i/columns*(cell+gap)) }
	b := &c.files
	order := b.dragTiles(gtx, tiles, columns, cell+gap, slot)
	d := &b.drag
	for i, f := range order {
		if d.active && f == d.file {
			continue
		}
		offset(gtx, slot(i), func(gtx layout.Context) layout.Dimensions { return c.layoutTile(gtx, p, f, image.Pt(cell, cell), l) })
	}
	if d.active {
		// Over the others, where the pointer holds it, inside the grid.
		at := d.at.Sub(d.grab).Round()
		at = image.Pt(min(max(at.X, 0), size.X-cell), min(max(at.Y, 0), size.Y-cell))
		// Lifted off the grid, as a menu is over the page.
		drawShadow(gtx, image.Rectangle{Min: at, Max: at.Add(image.Pt(cell, cell))}, gtx.Dp(8), menuShadow)
		offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
			return c.layoutTile(gtx, p, d.file, image.Pt(cell, cell), l)
		})
	}
	// Over the pictures, letting their clicks through to them.
	pass := pointer.PassOp{}.Push(gtx.Ops)
	area := clip.Rect(image.Rectangle{Max: size}).Push(gtx.Ops)
	event.Op(gtx.Ops, d)
	if d.active {
		pointer.CursorGrabbing.Add(gtx.Ops)
	}
	area.Pop()
	pass.Pop()
	return layout.Dimensions{Size: size}
}

// dragTiles follows the pointer over the grid of tiles, columns a row, step
// pixels apart, and moves the picture let go to where it was dragged. It is
// the order the pictures show in meanwhile: the one dragged at its target.
func (b *filesBox) dragTiles(gtx layout.Context, tiles []*boxFile, columns, step int, slot func(int) image.Point) []*boxFile {
	d := &b.drag
	placeAt := func(pos f32.Point) int {
		col := min(max(int(pos.X)/step, 0), columns-1)
		row := max(int(pos.Y)/step, 0)
		return min(row*columns+col, len(tiles)-1)
	}
	slop := float32(gtx.Dp(6))
	// The order the picture let go in this frame made, which the frame shows:
	// tiles are still the order before it.
	var dropped []*boxFile
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: d, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel})
		if !ok {
			break
		}
		e, ok := ev.(pointer.Event)
		if !ok {
			continue
		}
		switch e.Kind {
		case pointer.Press:
			if e.Buttons != pointer.ButtonPrimary {
				break
			}
			i := placeAt(e.Position)
			*d = boxDrag{file: tiles[i], target: i, start: e.Position, at: e.Position, grab: e.Position.Sub(layout.FPt(slot(i)))}
		case pointer.Drag:
			if d.file == nil {
				break
			}
			d.at = e.Position
			if moved := d.at.Sub(d.start); !d.active && moved.X*moved.X+moved.Y*moved.Y > slop*slop {
				d.active = true
			}
			if d.active {
				d.target = placeAt(e.Position)
			}
		case pointer.Release:
			if d.active {
				if from := slices.Index(tiles, d.file); from >= 0 {
					b.files = reorderTiles(b.files, tiles, from, d.target)
					dropped = moveTile(tiles, from, d.target)
				}
			}
			*d = boxDrag{}
		case pointer.Cancel:
			*d = boxDrag{}
		}
	}
	if dropped != nil {
		return dropped
	}
	from := slices.Index(tiles, d.file)
	if from < 0 {
		// Taken out meanwhile, or never pressed.
		*d = boxDrag{}
	}
	if !d.active {
		return tiles
	}
	return moveTile(tiles, from, min(d.target, len(tiles)-1))
}

// moveTile is tiles with the one at from moved to place to, the others
// making room.
func moveTile(tiles []*boxFile, from, to int) []*boxFile {
	order := slices.Clone(tiles)
	f := order[from]
	order = slices.Delete(order, from, from+1)
	return slices.Insert(order, to, f)
}

// reorderTiles is files with the tile at from of tiles moved to place to of
// them; the files shown as rows keep their places between them.
func reorderTiles(files, tiles []*boxFile, from, to int) []*boxFile {
	order := moveTile(tiles, from, to)
	out := slices.Clone(files)
	k := 0
	for i, f := range out {
		if slices.Contains(tiles, f) {
			out[i] = order[k]
			k++
		}
	}
	return out
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
	// Under the button that takes it out, drawn after it.
	tile := gtx
	tile.Constraints = layout.Exact(size)
	f.open.Layout(tile, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		if thumb := f.shownThumb(); thumb != nil && p.images != nil {
			widget.Image{Src: p.images.Op(thumb), Fit: widget.Cover, Position: layout.Center}.Layout(gtx)
		}
		if f.spoiler {
			// As Telegram Desktop shows media it will hide: blurred, darker.
			fillRect(gtx, spoilerDim, size)
		}
		return layout.Dimensions{Size: size}
	})
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
		return removeButton(gtx, &c.files.tips, &f.remove, d, l.T("files.remove"))
	})
	// Beside it, the one that hides it under a spoiler, or shows it again.
	hide, name := iconBlurOn, l.T("files.spoiler")
	if f.spoiler {
		hide, name = iconBlurOff, l.T("files.spoiler_off")
	}
	offset(gtx, image.Pt(size.X-2*d-gtx.Dp(10), gtx.Dp(6)), func(gtx layout.Context) layout.Dimensions {
		return boxIconButton(gtx, &c.files.tips, &f.hide, d, name, hide)
	})
	return layout.Dimensions{Size: size}
}

// spoilerDim darkens a picture to go under a spoiler, over its blur.
var spoilerDim = token.NewMatColorFromHexRGBA(0x00000040)

// shownThumb is the picture the tile of f shows: blurred under a spoiler.
func (f *boxFile) shownThumb() *image.RGBA {
	if !f.spoiler || f.thumb == nil {
		return f.thumb
	}
	if f.blurredOf != f.thumb {
		f.blurred, f.blurredOf = blurredPicture(f.thumb), f.thumb
	}
	return f.blurred
}

// blurredPicture is src made small and blurred, which drawn at the size of
// src shows its colors and nothing of what is in it, as Telegram Desktop
// blurs media under a spoiler.
func blurredPicture(src *image.RGBA) *image.RGBA {
	const side = 24
	r := src.Bounds()
	w, h := r.Dx(), r.Dy()
	if w <= 0 || h <= 0 {
		return src
	}
	dw, dh := max(1, w*side/max(w, h)), max(1, h*side/max(w, h))
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	// Each pixel is the mean of the pixels of src it covers.
	for y := range dh {
		y0, y1 := y*h/dh, max(y*h/dh+1, (y+1)*h/dh)
		for x := range dw {
			x0, x1 := x*w/dw, max(x*w/dw+1, (x+1)*w/dw)
			var sum [4]int
			for sy := y0; sy < y1; sy++ {
				row := src.Pix[src.PixOffset(r.Min.X+x0, r.Min.Y+sy):]
				for sx := range x1 - x0 {
					for c := range 4 {
						sum[c] += int(row[sx*4+c])
					}
				}
			}
			n := (y1 - y0) * (x1 - x0)
			for c := range 4 {
				dst.Pix[y*dst.Stride+x*4+c] = uint8(sum[c] / n)
			}
		}
	}
	// Then twice through a box of three each way, for the steps between
	// its pixels not to show once drawn large.
	tmp := image.NewRGBA(dst.Rect)
	for range 2 {
		for _, pass := range [2]image.Point{{1, 0}, {0, 1}} {
			for y := range dh {
				for x := range dw {
					for c := range 4 {
						sum := 0
						for k := -1; k <= 1; k++ {
							sx, sy := min(max(x+k*pass.X, 0), dw-1), min(max(y+k*pass.Y, 0), dh-1)
							sum += int(dst.Pix[sy*dst.Stride+sx*4+c])
						}
						tmp.Pix[y*tmp.Stride+x*4+c] = uint8((sum + 1) / 3)
					}
				}
			}
			dst, tmp = tmp, dst
		}
	}
	return dst
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
func removeButton(gtx layout.Context, tips *tooltips, s *surface, d int, name string) layout.Dimensions {
	return boxIconButton(gtx, tips, s, d, name, iconClear)
}

// boxIconButton is a round button with icon over a picture, on a dark plate,
// whose tooltip, where tips is set, tells name.
func boxIconButton(gtx layout.Context, tips *tooltips, s *surface, d int, name string, icon wdk.IconWidget) layout.Dimensions {
	white := token.NewMatColorFromHexRGB(0xffffff)
	size := image.Pt(d, d)
	style := surfaceStyle{radius: d / 2, content: white, background: boxOverlay, button: name, tips: tips}
	return s.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		inner := d * 5 / 9
		return offset(gtx, image.Pt((d-inner)/2, (d-inner)/2), func(gtx layout.Context) layout.Dimensions {
			return exact(gtx, image.Pt(inner, inner), func(gtx layout.Context) layout.Dimensions { return icon(gtx, white) })
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
				if !boxViewable(f) {
					return widget.Image{Src: p.images.Op(f.thumb), Fit: widget.Cover, Position: layout.Center}.Layout(gtx)
				}
				return f.open.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					return widget.Image{Src: p.images.Op(f.thumb), Fit: widget.Cover, Position: layout.Center}.Layout(gtx)
				})
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
				return quietRemove(gtx, &c.files.tips, &f.remove, d, sc.SurfaceVariant.OnColor, l.T("files.remove"))
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
func quietRemove(gtx layout.Context, tips *tooltips, s *surface, d int, color token.MatColor, name string) layout.Dimensions {
	size := image.Pt(d, d)
	style := surfaceStyle{radius: d / 2, content: color, background: color.SetOpacity(0), button: name, tips: tips}
	return s.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		inner := d * 5 / 9
		return offset(gtx, image.Pt((d-inner)/2, (d-inner)/2), func(gtx layout.Context) layout.Dimensions {
			return exact(gtx, image.Pt(inner, inner), func(gtx layout.Context) layout.Dimensions { return iconClear(gtx, color) })
		})
	})
}

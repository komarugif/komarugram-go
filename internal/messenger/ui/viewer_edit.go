// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"slices"
	"time"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/widget"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/chatmedia"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/photoedit"
	"komarugram/pkg/player"
	"komarugram/pkg/video"
)

// editAccent marks the tool in use, on the dark of the viewer: as Telegram
// Desktop's photoEditorButtonIconFgActive.
var editAccent = color.NRGBA{R: 0x5e, G: 0xb5, B: 0xf7, A: 255}

// editPalette is the colors a brush, a text and a figure take, Telegram
// Desktop's (color_picker.cpp).
var editPalette = [...]color.NRGBA{
	{A: 255},
	{R: 255, G: 255, B: 255, A: 255},
	{R: 234, G: 39, B: 57, A: 255},
	{R: 252, G: 150, B: 77, A: 255},
	{R: 252, G: 222, B: 101, A: 255},
	{R: 128, G: 200, B: 100, A: 255},
	{R: 73, G: 197, B: 237, A: 255},
	{R: 48, G: 81, B: 227, A: 255},
	{R: 219, G: 58, B: 210, A: 255},
	{R: 255, G: 114, B: 169, A: 255},
}

// Limits of what the editor takes and does.
const (
	// editMaxPixels bounds a photo decoded in full: Telegram's are far under.
	editMaxPixels = 100 << 20
	// editMaxUpscale is how much larger than its pixels a small picture shows.
	editMaxUpscale = 4
	// editStickerSide is the largest a sticker is kept in, its own usual size.
	editStickerSide = 512
	// editFramePause is how long a trim's handle rests before its frame is asked.
	editFramePause = 250 * time.Millisecond
)

type editMode uint8

const (
	editPaint editMode = iota
	editCrop
)

// cropAspect is what a crop keeps to.
type cropAspect uint8

const (
	cropFree cropAspect = iota
	cropOriginal
	cropSquare
)

// editGesture is what the pointer does on the picture.
type editGesture uint8

const (
	gestureNone editGesture = iota
	gestureStroke
	gestureMove
	gestureScale
	gestureShape
	gestureCrop
)

// viewerEdit is the editor of a photo or a video of the viewer, as Telegram
// Desktop's: over all of it, what is done kept in a photoedit.Scene, in the
// picture's own coordinates, and drawn by photoedit. Video is edited on its
// frames as FFmpeg gives them; it keeps its file's stream for that.
type viewerEdit struct {
	button widget.Clickable
	// allowed remembers canEdit of an item for a moment: it finds FFmpeg.
	allowedID model.MessageKey
	allowed   bool
	allowedAt time.Time

	open  bool
	focus bool
	keys  struct{}
	// transition is the way between the viewer and the editor, along which
	// all that moves goes: see viewer_edit_transition.go.
	transition editTransition
	// stage is where the editor shows the picture.
	stage image.Rectangle
	// resume plays on, on the way out, the video it paused on the way in.
	resume bool
	item   model.Message
	chat   int64
	// ctx ends when the editor closes, with all it does.
	ctx    context.Context
	cancel context.CancelFunc
	// loads brings the picture; loading is set until it has come.
	loads   chan editLoad
	loading bool
	loader  loadingIndicator

	video  bool
	media  video.Media
	stream *player.Stream
	// closeFile lets go of the file stream reads.
	closeFile func()
	// picture is what is drawn on, pictureScale of its pixels a pixel of the
	// picture itself. A video's is its frame at frameAt.
	picture      *image.RGBA
	pictureScale float64
	scene        *photoedit.Scene

	frames   chan editFrame
	frameAt  time.Duration
	frameDue time.Time

	preview photoedit.Preview
	// shown is what preview made last, op that as Gio draws it.
	shown  *image.RGBA
	op     paint.ImageOp
	hasOp  bool
	view   editView
	canvas struct{}

	mode       editMode
	tool       photoedit.Tool
	color      color.NRGBA
	size       widget.Float
	aspect     cropAspect
	selected   int
	armed      bool
	armedShape photoedit.ShapeType
	filled     bool
	shapesOpen bool

	gesture editGesture
	grab    photoedit.Grab
	from    photoedit.Point
	// origin is the element moved as the gesture found it; cropStart the crop.
	origin    *photoedit.Element
	cropStart image.Rectangle
	stroke    int
	lastPress time.Time
	lastItem  int
	hover     f32.Point
	hovering  bool

	text editText
	trim editTrim
	send editSend

	stickers *editStickers

	tools                                                    [5]widget.Clickable
	colors                                                   [len(editPalette)]widget.Clickable
	shapes                                                   [5]widget.Clickable
	modePaint, modeCrop, rotate, flip, undo, redo            widget.Clickable
	sticker, addText, shapesButton, remove, flipItem, repeat widget.Clickable
	fill, alignButton                                        widget.Clickable
	styleButton, fontButton                                  surface
	aspects                                                  [3]surface
	cancelEdit, done                                         surface
	toolList                                                 layout.List
	toast                                                    toast
}

// editView is how the picture is shown: t turns it, then scale and origin place
// it on the screen.
type editView struct {
	t      photoedit.Transform
	scale  float64
	origin image.Point
}

func (e *viewerEdit) opened() bool { return e.open }

// toScreen is where p of the picture shows.
func (e *viewerEdit) toScreen(p photoedit.Point) f32.Point {
	q := e.view.t.Map(p).Mul(e.view.scale)
	return f32.Pt(float32(q.X)+float32(e.view.origin.X), float32(q.Y)+float32(e.view.origin.Y))
}

// fromScreen is the place of the picture at p of the screen.
func (e *viewerEdit) fromScreen(p f32.Point) photoedit.Point {
	return e.view.t.Unmap(photoedit.Point{
		X: (float64(p.X) - float64(e.view.origin.X)) / e.view.scale,
		Y: (float64(p.Y) - float64(e.view.origin.Y)) / e.view.scale,
	})
}

type editLoad struct {
	picture   *image.RGBA
	scale     float64
	size      image.Point
	media     video.Media
	video     bool
	stream    *player.Stream
	closeFile func()
	err       error
}

type editFrame struct {
	at      time.Duration
	picture *image.RGBA
	scale   float64
	err     error
}

// canEdit reports whether the viewer offers to edit m: a photo, or a video with
// an FFmpeg, of a message, or a photo of a profile, that does not protect its
// content, and that something may be done with: a reply, a replacement, a copy in
// the account's own chat, the profile's main photo.
func (v *mediaViewer) canEdit(m model.Message) bool {
	e := &v.edit
	now := time.Now()
	if e.allowedID == m.Key && now.Sub(e.allowedAt) < 2*time.Second {
		return e.allowed
	}
	e.allowedID, e.allowedAt = m.Key, now
	e.allowed = v.canEditNow(m)
	return e.allowed
}

func (v *mediaViewer) canEditNow(m model.Message) bool {
	// One shown alone is no file of a message: a link's preview, an article's.
	profile := v.gallery == galleryProfile && model.IsProfilePhoto(m.Key.MessageID)
	if m.Media == nil || m.NoForwards || m.Local() || m.Key.MessageID <= 0 && !profile || v.isStory(m) || m.StoryOf != nil || v.alone {
		return false
	}
	switch v.gallery {
	case galleryPhotos, galleryGIFs, galleryShared:
	case galleryProfile:
		// What a profile's photo plays is no file to edit: the photo is.
		if m.Kind != model.MessagePhoto {
			return false
		}
	case galleryBox:
		if v.editSink == nil {
			return false
		}
	default:
		return false
	}
	switch m.Kind {
	case model.MessagePhoto:
	case model.MessageVideo, model.MessageGIF:
		if v.ffmpeg == nil || video.ResolveFFmpeg(v.ffmpeg()) == "" {
			return false
		}
	default:
		return false
	}
	return v.editSink != nil || len(v.editActions(m)) > 0
}

// editActions are what may be done with what the editor makes of m, the first
// offered first: a profile's photo becomes its main one or goes to the account's
// own chat, as there is no message to reply to; a message's file is replied with,
// takes its place, or goes to that chat.
func (v *mediaViewer) editActions(m model.Message) []editAction {
	var out []editAction
	if v.gallery == galleryProfile {
		if v.canSetAvatarEdit() {
			out = append(out, editAvatar)
		}
	} else {
		if v.canReplyEdit(m) {
			out = append(out, editReply)
		}
		if v.canReplaceEdit(m) {
			out = append(out, editReplace)
		}
	}
	if v.canSaveEdit(m) {
		out = append(out, editSaved)
	}
	return out
}

// canSetAvatarEdit reports whether what the editor makes may become the main
// photo of the profile shown.
func (v *mediaViewer) canSetAvatarEdit() bool {
	setter, ok := v.source.(model.ProfilePhotoSetter)
	return ok && setter.CanSetProfilePhoto(v.chat)
}

// canSaveEdit reports whether what the editor makes of m may go to the account's
// own chat, which takes anything.
func (v *mediaViewer) canSaveEdit(m model.Message) bool {
	if v.savedChat == nil || v.savedChat() == 0 {
		return false
	}
	_, composer := v.source.(model.ComposerStore)
	return composer
}

// canReplyEdit reports whether what the editor makes of m may go as a reply.
func (v *mediaViewer) canReplyEdit(m model.Message) bool {
	if v.gallery == galleryProfile {
		// A profile's photo is no message to reply to.
		return false
	}
	if _, ok := v.source.(model.ComposerStore); !ok {
		return false
	}
	kind := model.SendPhoto
	switch m.Kind {
	case model.MessageVideo:
		kind = model.SendVideo
	case model.MessageGIF:
		kind = model.SendGIF
	}
	if p, ok := v.source.(model.SendPermissionsSource); ok {
		return p.SendPermissions(v.chat).Allows(kind)
	}
	if r, ok := v.source.(model.RightsSource); ok {
		return r.CanSend(v.chat)
	}
	return true
}

// canReplaceEdit reports whether what the editor makes of m may replace its
// file.
func (v *mediaViewer) canReplaceEdit(m model.Message) bool {
	if v.gallery == galleryProfile {
		return false
	}
	if _, ok := v.source.(model.MediaEditor); !ok {
		return false
	}
	editor, ok := v.source.(model.MessageEditor)
	if !ok {
		return false
	}
	kind := model.KindUser
	if v.share.chats != nil {
		chats := v.share.chats()
		if i := slices.IndexFunc(chats, func(c model.Chat) bool { return c.ID == m.Key.ChatID }); i >= 0 {
			kind = chats[i].Kind
		}
	}
	return model.CanReplaceMedia(m, kind, time.Now(), editor.EditTimeLimit())
}

// updateEditButton takes the click on the button of m, the item shown.
func (v *mediaViewer) updateEditButton(gtx layout.Context, m model.Message) {
	if v.edit.button.Clicked(gtx) && v.canEdit(m) {
		v.openEdit(m)
		gtx.Execute(op.InvalidateCmd{})
	}
}

// openEdit starts editing m: at what is shown of a video.
func (v *mediaViewer) openEdit(m model.Message) {
	e := &v.edit
	at := time.Duration(0)
	if m.Kind == model.MessageVideo {
		if vs := v.video.state(m); vs.active {
			at = vs.position
		}
	} else if m.Kind == model.MessageGIF {
		at = v.full.GIFPosition(m)
	}
	// Paused, not stopped: its picture stays where it is, and plays on after.
	resume := false
	if vs := v.playState(m); vs.active && vs.playing {
		v.togglePlay(m)
		resume = true
	}
	ctx, cancel := context.WithCancel(context.Background())
	*e = viewerEdit{
		resume: resume,
		text:   editText{inbox: new(textInbox)},
		button: widget.Clickable{}, allowedID: e.allowedID, allowed: e.allowed, allowedAt: e.allowedAt,
		open: true, focus: true, item: m, chat: v.chat, ctx: ctx, cancel: cancel,
		loads: make(chan editLoad, 1), frames: make(chan editFrame, 1), loading: true, video: editsAsVideo(m),
		color: editPalette[2], selected: -1, stroke: -1, lastItem: -1, frameAt: at,
	}
	e.size.Value = .25
	e.toolList.Axis = layout.Horizontal
	source, invalidate := v.source, v.invalidate
	ffmpeg := ""
	if v.ffmpeg != nil {
		ffmpeg = v.ffmpeg()
	}
	loads := e.loads
	go func() {
		defer crash.Recover("open in the editor", func(p *crash.Panic) { loads <- editLoad{err: p}; invalidate() })
		loads <- loadEdit(ctx, source, m, ffmpeg, at)
		invalidate()
	}()
}

// editsAsVideo reports whether m is edited on its frames, by FFmpeg.
func editsAsVideo(m model.Message) bool {
	return m.Kind == model.MessageVideo || m.Kind == model.MessageGIF
}

// loadEdit gets what is edited: a photo decoded, or a video's stream, what
// FFmpeg tells of it and its frame at a time.
func loadEdit(ctx context.Context, source model.ConversationStore, m model.Message, ffmpeg string, at time.Duration) editLoad {
	if !editsAsVideo(m) {
		data, err := source.Media(ctx, m)
		if err != nil {
			return editLoad{err: err}
		}
		picture, err := decodeEditPhoto(data)
		if err != nil {
			return editLoad{err: err}
		}
		return editLoad{picture: picture, scale: 1, size: picture.Rect.Size()}
	}
	f, err := openFile(ctx, source, m)
	if err != nil {
		return editLoad{err: err}
	}
	stream, err := player.Serve("video", f.r, f.size)
	if err != nil {
		f.close()
		return editLoad{err: err}
	}
	fail := func(err error) editLoad {
		stream.Close()
		f.close()
		return editLoad{err: err}
	}
	media, err := video.ProbeMedia(ctx, stream.URL(), ffmpeg)
	if err != nil {
		return fail(err)
	}
	picture, err := video.FrameAt(ctx, ffmpeg, stream.URL(), media, at, image.Pt(1920, 1920))
	if err != nil {
		return fail(err)
	}
	size := media.EditSize()
	return editLoad{picture: picture, scale: float64(picture.Rect.Dx()) / float64(size.X), size: size, media: media, video: true, stream: stream, closeFile: f.close}
}

// decodeEditPhoto decodes a photo in full, having told its size first: a file
// from anyone may claim any.
func decodeEditPhoto(data []byte) (*image.RGBA, error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > editMaxPixels {
		return nil, fmt.Errorf("a picture of %d×%d", cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	out := image.NewRGBA(image.Rectangle{Max: b.Size()})
	draw.Draw(out, out.Rect, image.White, image.Point{}, draw.Src)
	draw.Draw(out, out.Rect, img, b.Min, draw.Over)
	return out, nil
}

// closeEdit stops all the editor does and lets go of what it holds.
func (v *mediaViewer) closeEdit() {
	e := &v.edit
	if !e.open {
		return
	}
	if e.cancel != nil {
		e.cancel()
	}
	e.send.release()
	stream, closeFile, job, loads := e.stream, e.closeFile, e.send.job, e.loads
	loading := e.loading
	if e.resume {
		v.togglePlay(e.item)
	}
	if e.stickers != nil {
		e.stickers.close()
	}
	allowedID, allowed, allowedAt := e.allowedID, e.allowed, e.allowedAt
	*e = viewerEdit{allowedID: allowedID, allowed: allowed, allowedAt: allowedAt}
	v.focus = v.open
	// What is on its way may still read the file: it goes when that ends.
	go func() {
		defer crash.Recover("close the editor", nil)
		if loading {
			if r := <-loads; r.stream != nil {
				r.stream.Close()
				r.closeFile()
			}
		}
		if job != nil {
			<-job.ended
			// What came with nobody left to take it.
			select {
			case o := <-job.done:
				if o.discard != nil {
					o.discard()
				}
			default:
			}
		}
		if stream != nil {
			stream.Close()
		}
		if closeFile != nil {
			closeFile()
		}
	}()
}

// releaseView drops the view's pictures, as a hidden window does: they are made
// again when it is shown.
func (e *viewerEdit) releaseView() {
	if e.open {
		e.preview.Release()
		e.hasOp, e.shown = false, nil
		e.op = paint.ImageOp{}
	}
}

// update takes what came to the editor before anything is drawn.
func (v *mediaViewer) updateEdit(gtx layout.Context, l localization.Catalog, animate bool) {
	e := &v.edit
	if e.stepTransition(gtx, animate) {
		v.closeEdit()
		return
	}
	if e.transition.leaving {
		// On the way out it takes nothing more; closing drops what still comes.
		return
	}
	select {
	case r := <-e.loads:
		e.loading = false
		if r.err != nil {
			if ctx := e.ctx; ctx.Err() == nil {
				v.toast.Show(l.T("editor.load_failed") + ": " + mediaErrorText(r.err))
			}
			v.leaveEdit()
			return
		}
		e.picture, e.pictureScale, e.video, e.media, e.stream, e.closeFile = r.picture, r.scale, r.video, r.media, r.stream, r.closeFile
		e.scene = photoedit.New(r.size)
	default:
	}
	select {
	case f := <-e.frames:
		e.trim.asking = false
		if f.err != nil {
			if e.ctx.Err() == nil {
				e.toast.Show(mediaErrorText(f.err))
			}
		} else if f.picture != nil {
			e.picture, e.pictureScale = f.picture, f.scale
			e.preview.Release()
			e.hasOp = false
		}
	default:
	}
	v.takeTexts()
	e.updateStickers(gtx, v, l)
	if e.focus {
		gtx.Execute(key.FocusCmd{Tag: &e.keys})
		e.focus = false
	}
	// Under a dialog the keys are its own: Escape closes it first.
	if !e.send.busy() {
		e.takeKeys(gtx, v, l)
		if !e.open || e.transition.leaving {
			return
		}
	}
	v.updateSend(gtx, l)
	if !e.open || e.transition.leaving {
		return
	}
	if e.send.busy() {
		// Under a dialog nothing changes what it is about.
		return
	}
	e.takeClicks(gtx, v, l)
	e.askFrame(gtx, v)
}

// takeKeys takes the keys: Escape goes back a step at a time, and takes the
// editor's own only here, never the viewer's.
func (e *viewerEdit) takeKeys(gtx layout.Context, v *mediaViewer, l localization.Catalog) {
	for {
		ev, ok := gtx.Event(
			key.FocusFilter{Target: &e.keys},
			key.Filter{Name: key.NameEscape},
			key.Filter{Focus: &e.keys, Name: "Z", Required: key.ModShortcut, Optional: key.ModShift},
			key.Filter{Focus: &e.keys, Name: "Y", Required: key.ModShortcut},
			key.Filter{Focus: &e.keys, Name: key.NameDeleteForward},
			key.Filter{Focus: &e.keys, Name: key.NameDeleteBackward},
		)
		if !ok {
			return
		}
		ke, isKey := ev.(key.Event)
		if !isKey || ke.State != key.Press {
			continue
		}
		// Chromium's: a key hides the tooltip.
		v.tips.hide()
		switch ke.Name {
		case key.NameEscape:
			e.escape(gtx, v)
			if !e.open || e.transition.leaving {
				return
			}
		case "Z":
			if ke.Modifiers.Contain(key.ModShift) {
				e.redoStep()
			} else {
				e.undoStep()
			}
		case "Y":
			e.redoStep()
		case key.NameDeleteForward, key.NameDeleteBackward:
			e.removeSelected()
		}
	}
}

// escape goes back a step: what is open closes first, and last the editor,
// asking first when something would be lost.
func (e *viewerEdit) escape(gtx layout.Context, v *mediaViewer) {
	switch {
	case e.text.editing:
		e.finishText(gtx)
	case e.stickers != nil && e.stickers.shown:
		e.stickers.shown = false
		gtx.Execute(key.FocusCmd{Tag: &e.keys})
	case e.armed || e.shapesOpen:
		e.armed, e.shapesOpen = false, false
	case e.selected >= 0:
		e.selected = -1
	default:
		e.askClose(v)
	}
}

// askClose closes the editor, or asks to when there is something to lose.
func (e *viewerEdit) askClose(v *mediaViewer) {
	if e.scene != nil && e.scene.Changed() {
		e.send.ask(editDialogDiscard)
		return
	}
	v.leaveEdit()
}

func (e *viewerEdit) undoStep() {
	if e.scene != nil && e.scene.CanUndo() {
		e.scene.Undo()
		e.selected, e.text.editing = -1, false
	}
}

func (e *viewerEdit) redoStep() {
	if e.scene != nil && e.scene.CanRedo() {
		e.scene.Redo()
		e.selected, e.text.editing = -1, false
	}
}

func (e *viewerEdit) removeSelected() {
	if e.selected >= 0 && e.scene != nil {
		e.scene.Remove(e.selected)
		e.selected, e.text.editing = -1, false
	}
}

// layoutEdit draws the editor over the viewer, which has drawn the backdrop, and
// the picture until the editor's own is there.
func (v *mediaViewer) layoutEdit(gtx layout.Context, l localization.Catalog, animate bool) {
	e := &v.edit
	size := gtx.Constraints.Max
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, &e.keys)
	stage := e.stage
	controls := e.controlsHeight(gtx)
	settled := e.settled()
	if e.scene != nil {
		e.sync(stage)
		if settled {
			e.takePointer(gtx, v, l)
		}
		e.drawStage(gtx, v, stage, l, settled)
	} else if e.waitingLong(gtx) {
		// Long on its way: over the picture, which stays shown.
		inRect(gtx, stage, func(gtx layout.Context) layout.Dimensions {
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return e.loader.sized(gtx, l, 48)
			})
		})
	}
	controlsGtx := gtx
	if e.transition.leaving {
		controlsGtx = gtx.Disabled()
	}
	if alpha := e.controlsAlpha(); alpha > 0 {
		fading := paint.PushOpacity(gtx.Ops, alpha)
		e.layoutControls(controlsGtx, v, l, image.Rect(0, size.Y-controls, size.X, size.Y))
		fading.Pop()
	}
	if e.scene == nil {
		return
	}
	e.layoutStickers(gtx, v, l, animate, stage)
	e.toast.Layout(gtx, image.Rect(0, 0, size.X, size.Y-controls))
	v.layoutSend(gtx, l)
}

// placeStage says where the editor shows the picture: above its rows, reaching
// up over where the viewer's bar was. A text being written does not move it: its
// row lies over the picture's bottom, the view staying as it is.
func (e *viewerEdit) placeStage(gtx layout.Context, size image.Point) {
	margin := gtx.Dp(20)
	controls := e.controlsHeight(gtx)
	e.stage = image.Rect(margin, margin, max(margin+1, size.X-margin), max(margin+1, size.Y-controls-margin/2))
}

// viewTransform is the transform the picture shows by: a crop being chosen shows
// all of it.
func (e *viewerEdit) viewTransform() photoedit.Transform {
	t := e.scene.Transform
	if e.mode == editCrop {
		return t.Whole(e.scene.Size)
	}
	if e.video {
		return t.Even()
	}
	return t
}

// sync fits the view to stage, and starts the picture over when the canvas it
// needs is another one.
func (e *viewerEdit) sync(stage image.Rectangle) {
	t := e.viewTransform()
	fit, placed := editFit(t.Size(), stage)
	e.view = editView{t: t, scale: fit, origin: placed.Min}
	canvas := photoedit.CanvasOf(t.Crop, min(fit, 1))
	if !e.preview.Ready() || e.preview.Canvas() != canvas {
		e.preview.SetBase(photoedit.BaseOf(e.picture, e.pictureScale, canvas), canvas, e.scene.Size)
		e.hasOp = false
	}
}

// drawStage draws the picture and what is over it, and registers the pointer's
// area.
func (e *viewerEdit) drawStage(gtx layout.Context, v *mediaViewer, stage image.Rectangle, l localization.Catalog, settled bool) {
	img, changed := e.preview.Render(e.scene.Elements, e.gesture == gestureNone, e.textRasters(gtx, v))
	if img != nil && (changed || !e.hasOp) {
		e.op, e.hasOp = paint.NewImageOp(img), true
	}
	e.shown = img
	if alpha := e.transition.alpha; e.hasOp && alpha > 0 {
		fading := paint.PushOpacity(gtx.Ops, alpha)
		e.drawPicture(gtx)
		fading.Pop()
	}
	if !settled {
		return
	}
	area := clip.Rect(stage).Push(gtx.Ops)
	if e.mode == editPaint {
		pointer.CursorCrosshair.Add(gtx.Ops)
	}
	event.Op(gtx.Ops, &e.canvas)
	area.Pop()
	switch {
	case e.mode == editCrop:
		e.drawCrop(gtx)
	case e.selected >= 0 && e.selected < len(e.scene.Elements):
		e.drawSelection(gtx, e.scene.Elements[e.selected])
	}
	if e.mode == editPaint && e.hovering && e.gesture != gestureMove && e.gesture != gestureScale && !e.armed {
		e.drawBrushRing(gtx)
	}
}

// drawPicture draws the view's picture where toScreen says, turned as it says.
func (e *viewerEdit) drawPicture(gtx layout.Context) {
	c := e.preview.Canvas()
	size := c.Size()
	sx, sy := float64(c.Rect.Dx())/float64(size.X), float64(c.Rect.Dy())/float64(size.Y)
	at := func(x, y float64) f32.Point {
		return e.toScreen(photoedit.Point{X: float64(c.Rect.Min.X) + x*sx, Y: float64(c.Rect.Min.Y) + y*sy})
	}
	o, a, b := at(0, 0), at(1, 0), at(0, 1)
	m := f32.NewAffine2D(a.X-o.X, b.X-o.X, o.X, a.Y-o.Y, b.Y-o.Y, o.Y)
	defer op.Affine(m).Push(gtx.Ops).Pop()
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	e.op.Add(gtx.Ops)
	paint.PaintOp{}.Add(gtx.Ops)
}

// cropOnScreen is the crop as a rectangle of the screen.
func (e *viewerEdit) cropOnScreen() (lo, hi f32.Point) {
	c := e.scene.Transform.Crop
	a := e.toScreen(photoedit.Point{X: float64(c.Min.X), Y: float64(c.Min.Y)})
	b := e.toScreen(photoedit.Point{X: float64(c.Max.X), Y: float64(c.Max.Y)})
	return f32.Pt(min(a.X, b.X), min(a.Y, b.Y)), f32.Pt(max(a.X, b.X), max(a.Y, b.Y))
}

// drawCrop dims what a crop leaves out, and draws its frame, its thirds and the
// handles at its corners.
func (e *viewerEdit) drawCrop(gtx layout.Context) {
	lo, hi := e.cropOnScreen()
	whole := e.scene.Size
	a := e.toScreen(photoedit.Point{})
	b := e.toScreen(photoedit.Point{X: float64(whole.X), Y: float64(whole.Y)})
	wlo, whi := f32.Pt(min(a.X, b.X), min(a.Y, b.Y)), f32.Pt(max(a.X, b.X), max(a.Y, b.Y))
	dim := color.NRGBA{A: 150}
	rect := func(x0, y0, x1, y1 float32, c color.NRGBA) {
		r := image.Rect(int(x0+.5), int(y0+.5), int(x1+.5), int(y1+.5))
		if !r.Empty() {
			paint.FillShape(gtx.Ops, c, clip.Rect(r).Op())
		}
	}
	rect(wlo.X, wlo.Y, whi.X, lo.Y, dim)
	rect(wlo.X, hi.Y, whi.X, whi.Y, dim)
	rect(wlo.X, lo.Y, lo.X, hi.Y, dim)
	rect(hi.X, lo.Y, whi.X, hi.Y, dim)
	line := color.NRGBA{R: 255, G: 255, B: 255, A: 90}
	for i := float32(1); i < 3; i++ {
		x := lo.X + (hi.X-lo.X)*i/3
		rect(x, lo.Y, x+1, hi.Y, line)
		y := lo.Y + (hi.Y-lo.Y)*i/3
		rect(lo.X, y, hi.X, y+1, line)
	}
	white := color.NRGBA{R: 255, G: 255, B: 255, A: 255}
	w := float32(max(1, gtx.Dp(1)))
	rect(lo.X, lo.Y, hi.X, lo.Y+w, white)
	rect(lo.X, hi.Y-w, hi.X, hi.Y, white)
	rect(lo.X, lo.Y, lo.X+w, hi.Y, white)
	rect(hi.X-w, lo.Y, hi.X, hi.Y, white)
	h := float32(gtx.Dp(5))
	for _, c := range [...]f32.Point{lo, {X: hi.X, Y: lo.Y}, hi, {X: lo.X, Y: hi.Y}} {
		rect(c.X-h, c.Y-h, c.X+h, c.Y+h, white)
	}
}

// drawSelection draws the frame of an element and the handles of its corners.
func (e *viewerEdit) drawSelection(gtx layout.Context, el *photoedit.Element) {
	corners := el.Corners()
	var p clip.Path
	p.Begin(gtx.Ops)
	for i, c := range corners {
		at := e.toScreen(c)
		if i == 0 {
			p.MoveTo(at)
		} else {
			p.LineTo(at)
		}
	}
	p.Close()
	paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 220}, clip.Stroke{Path: p.End(), Width: float32(max(1, gtx.Dp(1)))}.Op())
	r := gtx.Dp(5)
	for _, c := range corners {
		at := e.toScreen(c).Round()
		rect := image.Rect(at.X-r, at.Y-r, at.X+r, at.Y+r)
		paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 255}, clip.Ellipse(rect).Op(gtx.Ops))
	}
}

// brushWidth is the width of what a brush draws on the screen.
func (e *viewerEdit) brushWidth(gtx layout.Context) float64 {
	// Telegram Desktop's from 1 to 25.
	return float64(gtx.Dp(1)) * (1 + 24*float64(e.size.Value))
}

// drawBrushRing shows how wide the brush draws, under the pointer.
func (e *viewerEdit) drawBrushRing(gtx layout.Context) {
	w := e.brushWidth(gtx)
	switch e.tool {
	case photoedit.Marker:
		w *= 2.5
	case photoedit.Blur:
		w *= 3
	}
	r := max(1, int(w/2+.5))
	at := e.hover.Round()
	rect := image.Rect(at.X-r, at.Y-r, at.X+r, at.Y+r)
	paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 200}, clip.Stroke{Path: clip.Ellipse(rect).Path(gtx.Ops), Width: 1}.Op())
}

// takePointer takes what the pointer did on the picture.
func (e *viewerEdit) takePointer(gtx layout.Context, v *mediaViewer, l localization.Catalog) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &e.canvas, Kinds: pointer.Press | pointer.Drag | pointer.Release | pointer.Cancel | pointer.Move | pointer.Enter | pointer.Leave})
		if !ok {
			return
		}
		pe, isPointer := ev.(pointer.Event)
		if !isPointer {
			continue
		}
		e.hover = pe.Position
		switch pe.Kind {
		case pointer.Enter, pointer.Move:
			e.hovering = true
		case pointer.Leave:
			e.hovering = false
		case pointer.Press:
			if pe.Buttons == pointer.ButtonPrimary {
				e.press(gtx, pe, l)
			}
		case pointer.Drag:
			e.drag(gtx, pe)
		case pointer.Release, pointer.Cancel:
			e.release(gtx, pe)
		}
	}
}

// press starts what the pointer does from where it was pressed.
func (e *viewerEdit) press(gtx layout.Context, pe pointer.Event, l localization.Catalog) {
	if e.send.busy() {
		return
	}
	if e.stickers != nil {
		e.stickers.shown = false
	}
	p := e.fromScreen(pe.Position)
	e.from = p
	reach := float64(gtx.Dp(14)) / e.view.scale
	if e.mode == editCrop {
		g, ok := photoedit.GrabAt(e.scene.Transform.Crop, p, reach)
		if ok {
			e.gesture, e.grab, e.cropStart = gestureCrop, g, e.scene.Transform.Crop
		}
		return
	}
	if e.selected >= 0 && e.selected < len(e.scene.Elements) {
		el := e.scene.Elements[e.selected]
		for _, c := range el.Corners() {
			if c.Sub(p).Len() <= reach {
				e.gesture, e.origin = gestureScale, el
				e.scene.Begin()
				return
			}
		}
	}
	hit := e.scene.At(p, 0)
	if e.text.editing && hit != e.text.at {
		// A text left empty goes, and with it the places above it.
		e.finishText(gtx)
		hit = e.scene.At(p, 0)
	}
	if hit >= 0 {
		el := e.scene.Elements[hit]
		// A second press on a text writes in it.
		if hit == e.lastItem && gtx.Now.Sub(e.lastPress) < 400*time.Millisecond && el.Text != nil {
			e.selected = hit
			e.editTextOf(gtx, hit)
			return
		}
		e.finishText(gtx)
		e.selected, e.lastItem, e.lastPress = hit, hit, gtx.Now
		e.gesture, e.origin = gestureMove, el
		e.scene.Begin()
		return
	}
	e.finishText(gtx)
	e.selected, e.lastItem = -1, -1
	gtx.Execute(key.FocusCmd{Tag: &e.keys})
	if e.armed {
		e.gesture = gestureShape
		e.scene.Begin()
		e.stroke = e.scene.Add(e.shapeBetween(gtx, p, p, pe.Modifiers))
		return
	}
	e.gesture = gestureStroke
	e.scene.Begin()
	e.stroke = e.scene.Add(&photoedit.Element{Stroke: &photoedit.Stroke{
		Tool: e.tool, Color: e.color, Width: e.brushWidth(gtx) / e.view.scale, Points: []photoedit.Point{p},
	}})
}

// drag goes on with what the pointer does.
func (e *viewerEdit) drag(gtx layout.Context, pe pointer.Event) {
	p := e.fromScreen(pe.Position)
	switch e.gesture {
	case gestureStroke:
		if e.stroke < 0 || e.stroke >= len(e.scene.Elements) {
			return
		}
		el := e.scene.Elements[e.stroke]
		s := *el.Stroke
		if last := s.Points[len(s.Points)-1]; p.Sub(last).Len()*e.view.scale < 2 {
			return
		}
		s.Points = append(s.Points, p)
		e.scene.Replace(e.stroke, &photoedit.Element{Stroke: &s})
	case gestureMove:
		moved := *e.origin
		moved.Center = e.origin.Center.Add(p.Sub(e.from))
		e.scene.Replace(e.selected, &moved)
	case gestureScale:
		e.scene.Replace(e.selected, turnAndScale(e.origin, e.from, p))
	case gestureShape:
		e.scene.Replace(e.stroke, e.shapeBetween(gtx, e.from, p, pe.Modifiers))
	case gestureCrop:
		aspect := 0.
		switch e.aspect {
		case cropOriginal:
			aspect = float64(e.scene.Size.X) / float64(e.scene.Size.Y)
		case cropSquare:
			aspect = 1
		}
		minSide := max(8, min(e.scene.Size.X, e.scene.Size.Y)/32)
		e.scene.Transform.Crop = photoedit.DragCrop(e.cropStart, e.grab, p.Sub(e.from), e.scene.Size, aspect, minSide)
	}
}

// release ends what the pointer did.
func (e *viewerEdit) release(gtx layout.Context, pe pointer.Event) {
	switch e.gesture {
	case gestureStroke, gestureMove, gestureScale:
		e.scene.End()
	case gestureShape:
		// One press alone leaves a figure of its usual size.
		if el := e.scene.Elements[e.stroke]; el.Width*e.view.scale < float64(gtx.Dp(6)) {
			at := e.view.t.Map(e.from)
			d := float64(gtx.Dp(100)) / e.view.scale
			if el.Shape.Type == photoedit.ShapeArrow {
				e.scene.Replace(e.stroke, e.shapeBetween(gtx, e.from, e.view.t.Unmap(photoedit.Point{X: at.X + d, Y: at.Y}), 0))
			} else {
				e.scene.Replace(e.stroke, e.shapeBetween(gtx, e.from, e.view.t.Unmap(photoedit.Point{X: at.X + d, Y: at.Y + d}), 0))
			}
		}
		e.scene.End()
		e.selected, e.armed = e.stroke, false
	}
	e.gesture, e.origin, e.stroke = gestureNone, nil, -1
}

// turnAndScale is el with the corner at from dragged to to: it grows and turns
// around its center.
func turnAndScale(el *photoedit.Element, from, to photoedit.Point) *photoedit.Element {
	was, now := from.Sub(el.Center), to.Sub(el.Center)
	if was.Len() < 1 || now.Len() < 1 {
		return el
	}
	k := max(.1, min(10, now.Len()/was.Len()))
	out := *el
	out.Width, out.Height = el.Width*k, el.Height*k
	out.Rotation = el.Rotation + (angleOf(now)-angleOf(was))*180/math.Pi
	if el.Text != nil {
		t := *el.Text
		t.Size = el.Text.Size * k
		out.Text = &t
	}
	return &out
}

// shapeBetween is the armed figure from a to b of the picture: in the box
// between them as they show, upright, or an arrow from one to the other.
func (e *viewerEdit) shapeBetween(gtx layout.Context, a, b photoedit.Point, mods key.Modifiers) *photoedit.Element {
	lineWidth := e.brushWidth(gtx) / e.view.scale
	shape := &photoedit.Shape{Type: e.armedShape, Color: e.color, Filled: e.filled, Width: lineWidth}
	if e.armedShape == photoedit.ShapeArrow {
		d := b.Sub(a)
		if mods.Contain(key.ModShift) {
			// To the nearest of eight directions, as Telegram Desktop snaps it.
			step := math.Pi / 4
			angle := float64(int(angleOf(d)/step+8.5)-8) * step
			sin, cos := math.Sincos(angle)
			d = photoedit.Point{X: d.Len() * cos, Y: d.Len() * sin}
			b = a.Add(d)
		}
		return &photoedit.Element{Shape: shape, Center: a.Add(b).Mul(.5), Width: d.Len(), Height: max(lineWidth*4, float64(gtx.Dp(16))/e.view.scale), Rotation: angleOf(d) * 180 / math.Pi}
	}
	// In the view, for the box to stand upright there.
	va, vb := e.view.t.Map(a), e.view.t.Map(b)
	w, h := vb.X-va.X, vb.Y-va.Y
	if w < 0 {
		w = -w
	}
	if h < 0 {
		h = -h
	}
	if e.armedShape == photoedit.ShapeStar {
		h = w * photoedit.StarAspect
	} else if mods.Contain(key.ModShift) {
		w, h = max(w, h), max(w, h)
	}
	mid := e.view.t.Unmap(va.Add(vb).Mul(.5))
	if e.armedShape == photoedit.ShapeStar {
		// Grown from the press, as the box's height follows its width.
		mid = e.view.t.Unmap(photoedit.Point{X: (va.X + vb.X) / 2, Y: va.Y + h/2})
	}
	rotation, flipped := e.view.t.Upright()
	return &photoedit.Element{Shape: shape, Center: mid, Width: w, Height: h, Rotation: rotation, Flipped: flipped}
}

// askFrame asks for the frame a trim's handle rests at.
func (e *viewerEdit) askFrame(gtx layout.Context, v *mediaViewer) {
	if !e.video || e.frameDue.IsZero() || e.trim.asking {
		return
	}
	if gtx.Now.Before(e.frameDue) {
		gtx.Execute(op.InvalidateCmd{At: e.frameDue})
		return
	}
	e.frameDue = time.Time{}
	e.trim.asking = true
	ffmpeg := ""
	if v.ffmpeg != nil {
		ffmpeg = v.ffmpeg()
	}
	ctx, url, media, at, frames, invalidate := e.ctx, e.stream.URL(), e.media, e.frameAt, e.frames, v.invalidate
	size := media.EditSize()
	go func() {
		defer crash.Recover("frame of the video", func(p *crash.Panic) { frames <- editFrame{err: p}; invalidate() })
		picture, err := video.FrameAt(ctx, ffmpeg, url, media, at, image.Pt(1920, 1920))
		if err != nil {
			frames <- editFrame{at: at, err: err}
			invalidate()
			return
		}
		frames <- editFrame{at: at, picture: picture, scale: float64(picture.Rect.Dx()) / float64(size.X)}
		invalidate()
	}()
}

// editStickers is the panel of stickers, the composer's picker with its stickers
// alone, and the sticker chosen until its frame is decoded.
type editStickers struct {
	picker *messageComposer
	page   *chatPage
	// grid decodes the panel's stickers when the viewer's own decoders read only
	// what it shows: the box of files'. It is nil otherwise.
	grid  *chatmedia.Manager
	shown bool
	// media decodes the sticker chosen at the size it is kept in; waiting is
	// that one.
	media   *chatmedia.Manager
	waiting *model.PickerItem
}

func (s *editStickers) close() {
	s.picker.cancel()
	s.media.Close()
	if s.grid != nil {
		s.grid.Close()
	}
}

// openStickers opens the panel, making it the first time.
func (e *viewerEdit) openStickers(v *mediaViewer, l localization.Catalog) {
	if e.stickers == nil {
		s := &editStickers{}
		store, thumbs := v.source, v.thumbs
		if v.stickerStore != nil {
			store = v.stickerStore
			s.grid = chatmedia.NewSized(store, v.invalidate, 96, 256)
			thumbs = s.grid
		}
		s.picker = newMessageComposer(store, v.invalidate)
		s.picker.chat = e.chat
		s.picker.tab = model.PickerStickers
		s.picker.pickerOpen = true
		s.picker.pick = func(item model.PickerItem) {
			s.waiting = &item
			s.shown = false
		}
		s.page = &chatPage{media: thumbs, images: v.images, source: store, invalidate: v.invalidate}
		s.media = chatmedia.NewSized(store, v.invalidate, 2, editStickerSide)
		e.stickers = s
		s.picker.request(l, false)
	}
	e.stickers.shown = true
}

// updateStickers takes what the panel did, and places the sticker chosen when
// its frame is there: the first of one that plays, on a video too.
func (e *viewerEdit) updateStickers(gtx layout.Context, v *mediaViewer, l localization.Catalog) {
	s := e.stickers
	if s == nil {
		return
	}
	s.picker.updatePick(gtx, l)
	if s.waiting == nil {
		return
	}
	s.media.BeginFrame()
	defer s.media.EndFrame()
	status := s.media.StatusFit(s.waiting.Media, false, image.Pt(editStickerSide, editStickerSide), false)
	switch {
	case status.Err != nil:
		e.toast.Show(mediaErrorText(status.Err))
		s.waiting = nil
	case status.Frame != nil && e.scene != nil:
		e.placeSticker(gtx, toNRGBA(status.Frame))
		s.waiting = nil
		s.media.Clear()
	default:
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(50 * time.Millisecond)})
	}
}

// toNRGBA is a copy of img with colors of their own.
func toNRGBA(img image.Image) *image.NRGBA {
	b := img.Bounds()
	out := image.NewNRGBA(image.Rectangle{Max: b.Size()})
	draw.Draw(out, out.Rect, img, b.Min, draw.Src)
	return out
}

// placeSticker puts a sticker in the middle of what shows, upright.
func (e *viewerEdit) placeSticker(gtx layout.Context, img *image.NRGBA) {
	b := img.Bounds()
	if b.Empty() {
		return
	}
	t := e.viewTransform()
	out := t.Size()
	long := float64(min(out.X, out.Y)) * .4
	w, h := long, long*float64(b.Dy())/float64(b.Dx())
	if b.Dy() > b.Dx() {
		w, h = long*float64(b.Dx())/float64(b.Dy()), long
	}
	rotation, flipped := t.Upright()
	e.mode = editPaint
	e.selected = e.scene.Add(&photoedit.Element{
		Sticker: &photoedit.Sticker{Image: img},
		Center:  t.Unmap(photoedit.Point{X: float64(out.X) / 2, Y: float64(out.Y) / 2}),
		Width:   w, Height: h, Rotation: rotation, Flipped: flipped,
	})
}

// layoutStickers draws the panel over the right of the picture.
func (e *viewerEdit) layoutStickers(gtx layout.Context, v *mediaViewer, l localization.Catalog, animate bool, stage image.Rectangle) {
	s := e.stickers
	if s == nil || !s.shown {
		return
	}
	if s.grid != nil {
		s.grid.BeginFrame()
		defer s.grid.EndFrame()
	}
	w, h := min(gtx.Dp(380), stage.Dx()), min(gtx.Dp(480), stage.Dy())
	r := image.Rect(stage.Max.X-w, stage.Max.Y-h, stage.Max.X, stage.Max.Y)
	inRect(gtx, r, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(r.Size())
		defer clip.Rect{Max: r.Size()}.Push(gtx.Ops).Pop()
		return s.picker.pickerLayout(gtx, l, s.page, animate, r.Min)
	})
}

// angleOf is the direction of p, clockwise from the right, in radians.
func angleOf(p photoedit.Point) float64 { return math.Atan2(p.Y, p.X) }

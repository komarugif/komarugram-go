// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"gio-mw/token"
	"gio-mw/widget/indicator"
	"gio-mw/widget/radio"

	"gioui.org/layout"
	"gioui.org/op"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/photoedit"
	"komarugram/pkg/video"
)

// editDialog is what the editor's dialog asks or tells.
type editDialog uint8

const (
	editDialogNone editDialog = iota
	// editDialogChoice asks what to do with the result: editActions.
	editDialogChoice
	// editDialogWork tells how far the result has come, and stops it.
	editDialogWork
	// editDialogFailed tells a replacement, or a profile's new photo, failed, the
	// result being kept, and offers to try again or, a replacement, to send it as a
	// reply.
	editDialogFailed
	// editDialogDiscard asks whether to leave with what was done lost.
	editDialogDiscard
)

// editAction is what becomes of the result.
type editAction uint8

const (
	editReply editAction = iota
	editReplace
	// editSaved sends it to the account's own chat, as a message of its own.
	editSaved
	// editAvatar makes it the main photo of the profile it was a photo of.
	editAvatar
)

// editActionKeys are the texts of the actions.
var editActionKeys = map[editAction]string{
	editReply: "editor.as_reply", editReplace: "editor.replace",
	editSaved: "editor.to_saved", editAvatar: "editor.as_avatar",
}

// kept reports whether a result that failed to go this way is kept to be tried
// again: one that went to the outbox is the store's to send.
func (a editAction) kept() bool { return a == editReplace || a == editAvatar }

// editStage is what a job does now.
type editStage uint8

const (
	stagePreparing editStage = iota
	stageEncoding
	stageSending
)

// editSend is the way from what was edited to a message.
type editSend struct {
	modal  modal
	dialog editDialog
	radios *radio.Radios[editAction]
	// actions are those the radios offer.
	actions []editAction
	action  editAction
	bar     *indicator.Indicator
	job     *editJob
	// dir and path are the result, kept while a replacement that failed may be
	// tried again; failure is what it failed with.
	dir, path string
	failure   error
	// canReply tells whether that result may still go as a reply.
	canReply bool

	confirm, cancel, asReply surface
}

// editJob is a result being made and sent, off the frame.
type editJob struct {
	stop  context.CancelFunc
	done  chan editOutcome
	ended chan struct{}
	mu    sync.Mutex
	stage editStage
	share float64
}

type editOutcome struct {
	dir, path string
	action    editAction
	// replaced is the message with its new file, after a replacement.
	replaced *model.Message
	// commit and discard are those of a result the box of files takes.
	commit, discard func()
	err             error
}

// report tells how far the job went. Within a stage the share only grows: FFmpeg's
// own count of the time done goes back a little as it ends.
func (j *editJob) report(stage editStage, share float64) {
	j.mu.Lock()
	if stage == j.stage {
		share = max(share, j.share)
	}
	j.stage, j.share = stage, share
	j.mu.Unlock()
}

func (j *editJob) state() (editStage, float64) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.stage, j.share
}

// busy reports whether a dialog holds the editor.
func (s *editSend) busy() bool { return s.dialog != editDialogNone }

// ask opens the dialog of kind.
func (s *editSend) ask(kind editDialog) {
	s.dialog = kind
	s.modal.Open()
}

// release stops the job and removes the result kept.
func (s *editSend) release() {
	if s.job != nil {
		s.job.stop()
	}
	s.dropResult()
}

func (s *editSend) dropResult() {
	if s.dir != "" {
		os.RemoveAll(s.dir)
		s.dir, s.path = "", ""
	}
}

// finish goes on from Done: asks what to do with a result that may replace the
// file, and otherwise makes it a reply. Nothing done leaves nothing to send.
func (e *viewerEdit) finish(gtx layout.Context, v *mediaViewer, l localization.Catalog) {
	if !e.scene.Changed() {
		v.leaveEdit()
		return
	}
	if v.editSink != nil {
		// In the box it replaces the file: there is no message to ask about.
		e.start(gtx, v, editReplace)
		return
	}
	actions := v.editActions(e.item)
	switch len(actions) {
	case 0:
		e.toast.Show(l.T("editor.not_allowed"))
	case 1:
		e.start(gtx, v, actions[0])
	default:
		s := &e.send
		s.action, s.actions = actions[0], actions
		s.radios = radio.NewRadios(actions, actions[0], func(a editAction) { s.action = a })
		s.ask(editDialogChoice)
	}
}

// editTarget is the chat a result goes to as action says: the account's own for
// one saved there, the one shown for the others.
func (v *mediaViewer) editTarget(action editAction) int64 {
	if action == editSaved && v.savedChat != nil {
		return v.savedChat()
	}
	return v.edit.chat
}

// start makes the result, off the frame, and sends it as action says.
func (e *viewerEdit) start(gtx layout.Context, v *mediaViewer, action editAction) {
	s := &e.send
	// A result kept of before is another's now.
	s.dropResult()
	texts := e.exactTexts(gtx)
	ctx, cancel := context.WithCancel(e.ctx)
	job := &editJob{stop: cancel, done: make(chan editOutcome, 1), ended: make(chan struct{}), share: -1}
	s.job, s.bar, s.action = job, indicator.Linear(), action
	s.ask(editDialogWork)
	source, m, chat, sink := v.source, e.item, v.editTarget(action), v.editSink
	silent := v.silentAsVideo != nil && v.silentAsVideo()
	scene := *e.scene
	scene.Transform = e.scene.Transform
	if e.video {
		scene.Transform = scene.Transform.Even()
	}
	var ffmpeg string
	if v.ffmpeg != nil {
		ffmpeg = v.ffmpeg()
	}
	picture, media, isVideo := e.picture, e.media, e.video
	url := ""
	if e.stream != nil {
		url = e.stream.URL()
	}
	invalidate := v.invalidate
	report := func(stage editStage, share float64) {
		job.report(stage, share)
		invalidate()
	}
	go func() {
		defer close(job.ended)
		defer crash.Recover("save what was edited", func(p *crash.Panic) { job.done <- editOutcome{err: p, action: action}; invalidate() })
		defer invalidate()
		// A file of the box that went without loss stays so: it may go as a document.
		photoName := "photo.jpg"
		if sink != nil && m.Media != nil && m.Media.MIMEType != "image/jpeg" {
			photoName = "photo.png"
		}
		dir, path, err := makeEdited(ctx, &scene, texts, picture, media, isVideo, m.Kind == model.MessageGIF, photoName, url, ffmpeg, report)
		if err != nil {
			if dir != "" {
				os.RemoveAll(dir)
			}
			job.done <- editOutcome{err: err, action: action}
			return
		}
		if sink != nil {
			edited, err := sink(ctx, m, path)
			os.RemoveAll(dir)
			job.done <- editOutcome{err: err, action: action, replaced: &edited.message, commit: edited.commit, discard: edited.discard}
			return
		}
		replaced, err := deliverEdited(ctx, source, m, chat, path, ffmpeg, silent, action, report)
		if err != nil && action.kept() && ctx.Err() == nil {
			// Kept: the work is not lost with a connection, or with the time
			// the message could be edited in.
			job.done <- editOutcome{dir: dir, path: path, err: err, action: action}
			return
		}
		// A reply is in the store once queued, its file copied.
		os.RemoveAll(dir)
		job.done <- editOutcome{err: err, action: action, replaced: replaced}
	}()
}

// retry sends the result kept again.
func (e *viewerEdit) retry(v *mediaViewer, action editAction) {
	s := &e.send
	ctx, cancel := context.WithCancel(e.ctx)
	job := &editJob{stop: cancel, done: make(chan editOutcome, 1), ended: make(chan struct{}), stage: stageSending, share: -1}
	s.job, s.bar, s.action = job, indicator.Linear(), action
	s.ask(editDialogWork)
	source, m, chat, path, dir := v.source, e.item, v.editTarget(action), s.path, s.dir
	silent := v.silentAsVideo != nil && v.silentAsVideo()
	s.dir, s.path = "", ""
	var ffmpeg string
	if v.ffmpeg != nil {
		ffmpeg = v.ffmpeg()
	}
	invalidate := v.invalidate
	report := func(stage editStage, share float64) {
		job.report(stage, share)
		invalidate()
	}
	go func() {
		defer close(job.ended)
		defer crash.Recover("send what was edited", func(p *crash.Panic) { job.done <- editOutcome{err: p, action: action}; invalidate() })
		defer invalidate()
		replaced, err := deliverEdited(ctx, source, m, chat, path, ffmpeg, silent, action, report)
		if err != nil && action.kept() && ctx.Err() == nil {
			job.done <- editOutcome{dir: dir, path: path, err: err, action: action}
			return
		}
		os.RemoveAll(dir)
		job.done <- editOutcome{err: err, action: action, replaced: replaced}
	}()
}

// makeEdited makes the result in a directory of its own, and returns both.
func makeEdited(ctx context.Context, s *photoedit.Scene, texts []exactText, picture *image.RGBA, media video.Media, isVideo, gif bool, photoName, url, ffmpeg string, report func(editStage, float64)) (dir, path string, err error) {
	report(stagePreparing, -1)
	rasters, err := drawExactTexts(ctx, texts)
	if err != nil {
		return "", "", err
	}
	dir, err = os.MkdirTemp("", "komarugram-go-edit-*")
	if err != nil {
		return "", "", err
	}
	if !isVideo {
		path = filepath.Join(dir, photoName)
		return dir, path, savePhotoResult(s, picture, rasters, path)
	}
	path = filepath.Join(dir, "video.mp4")
	plan, err := videoPlan(dir, s, media, rasters)
	if err != nil {
		return dir, "", err
	}
	plan.GIF = gif
	if err := ctx.Err(); err != nil {
		return dir, "", err
	}
	report(stageEncoding, 0)
	err = video.EncodeEdit(ctx, ffmpeg, url, path, plan, func(p video.GIFProgress) { report(stageEncoding, p.Share()) })
	return dir, path, err
}

// drawExactTexts draws the pictures of texts.
func drawExactTexts(ctx context.Context, texts []exactText) (photoedit.Rasters, error) {
	pictures := make(map[*photoedit.Text]*image.NRGBA, len(texts))
	for _, t := range texts {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		img, err := drawText(t.ops, t.size)
		if err != nil {
			return nil, err
		}
		pictures[t.text] = img
	}
	return func(t *photoedit.Text, _ float64) *image.NRGBA { return pictures[t] }, nil
}

// photoQuality keeps the picture as it is until Telegram's own compression, a
// step of sendfiles.PreparePhoto when it is too large.
const photoQuality = 97

// savePhotoResult draws the scene on the whole photo, turned and cut, as a JPEG,
// or a PNG by the extension of path.
func savePhotoResult(s *photoedit.Scene, picture *image.RGBA, rasters photoedit.Rasters, path string) error {
	c := photoedit.CanvasOf(image.Rectangle{Max: s.Size}, 1)
	out := s.Transform.Apply(photoedit.Flatten(picture, s.Elements, c, rasters, s.Size))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if strings.EqualFold(filepath.Ext(path), ".png") {
		err = png.Encode(f, out)
	} else {
		err = jpeg.Encode(f, out, &jpeg.Options{Quality: photoQuality})
	}
	if err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// videoPlan saves the layers of the scene in dir, and says what FFmpeg makes of
// the video with them.
func videoPlan(dir string, s *photoedit.Scene, media video.Media, rasters photoedit.Rasters) (video.EditPlan, error) {
	plan := video.EditPlan{Media: media, Flip: s.Transform.Flip, Angle: s.Transform.Angle}
	if s.Transform.Crop != (image.Rectangle{Max: s.Size}) {
		plan.Crop = s.Transform.Crop
	}
	trim := s.Trim.Clamped(media.Duration)
	plan.From, plan.To = trim.From, trim.To
	encoder := png.Encoder{CompressionLevel: png.BestSpeed}
	for i, step := range photoedit.Layers(s, rasters) {
		path := filepath.Join(dir, fmt.Sprintf("layer%d.png", i))
		f, err := os.Create(path)
		if err != nil {
			return plan, err
		}
		var img image.Image = step.Over
		if step.Blur != nil {
			img = &image.Gray{Pix: step.Blur.Pix, Stride: step.Blur.Stride, Rect: step.Blur.Rect}
		}
		if err := encoder.Encode(f, img); err != nil {
			f.Close()
			return plan, err
		}
		if err := f.Close(); err != nil {
			return plan, err
		}
		plan.Steps = append(plan.Steps, video.EditStep{Path: path, Radius: step.Radius})
	}
	return plan, nil
}

// deliverEdited sends the file at path: as a reply to m, in chat, or as a message
// of its own there, the account's own chat, the store's to send from then on; in
// place of m's file, whose message it returns; or as the main photo of chat's
// profile.
func deliverEdited(ctx context.Context, source model.ConversationStore, m model.Message, chat int64, path, ffmpeg string, silentAsVideo bool, action editAction, report func(editStage, float64)) (*model.Message, error) {
	report(stageSending, -1)
	progress := func(done, total int64) {
		if total > 0 {
			report(stageSending, float64(done)/float64(total))
		}
	}
	if action == editAvatar {
		setter, ok := source.(model.ProfilePhotoSetter)
		if !ok {
			return nil, model.ErrProfilePhotoForbidden
		}
		return nil, setter.SetProfilePhoto(ctx, chat, path, progress)
	}
	if action == editReplace {
		editor, ok := source.(model.MediaEditor)
		if !ok {
			return nil, model.ErrEditForbidden
		}
		replaced, err := editor.ReplaceMedia(ctx, m, path, ffmpeg, progress)
		if err != nil {
			return nil, err
		}
		return &replaced, nil
	}
	msg := model.OutgoingMessage{
		RandomID: randomMessageID(),
		FFmpeg:   ffmpeg,
		Files:    &model.OutgoingFiles{Paths: []string{path}, HighQuality: true, SilentAsVideo: silentAsVideo},
	}
	if action == editReply {
		msg.ReplyTo = m.Key.MessageID
	}
	if m.Kind == model.MessageGIF {
		msg.Files.Animated = []int{0}
	}
	if err := msg.Validate(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	if queue, ok := source.(model.OutboxStore); ok {
		return nil, queue.QueueMessage(ctx, chat, msg)
	}
	composer, ok := source.(model.ComposerStore)
	if !ok {
		return nil, errors.New("no way to send")
	}
	return nil, composer.Send(ctx, chat, msg)
}

// viewerReplaced is the picture to show for an item whose file was replaced,
// until the new one has come.
type viewerReplaced struct {
	key     model.MessageKey
	picture image.Image
}

// takeReplaced makes m, with its new file, the item it was: all kept of the old
// one goes, the video that played first of all, and what the editor shows now
// stands in for the file until it has come from Telegram.
func (v *mediaViewer) takeReplaced(m model.Message) {
	e := &v.edit
	id := m.Key.MessageID
	v.mu.Lock()
	if i := slices.IndexFunc(v.items, func(o model.Message) bool { return o.Key.MessageID == id }); i >= 0 {
		// A copy: the frame under way goes through the one it was given.
		v.items = slices.Clone(v.items)
		v.items[i] = m
	}
	v.mu.Unlock()
	delete(v.thumbState, id)
	delete(v.targets, id)
	v.video.stop()
	v.zoom.reset()
	e.resume = false
	e.transition.replaced = true
	v.replaced = viewerReplaced{key: m.Key}
	if picture := e.resultPicture(); picture != nil {
		v.replaced.picture = picture
	}
}

// resultPicture is what the editor's view shows, as it comes out: cut and turned.
func (e *viewerEdit) resultPicture() *image.RGBA {
	if e.shown == nil || e.scene == nil {
		return nil
	}
	c := e.preview.Canvas()
	t := e.scene.Transform
	if e.video {
		t = t.Even()
	}
	scaled := func(v, origin int) int { return int(float64(v-origin)*c.Scale + .5) }
	crop := image.Rect(scaled(t.Crop.Min.X, c.Rect.Min.X), scaled(t.Crop.Min.Y, c.Rect.Min.Y),
		scaled(t.Crop.Max.X, c.Rect.Min.X), scaled(t.Crop.Max.Y, c.Rect.Min.Y)).Intersect(e.shown.Rect)
	if crop.Empty() {
		return nil
	}
	return photoedit.Transform{Crop: crop, Angle: t.Angle, Flip: t.Flip}.Apply(e.shown)
}

// updateSend takes the dialog's clicks and what came of the job.
func (v *mediaViewer) updateSend(gtx layout.Context, l localization.Catalog) {
	e := &v.edit
	s := &e.send
	if s.job != nil {
		select {
		case o := <-s.job.done:
			s.job = nil
			v.editOutcome(gtx, o, l)
			if !e.open {
				return
			}
		default:
		}
	}
	if !s.busy() {
		return
	}
	if s.cancel.Clicked(gtx) {
		switch s.dialog {
		case editDialogWork:
			if s.job != nil {
				s.job.stop()
			}
		case editDialogFailed:
			s.dropResult()
			s.modal.Close()
		default:
			s.modal.Close()
		}
	}
	// What failed, before a reply tried in its place says otherwise.
	failed := s.action
	if s.asReply.Clicked(gtx) && s.dialog == editDialogFailed && s.canReply {
		e.retry(v, editReply)
	}
	if s.confirm.Clicked(gtx) {
		switch s.dialog {
		case editDialogChoice:
			s.modal.Hide()
			e.start(gtx, v, s.action)
		case editDialogFailed:
			e.retry(v, failed)
		case editDialogDiscard:
			v.leaveEdit()
		}
	}
}

// editOutcome takes what came of a job: the editor closes on what was done, and
// stays on what was stopped or failed, the work not being lost with it.
func (v *mediaViewer) editOutcome(gtx layout.Context, o editOutcome, l localization.Catalog) {
	e := &v.edit
	s := &e.send
	switch {
	case o.err == nil:
		text := l.T("editor.sent")
		switch o.action {
		case editReplace:
			text = l.T("editor.replaced")
		case editSaved:
			text = l.T("editor.saved_done")
		case editAvatar:
			text = l.T("editor.avatar_done")
		}
		if o.replaced != nil {
			v.takeReplaced(*o.replaced)
		}
		if o.commit != nil {
			o.commit()
		}
		v.leaveEdit()
		v.toast.Show(text)
	case o.path != "":
		s.dir, s.path, s.failure = o.dir, o.path, o.err
		s.canReply = o.action == editReplace && v.canReplyEdit(e.item)
		s.ask(editDialogFailed)
	case errors.Is(o.err, context.Canceled):
		s.dialog = editDialogNone
		s.modal.Hide()
		e.focus = true
	default:
		s.dialog = editDialogNone
		s.modal.Hide()
		e.focus = true
		e.toast.Show(l.T("editor.failed") + ": " + editFailureText(o.err, l))
	}
	gtx.Execute(op.InvalidateCmd{})
}

// editFailureText says what a failure means.
func editFailureText(err error, l localization.Catalog) string {
	switch {
	case errors.Is(err, model.ErrEditForbidden):
		return l.T("editor.expired")
	case errors.Is(err, model.ErrProfilePhotoForbidden):
		return l.T("editor.avatar_forbidden")
	case errors.Is(err, model.ErrMediaInvalid):
		return l.T("editor.invalid_file")
	case errors.Is(err, video.ErrGIFTooLarge):
		return l.T("viewer.gif_too_large")
	case errors.Is(err, video.ErrNoH264Encoder):
		return l.T("ffmpeg.no_h264_encoder")
	}
	var ban *model.SendRestriction
	if errors.As(err, &ban) {
		return restrictionText(ban.Permissions, ban.Kind, l)
	}
	return mediaErrorText(err)
}

// layoutSend draws the dialog over the editor.
func (v *mediaViewer) layoutSend(gtx layout.Context, l localization.Catalog) {
	e := &v.edit
	s := &e.send
	if !s.modal.Shown() {
		return
	}
	if s.dialog == editDialogChoice {
		// Here, not with the clicks: Done opens the dialog after those, and
		// the radios refuse to be drawn in its first frame without it.
		s.radios.Update(gtx)
	}
	locked := s.dialog == editDialogWork
	shown := s.modal.Layout(gtx, locked, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return s.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, e.dialogRows(gtx, l)...)
		}, defaultCardPadding)
	})
	if !shown {
		// Closed: the keys are the editor's again.
		if s.dialog != editDialogWork {
			s.dialog = editDialogNone
		}
		s.modal = modal{}
		e.focus = true
	}
}

// dialogRows are what the dialog shows.
func (e *viewerEdit) dialogRows(gtx layout.Context, l localization.Catalog) []layout.FlexChild {
	s := &e.send
	sc := scheme(gtx)
	title := func(text string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, text, token.TypestyleTitleLarge, sc.Surface.OnColor, 2)
		})
	}
	about := func(text string) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, text, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		})
	}
	buttons := func(ws ...layout.Widget) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			children := make([]layout.FlexChild, len(ws))
			for i, w := range ws {
				children[i] = layout.Rigid(w)
			}
			return layout.Flex{Spacing: layout.SpaceStart}.Layout(gtx, children...)
		})
	}
	button := func(c *surface, text string) layout.Widget {
		return func(gtx layout.Context) layout.Dimensions { return textButton(gtx, c, text) }
	}
	switch s.dialog {
	case editDialogChoice:
		return []layout.FlexChild{
			title(l.T(e.titleKey())), vspace(8),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				// The labels of the actions offered, and of no other: the radios take
				// as many as they have.
				labels := make(map[editAction]string, len(s.actions))
				for _, a := range s.actions {
					labels[a] = l.T(editActionKeys[a])
				}
				return s.radios.Layout(gtx, radio.LeadingKind, labels)
			}),
			vspace(8),
			buttons(button(&s.cancel, l.T("editor.cancel")), button(&s.confirm, l.T("editor.continue"))),
		}
	case editDialogWork:
		stage, share := stagePreparing, -1.
		if s.job != nil {
			stage, share = s.job.state()
		}
		status := l.T("editor.preparing")
		switch stage {
		case stageEncoding:
			status = l.T("editor.encoding")
		case stageSending:
			status = l.T("editor.sending")
		}
		out := []layout.FlexChild{title(l.T(e.titleKey())), vspace(12)}
		out = append(out, progressRows(s.bar, status, float32(share))...)
		return append(out, vspace(8), buttons(button(&s.cancel, l.T("editor.cancel"))))
	case editDialogFailed:
		return []layout.FlexChild{
			title(l.T("editor.failed")), vspace(8), about(editFailureText(s.failure, l)), vspace(12),
			buttons(button(&s.cancel, l.T("editor.close")), func(gtx layout.Context) layout.Dimensions {
				if !s.canReply {
					return layout.Dimensions{}
				}
				return textButton(gtx, &s.asReply, l.T("editor.as_reply"))
			}, button(&s.confirm, l.T("editor.retry"))),
		}
	}
	return []layout.FlexChild{
		title(l.T("editor.discard_title")), vspace(8), about(l.T("editor.discard_about")), vspace(12),
		buttons(button(&s.cancel, l.T("editor.keep")), button(&s.confirm, l.T("editor.discard"))),
	}
}

// titleKey is the key of the dialog's title.
func (e *viewerEdit) titleKey() string {
	if e.item.Kind == model.MessageGIF {
		return "editor.title_gif"
	}
	if e.video {
		return "editor.title_video"
	}
	return "editor.title_photo"
}

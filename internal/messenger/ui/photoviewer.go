// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"fmt"
	"image"
	"image/color"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"
	"gioui.org/widget"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/chatmedia"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/resample"
)

// The viewer's background: nearly opaque over the chat, and in a window of
// its own lighter the more the compositor does to keep the photo readable
// over whatever the desktop shows.
var (
	viewerBackdrop        = color.NRGBA{R: 8, G: 9, B: 12, A: 244}
	viewerBackdropClear   = color.NRGBA{R: 8, G: 9, B: 12, A: 215}
	viewerBackdropBlurred = color.NRGBA{R: 8, G: 9, B: 12, A: 150}
)

const (
	viewerBar       = unit.Dp(56)
	viewerStrip     = unit.Dp(84)
	viewerThumb     = unit.Dp(56)
	viewerThumbGap  = unit.Dp(6)
	viewerArrow     = unit.Dp(48)
	viewerSide      = unit.Dp(72)
	viewerPage      = 60 // photos asked for per request
	viewerPrefetch  = 8  // read more when this close to a loaded end
	viewerFullLimit = 4  // decoded full-size photos: current, neighbours, the one before
	// viewerRingDelay keeps a photo that decodes quickly from flashing a
	// progress ring on the way.
	viewerRingDelay = 400 * time.Millisecond
)

// photoViewer shows a chat's photos over the whole window: the current one
// at its own size, as far as the window allows, arrows to its neighbours and
// a strip of thumbnails below. It pages the chat's photos, or the photos of
// its profile, as the strip comes near either end.
type photoViewer struct {
	source     model.ConversationStore
	gallery    model.PhotoGallery
	profiles   model.ProfilePhotoSource
	images     *imageOps
	invalidate func()
	// full decodes the photo on screen for the space it has; thumbs decodes
	// the smallest variant that covers a thumbnail; mid decodes one that
	// covers half the stage, shown while the original is on its way. A
	// chat tile has usually cached that one already.
	full, mid, thumbs *chatmedia.Manager

	open    bool
	focus   bool
	chat    int64
	current model.MessageID
	center  bool // scroll the strip to the current photo on the next frame
	// previous is the photo shown before current, kept decoded so that
	// going back is instant; since is when current was chosen.
	previous model.MessageID
	since    time.Time

	mu        sync.Mutex
	session   int
	ctx       context.Context // ends when the viewer closes
	cancel    context.CancelFunc
	items     []model.Message // sorted by message ID
	loading   [2]bool         // older, newer
	exhausted [2]bool         // nothing more is asked for on that side
	// ended tells the sides the gallery is known to end at, which, with
	// total, the count of all its photos, places the photos shown in it.
	ended [2]bool
	total int
	// profile tells that items are the photos of the chat's profile,
	// paged from offset, not its photo messages.
	profile bool
	offset  string

	backdrop, picture, prev, next, close, detach widget.Clickable
	// save and copy keep the photo on screen; kept brings what they gave,
	// and toast tells it, over the strip of photos.
	save, copy widget.Clickable
	kept       chan viewerFile
	toast      toast
	zoom       viewerZoom
	// popout, when set, is what the button beside ✕ calls to show the
	// photos in a window of their own; the viewer then closes.
	popout func(chat int64, current model.Message, known photoList)
	// standalone is set in such a window: the background does not close it.
	standalone bool
	// backdrop is the colour under the photo; a translucent window lets
	// the desktop show through it.
	backdropColor color.NRGBA
	keys          struct{}
	strip         layout.List
	drag          stripDrag
	thumbState    map[model.MessageID]*viewerThumbState
	targets       map[model.MessageID]model.Message
	// play opens a video in the external player; playErrs bring what it
	// failed with.
	play     func(gtx layout.Context, m model.Message, l localization.Catalog)
	playErrs chan error
	// drawn is the photo the last frame drew, for tests to compare with
	// current.
	drawn model.MessageID
}

type viewerThumbState struct {
	click widget.Clickable
	// msg shows the variant that suits the thumbnail size it was made for;
	// mid the one shown while the original loads.
	msg, mid model.Message
	size     int
}

// stripDrag scrolls the thumbnail strip by dragging it with a mouse. Touch
// drags and horizontal swipes are scrolled by the list itself.
type stripDrag struct {
	pressed, dragging bool
	id                pointer.ID
	last, moved, rest float32
}

// photoList is what a viewer knows of the photos it shows, for another one
// to go on from, such as the viewer of a window of their own.
type photoList struct {
	items   []model.Message
	total   int
	ended   [2]bool
	profile bool
	offset  string
}

func newPhotoViewer(source model.ConversationStore, images *imageOps, invalidate func()) *photoViewer {
	v := &photoViewer{source: source, images: images, invalidate: invalidate, thumbState: map[model.MessageID]*viewerThumbState{}, backdropColor: viewerBackdrop, playErrs: make(chan error, 1)}
	v.gallery, _ = source.(model.PhotoGallery)
	v.profiles, _ = source.(model.ProfilePhotoSource)
	v.full = chatmedia.NewSized(source, invalidate, viewerFullLimit, 4096)
	v.mid = chatmedia.NewSized(source, invalidate, 3, 1024)
	v.thumbs = chatmedia.NewSized(source, invalidate, 96, 256)
	v.strip.Axis = layout.Horizontal
	return v
}

// Open shows photo m of chat. known are photos the caller already has, such
// as those of the loaded history, so that the strip is not empty while the
// cache is read.
func (v *photoViewer) Open(chat int64, m model.Message, known []model.Message) {
	v.openList(chat, m, photoList{items: known, profile: model.IsProfilePhoto(m.Key.MessageID)})
}

// openList shows photo m of chat among what list tells of the others.
func (v *photoViewer) openList(chat int64, m model.Message, list photoList) {
	v.mu.Lock()
	if v.cancel != nil {
		v.cancel()
	}
	v.session++
	v.items = mergePhotos(nil, list.items)
	if indexOf(v.items, m.Key.MessageID) < 0 {
		v.items = append(v.items, m)
		sort.Slice(v.items, func(i, j int) bool { return v.items[i].Key.MessageID < v.items[j].Key.MessageID })
	}
	v.total, v.ended, v.profile, v.offset = list.total, list.ended, list.profile, list.offset
	pageless := v.gallery == nil || m.Key.MessageID < 0
	if v.profile {
		// The photos of a profile start at the one it shows now.
		v.ended[0] = true
		pageless = v.profiles == nil
	}
	v.loading, v.exhausted = [2]bool{}, [2]bool{pageless || v.ended[0], pageless || v.ended[1]}
	v.ctx, v.cancel = context.WithCancel(context.Background())
	ctx, session := v.ctx, v.session
	first, last := v.items[0].Key.MessageID, v.items[len(v.items)-1].Key.MessageID
	v.mu.Unlock()
	v.open, v.focus, v.center = true, true, true
	v.zoom.reset()
	v.chat, v.current = chat, m.Key.MessageID
	v.strip.Position = layout.Position{}
	clear(v.thumbState)
	clear(v.targets)
	v.fetch(ctx, session, -1, first)
	v.fetch(ctx, session, 1, last)
}

// OpenAlone shows m without a gallery, as a GIF is.
func (v *photoViewer) OpenAlone(chat int64, m model.Message) {
	v.openList(chat, m, photoList{ended: [2]bool{true, true}})
}

// OpenProfile shows the photos of chat's profile, the one it shows now
// first: at once, from what is known, and with the older ones added when
// Telegram has told them. It tells whether the store has a photo of the chat.
func (v *photoViewer) OpenProfile(chat int64) bool {
	if v.profiles == nil {
		return false
	}
	current, ok := v.profiles.ProfilePhoto(chat)
	if !ok {
		return false
	}
	// The photo shown stays as it is, already decoded: the first page's
	// photo of the same place is not merged.
	v.openList(chat, current, photoList{profile: true})
	return true
}

func (v *photoViewer) Close() {
	v.mu.Lock()
	if v.cancel != nil {
		v.cancel()
		v.cancel = nil
	}
	v.session++
	v.items, v.total, v.offset = nil, 0, ""
	v.mu.Unlock()
	v.open = false
	// A closed viewer keeps no decoded pixels.
	v.full.Clear()
	v.mid.Clear()
	v.thumbs.Clear()
	clear(v.thumbState)
}

// Destroy stops the viewer's workers for good, when its window closes.
func (v *photoViewer) Destroy() {
	v.Close()
	v.full.Close()
	v.mid.Close()
	v.thumbs.Close()
}

// Release drops the decoded photos of a hidden window; the open photo and
// its strip are decoded again when the window is shown.
func (v *photoViewer) Release() {
	v.full.Release()
	v.mid.Release()
	v.thumbs.Release()
}

// fetch reads the next gallery page past anchor in the background; the
// photos of a profile are paged from where the last page ended.
func (v *photoViewer) fetch(ctx context.Context, session, dir int, anchor model.MessageID) {
	side := (dir + 1) / 2
	v.mu.Lock()
	if v.session != session || v.loading[side] || v.exhausted[side] {
		v.mu.Unlock()
		return
	}
	v.loading[side] = true
	chat, profile, offset := v.chat, v.profile, v.offset
	v.mu.Unlock()
	go func() {
		defer crash.Recover("photo gallery", func(*crash.Panic) {
			v.mu.Lock()
			if v.session == session {
				v.loading[side], v.exhausted[side] = false, true
			}
			v.mu.Unlock()
		})
		var page model.PhotoPage
		var err error
		if profile {
			page, err = v.profiles.ProfilePhotos(ctx, chat, offset, viewerPage)
		} else {
			page, err = v.gallery.ChatPhotos(ctx, chat, anchor, dir, viewerPage)
		}
		v.mu.Lock()
		defer v.invalidate()
		defer v.mu.Unlock()
		if v.session != session {
			return
		}
		v.loading[side] = false
		if err != nil {
			// A failed page ends the strip there; photos already known stay available.
			v.exhausted[side] = true
			return
		}
		v.ended[side] = !page.More
		v.exhausted[side] = v.ended[side]
		if page.Total > 0 {
			v.total = page.Total
		}
		v.offset = page.Next
		v.items = mergePhotos(v.items, page.Messages)
	}()
}

func inGallery(m model.Message) bool {
	return (m.Kind == model.MessagePhoto || m.Kind == model.MessageVideo) && m.Media != nil
}

// viewerTarget is what the viewer decodes for m: a video's thumbnail, in the
// video's proportions; ok is false for a video without one.
func (v *photoViewer) viewerTarget(m model.Message) (model.Message, bool) {
	if m.Kind != model.MessageVideo {
		return m, m.Media != nil
	}
	if m.Media.Thumbnail == nil {
		return m, false
	}
	if t, ok := v.targets[m.Key.MessageID]; ok {
		return t, true
	}
	thumb := *m.Media.Thumbnail
	thumb.Width, thumb.Height = m.Media.Width, m.Media.Height
	if thumb.Preview == nil {
		thumb.Preview = m.Media.Preview
	}
	t := m.WithMedia(&thumb)
	t.Kind = model.MessagePhoto
	if v.targets == nil {
		v.targets = map[model.MessageID]model.Message{}
	}
	v.targets[m.Key.MessageID] = t
	return t, true
}

// mergePhotos adds photos and videos to a sorted list, once each.
func mergePhotos(items, add []model.Message) []model.Message {
	seen := make(map[model.MessageID]bool, len(items))
	for _, m := range items {
		seen[m.Key.MessageID] = true
	}
	for _, m := range add {
		if inGallery(m) && !seen[m.Key.MessageID] {
			seen[m.Key.MessageID] = true
			items = append(items, m)
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key.MessageID < items[j].Key.MessageID })
	return items
}

// place is where photo i of items is in the whole gallery, and how many
// photos it has, when that is known: counted from the end the gallery is
// known to end at, as Telegram Desktop does. Without a total from Telegram
// only a gallery read to both ends is counted.
func (v *photoViewer) place(items []model.Message, i int) (n, amount int, ok bool) {
	v.mu.Lock()
	total, ended := v.total, v.ended
	v.mu.Unlock()
	if total == 0 {
		if !ended[0] || !ended[1] {
			return 0, 0, false
		}
		total = len(items)
	}
	// Photos that came after Telegram counted them are counted too.
	amount = max(total, len(items))
	switch {
	case ended[0]:
		n = i + 1
	case ended[1]:
		n = amount - (len(items) - 1 - i)
	default:
		return 0, 0, false
	}
	return n, amount, n >= 1 && n <= amount
}

func (v *photoViewer) snapshot() (items []model.Message, loading, exhausted [2]bool, ctx context.Context, session int) {
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.items, v.loading, v.exhausted, v.ctx, v.session
}

func indexOf(items []model.Message, id model.MessageID) int {
	i := sort.Search(len(items), func(i int) bool { return items[i].Key.MessageID >= id })
	return min(i, len(items)-1)
}

// show makes photo i current. center scrolls the strip to it, which a
// click on the strip itself does not want.
func (v *photoViewer) show(items []model.Message, i int, center bool) {
	if i >= 0 && i < len(items) && items[i].Key.MessageID != v.current {
		v.previous, v.current = v.current, items[i].Key.MessageID
		v.since = time.Time{}
		v.zoom.reset()
		v.center = v.center || center
	}
}

func (v *photoViewer) Layout(gtx layout.Context, l localization.Catalog, animate bool) {
	if !v.open {
		return
	}
	v.full.BeginFrame()
	defer v.full.EndFrame()
	v.mid.BeginFrame()
	defer v.mid.EndFrame()
	v.thumbs.BeginFrame()
	defer v.thumbs.EndFrame()

	items, loading, exhausted, ctx, session := v.snapshot()
	if len(items) == 0 {
		v.Close()
		return
	}
	size := gtx.Constraints.Max
	bar, strip, side := gtx.Dp(viewerBar), gtx.Dp(viewerStrip), gtx.Dp(viewerSide)
	stage := image.Rect(side, bar, max(side, size.X-side), max(bar, size.Y-strip))
	if size.X < 3*side {
		stage.Min.X, stage.Max.X = 0, size.X
	}
	// An enlarged photo spreads under the side zones, up to the window edges.
	view := image.Rect(0, stage.Min.Y, size.X, stage.Max.Y)
	i := indexOf(items, v.current)
	v.keyEvents(gtx, items, i, native(items[i]), stage.Size())
	if !v.open {
		return
	}
	i = indexOf(items, v.current)
	v.zoomEvents(gtx, items, i, view, native(items[i]), fitScale(native(items[i]), stage.Size()))
	i = indexOf(items, v.current)
	if v.prev.Clicked(gtx) {
		v.show(items, i-1, true)
	}
	if v.next.Clicked(gtx) {
		v.show(items, i+1, true)
	}
	// Thumbnail clicks are handled here, before anything is drawn, like the
	// arrows: handled while laying out the strip, they would change the photo
	// after it was drawn, and nothing would draw it until the next input.
	for id, st := range v.thumbState {
		if st.click.Clicked(gtx) {
			v.show(items, indexOf(items, id), false)
		}
	}
	if v.backdrop.Clicked(gtx) && !v.standalone || v.close.Clicked(gtx) {
		v.Close()
		return
	}
	v.updateKept(gtx)
	select {
	case err := <-v.playErrs:
		v.toast.Show(mediaErrorText(err))
	default:
	}
	if cur := items[indexOf(items, v.current)]; canKeep(cur) {
		if v.save.Clicked(gtx) {
			v.keepPhoto(cur, false, l)
		}
		if v.copy.Clicked(gtx) {
			v.keepPhoto(cur, true, l)
		}
	}
	if v.detach.Clicked(gtx) && v.popout != nil {
		i := indexOf(items, v.current)
		v.mu.Lock()
		list := photoList{items: items, total: v.total, ended: v.ended, profile: v.profile, offset: v.offset}
		v.mu.Unlock()
		v.popout(v.chat, items[i], list)
		v.Close()
		return
	}
	i = indexOf(items, v.current)
	if v.since.IsZero() {
		v.since = gtx.Now
	}
	// Keep decoded only what the arrows lead to and the way back.
	var keep []string
	for _, j := range []int{i, i - 1, i + 1, indexOf(items, v.previous)} {
		if j >= 0 && j < len(items) {
			if t, ok := v.viewerTarget(items[j]); ok {
				keep = append(keep, t.Media.ID)
			}
		}
	}
	v.full.Retain(keep...)
	// Read further into the cache before the strip runs out.
	if i < viewerPrefetch && !exhausted[0] && !loading[0] {
		v.fetch(ctx, session, -1, items[0].Key.MessageID)
	}
	if len(items)-1-i < viewerPrefetch && !exhausted[1] && !loading[1] {
		v.fetch(ctx, session, 1, items[len(items)-1].Key.MessageID)
	}

	photo := native(items[i])
	fit := fitScale(photo, stage.Size())
	if v.zoom.step(gtx.Now, animate, fit, photo, view.Size()) {
		gtx.Execute(op.InvalidateCmd{})
	}

	area := clip.Rect{Max: size}.Push(gtx.Ops)
	defer area.Pop()
	event.Op(gtx.Ops, &v.keys)
	// Everything under the viewer is covered and gets no input.
	v.backdrop.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		paint.Fill(gtx.Ops, v.backdropColor)
		return layout.Dimensions{Size: size}
	})

	v.layoutPhoto(gtx, items[i], stage, view, fit, l, animate)
	captionTop := v.layoutCaption(gtx, items[i], stage)
	v.layoutSides(gtx, view, i > 0, i < len(items)-1)
	// Over the side zones, so that the wheel works there too; it passes
	// clicks on to them.
	v.zoomArea(gtx, view, photo)
	v.layoutBar(gtx, items, i, l)
	v.layoutStrip(gtx, items, i, image.Rect(0, size.Y-strip, size.X, size.Y))
	v.toast.Layout(gtx, image.Rect(0, bar, size.X, min(size.Y-strip, captionTop)))
	// Decode the neighbours ahead, so that the arrows switch at once.
	for _, j := range []int{i - 1, i + 1} {
		if j >= 0 && j < len(items) {
			if t, ok := v.viewerTarget(items[j]); ok {
				v.full.StatusFit(t, false, stage.Size(), false)
			}
		}
	}
}

func (v *photoViewer) keyEvents(gtx layout.Context, items []model.Message, i int, photo, stage image.Point) {
	if v.focus {
		gtx.Execute(key.FocusCmd{Tag: &v.keys})
		v.focus = false
	}
	for {
		ev, ok := gtx.Event(
			key.FocusFilter{Target: &v.keys},
			key.Filter{Focus: &v.keys, Name: key.NameLeftArrow},
			key.Filter{Focus: &v.keys, Name: key.NameRightArrow},
			key.Filter{Focus: &v.keys, Name: key.NameHome},
			key.Filter{Focus: &v.keys, Name: key.NameEnd},
			key.Filter{Focus: &v.keys, Name: key.NameEscape},
			key.Filter{Focus: &v.keys, Name: "=", Required: key.ModShortcut, Optional: key.ModShift},
			key.Filter{Focus: &v.keys, Name: "+", Required: key.ModShortcut, Optional: key.ModShift},
			key.Filter{Focus: &v.keys, Name: "-", Required: key.ModShortcut},
			key.Filter{Focus: &v.keys, Name: "0", Required: key.ModShortcut},
			// Copy and Save, as Telegram Desktop's viewer: also on a
			// Russian layout.
			key.Filter{Focus: &v.keys, Name: "C", Required: key.ModShortcut},
			key.Filter{Focus: &v.keys, Name: "С", Required: key.ModShortcut},
			key.Filter{Focus: &v.keys, Name: "S", Required: key.ModShortcut},
			key.Filter{Focus: &v.keys, Name: "Ы", Required: key.ModShortcut},
		)
		if !ok {
			return
		}
		e, ok := ev.(key.Event)
		if !ok || e.State != key.Press {
			continue
		}
		switch e.Name {
		case key.NameLeftArrow:
			v.show(items, i-1, true)
		case key.NameRightArrow:
			v.show(items, i+1, true)
		case key.NameHome:
			v.show(items, 0, true)
		case key.NameEnd:
			v.show(items, len(items)-1, true)
		case key.NameEscape:
			v.Close()
			gtx.Execute(key.FocusCmd{})
			return
		case "=", "+":
			if photo != (image.Point{}) {
				v.zoom.zoomBy(zoomStep, f32.Point{}, fitScale(photo, stage))
			}
		case "-":
			if v.zoom.active() {
				v.zoom.zoomBy(1/zoomStep, f32.Point{}, fitScale(photo, stage))
			}
		case "0":
			v.zoom.unzoom(fitScale(photo, stage))
		case "C", "С":
			v.copy.Click()
		case "S", "Ы":
			v.save.Click()
		}
		i = indexOf(items, v.current)
	}
}

// layoutPhoto draws the current photo centered in stage, never larger than
// its own pixels. Until it is decoded, its thumbnail or blurred preview is
// stretched to the same place.
func (v *photoViewer) layoutPhoto(gtx layout.Context, item model.Message, stage, view image.Rectangle, fit float32, l localization.Catalog, animate bool) {
	v.drawn = item.Key.MessageID
	video := item.Kind == model.MessageVideo
	m, decodable := v.viewerTarget(item)
	box := stage.Size()
	if v.zoom.active() && v.zoom.target > fit {
		// Enlarged, the photo is decoded at its own size; the fitted frame
		// stays on screen until then.
		box = native(m)
	}
	var status chatmedia.Status
	if decodable {
		status = v.full.StatusFit(m, animate, box, false)
	}
	w, h := m.Media.Width, m.Media.Height
	if status.Frame != nil && (w <= 0 || h <= 0) {
		b := status.Frame.Bounds()
		w, h = b.Dx(), b.Dy()
	}
	if w <= 0 || h <= 0 {
		w, h = 4, 3
	}
	shown := resample.Fit(w, h, stage.Size(), false)
	if m.Media.Width <= 0 && status.Frame == nil {
		// Unknown size: reserve the stage's width in a 4:3 frame.
		shown = resample.Fit(4000, 3000, stage.Size(), false)
	}
	origin := stage.Min.Add(stage.Size().Sub(shown).Div(2))
	if v.zoom.active() {
		s := v.zoom.scale
		shown = image.Pt(max(1, int(math.Round(float64(float32(w)*s)))), max(1, int(math.Round(float64(float32(h)*s)))))
		center := layout.FPt(view.Min.Add(view.Max)).Mul(.5).Add(v.zoom.offset)
		origin = image.Pt(int(math.Round(float64(center.X)-float64(shown.X)/2)), int(math.Round(float64(center.Y)-float64(shown.Y)/2)))
	}
	defer clip.Rect(view).Push(gtx.Ops).Pop()
	im := status.Frame
	if im == nil && decodable {
		// Half the stage in a smaller variant, decoded in a few milliseconds
		// and usually cached by the chat tile, then the thumbnail, then the
		// blurred preview.
		half := stage.Size().Div(2)
		if mid := m.Media.Variant(half.X, half.Y); mid != m.Media {
			im = v.mid.StatusFit(v.variant(m, mid), false, half, false).Frame
		}
	}
	if im == nil && decodable {
		if t := v.thumb(m, gtx.Dp(viewerThumb)); t != nil {
			im = v.thumbs.StatusFit(*t, false, image.Pt(gtx.Dp(viewerThumb), gtx.Dp(viewerThumb)), true).Frame
		}
	}
	if im == nil {
		im = status.Preview
	}
	showRing := status.Err != nil
	if status.Loading {
		if wait := v.since.Add(viewerRingDelay); gtx.Now.Before(wait) {
			gtx.Execute(op.InvalidateCmd{At: wait})
		} else {
			showRing = true
		}
	}
	if v.picture.Clicked(gtx) {
		switch {
		case video && v.play != nil:
			v.play(gtx, item, l)
		case status.Err != nil || status.Cancelled:
			v.full.Retry(m)
		}
	}
	offset(gtx, origin, func(gtx layout.Context) layout.Dimensions {
		return v.picture.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			if video && v.play != nil || status.Err != nil || status.Cancelled {
				// A press plays the video, or loads the photo again.
				pointer.CursorPointer.Add(gtx.Ops)
			}
			gtx.Constraints = layout.Exact(shown)
			if im == nil {
				fillRect(gtx, token.NewMatColorFromHexRGB(0x1d2127), shown)
			} else {
				widget.Image{Src: v.images.Op(im), Fit: widget.Fill}.Layout(gtx)
			}
			if video && !showRing {
				d := min(gtx.Dp(64), shown.X, shown.Y)
				offset(gtx, shown.Sub(image.Pt(d, d)).Div(2), func(gtx layout.Context) layout.Dimensions {
					paint.FillShape(gtx.Ops, color.NRGBA{A: 150}, clip.Ellipse{Max: image.Pt(d, d)}.Op(gtx.Ops))
					inner := d * 3 / 4
					return offset(gtx, image.Pt((d-inner)/2, (d-inner)/2), func(gtx layout.Context) layout.Dimensions {
						return exact(gtx, image.Pt(inner, inner), func(gtx layout.Context) layout.Dimensions {
							return iconPlayFile(gtx, token.NewMatColorFromHexRGB(0xffffff))
						})
					})
				})
			}
			if showRing {
				diameter := min(gtx.Dp(56), shown.X, shown.Y)
				offset(gtx, shown.Sub(image.Pt(diameter, diameter)).Div(2), func(gtx layout.Context) layout.Dimensions {
					paint.FillShape(gtx.Ops, color.NRGBA{A: 150}, clip.Ellipse{Max: image.Pt(diameter, diameter)}.Op(gtx.Ops))
					if status.Loading {
						progress := float32(0)
						if status.Total > 0 {
							progress = min(1, float32(status.Downloaded)/float32(status.Total))
						}
						ring(gtx, diameter, progress, animate)
					}
					return layout.Dimensions{Size: image.Pt(diameter, diameter)}
				})
			}
			if status.Err != nil {
				errGtx := gtx
				errGtx.Constraints = layout.Constraints{Max: image.Pt(shown.X, shown.Y/2)}
				offset(errGtx, image.Pt(0, shown.Y/2+gtx.Dp(36)), func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return centeredLabel(gtx, l.T("viewer.failed"), token.TypestyleLabelLarge, token.NewMatColorFromHexRGB(0xffffff), 2)
				})
			}
			return layout.Dimensions{Size: shown}
		})
	})
}

// reportPlay tells the viewer why a video did not play; it may be called
// from any goroutine.
func (v *photoViewer) reportPlay(err error) {
	if err == nil {
		return
	}
	select {
	case v.playErrs <- err:
		v.invalidate()
	default:
	}
}

// layoutCaption draws m's caption over the bottom of the stage, and returns
// where its top is.
func (v *photoViewer) layoutCaption(gtx layout.Context, m model.Message, stage image.Rectangle) int {
	text := strings.TrimSpace(m.Text)
	if text == "" {
		return math.MaxInt
	}
	pad, margin := gtx.Dp(12), gtx.Dp(12)
	gtx.Constraints = layout.Constraints{Max: image.Pt(max(0, min(stage.Dx()-2*margin, gtx.Dp(640))-2*pad), stage.Dy()/3)}
	macro := op.Record(gtx.Ops)
	dims := label(gtx, text, token.TypestyleBodyLarge, token.NewMatColorFromHexRGB(0xffffff), 4)
	call := macro.Stop()
	size := dims.Size.Add(image.Pt(2*pad, 2*gtx.Dp(8)))
	at := image.Pt(stage.Min.X+(stage.Dx()-size.X)/2, stage.Max.Y-margin-size.Y)
	offset(gtx, at, func(gtx layout.Context) layout.Dimensions {
		paint.FillShape(gtx.Ops, color.NRGBA{A: 150}, clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(8)).Op(gtx.Ops))
		offset(gtx, image.Pt(pad, gtx.Dp(8)), func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return dims
		})
		return layout.Dimensions{Size: size}
	})
	return at.Y
}

// layoutSides lays out the zones beside the photo that switch to its
// neighbours: the whole height of the stage, not only the arrow drawn in it,
// is the target.
func (v *photoViewer) layoutSides(gtx layout.Context, view image.Rectangle, prev, next bool) {
	w := gtx.Dp(viewerSide)
	if prev {
		sideZone(gtx, &v.prev, image.Rect(view.Min.X, view.Min.Y, view.Min.X+w, view.Max.Y), iconChevronLeft)
	}
	if next {
		sideZone(gtx, &v.next, image.Rect(view.Max.X-w, view.Min.Y, view.Max.X, view.Max.Y), iconChevron)
	}
}

func sideZone(gtx layout.Context, c *widget.Clickable, r image.Rectangle, icon wdk.IconWidget) {
	offset(gtx, r.Min, func(gtx layout.Context) layout.Dimensions {
		size := r.Size()
		gtx.Constraints = layout.Exact(size)
		return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			pointer.CursorPointer.Add(gtx.Ops)
			if c.Hovered() {
				paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 10}, clip.Rect{Max: size}.Op())
			}
			d := gtx.Dp(viewerArrow)
			offset(gtx, size.Sub(image.Pt(d, d)).Div(2), func(gtx layout.Context) layout.Dimensions {
				return viewerIcon(gtx, c.Hovered(), icon, d)
			})
			return layout.Dimensions{Size: size}
		})
	})
}

// viewerIcon draws a round translucent button face that stays visible over
// any photo.
func viewerIcon(gtx layout.Context, hovered bool, icon wdk.IconWidget, d int) layout.Dimensions {
	size := image.Pt(d, d)
	alpha := uint8(110)
	if hovered {
		alpha = 170
	}
	paint.FillShape(gtx.Ops, color.NRGBA{R: 40, G: 44, B: 52, A: alpha}, clip.Ellipse{Max: size}.Op(gtx.Ops))
	inner := d * 3 / 5
	offset(gtx, size.Sub(image.Pt(inner, inner)).Div(2), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(inner, inner))
		return icon(gtx, token.NewMatColorFromHexRGB(0xffffff))
	})
	return layout.Dimensions{Size: size}
}

func viewerButton(gtx layout.Context, c *widget.Clickable, icon wdk.IconWidget, d int) layout.Dimensions {
	return c.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		return viewerIcon(gtx, c.Hovered(), icon, d)
	})
}

func (v *photoViewer) layoutBar(gtx layout.Context, items []model.Message, i int, l localization.Catalog) {
	bar := gtx.Dp(viewerBar)
	white := token.NewMatColorFromHexRGB(0xffffff)
	dim := token.NewMatColorFromHexRGB(0xb8bec8)
	m := items[i]
	d := gtx.Dp(40)
	barGtx := gtx
	barGtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, bar))
	layout.Inset{Left: 20, Right: 12}.Layout(barGtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min.Y = gtx.Constraints.Max.Y
				return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					// The column takes its own height, so that W centres it.
					gtx.Constraints.Min = image.Point{}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							title := l.T("viewer.photo")
							switch m.Kind {
							case model.MessageVideo:
								title = l.T("viewer.video")
							case model.MessageGIF:
								title = "GIF"
							}
							if n, amount, ok := v.place(items, i); ok && m.Kind != model.MessageGIF {
								title = l.Format("viewer.position", map[string]string{"n": fmt.Sprint(n), "amount": fmt.Sprint(amount)})
							}
							return label(gtx, title, token.TypestyleTitleSmall, white, 1)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							// A profile photo known only locally has no date yet.
							txt := m.SenderName
							if !m.Date.IsZero() {
								if txt != "" {
									txt += " · "
								}
								txt += m.Date.Local().Format("02.01.2006 15:04")
							}
							return label(gtx, txt, token.TypestyleLabelMedium, dim, 1)
						}),
					)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !canKeep(m) {
					return layout.Dimensions{}
				}
				return layout.Inset{Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return viewerButton(gtx, &v.copy, iconCopy, d)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !canKeep(m) {
					return layout.Dimensions{}
				}
				return layout.Inset{Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return viewerButton(gtx, &v.save, iconDownload, d)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if v.popout == nil {
					return layout.Dimensions{}
				}
				return layout.Inset{Right: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return viewerButton(gtx, &v.detach, iconOpenInNew, d)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return viewerButton(gtx, &v.close, iconClear, d) }),
		)
	})
}

// variant returns m showing variant, with the original's blurred preview.
// The copy is kept with the thumbnail state, so drawing does not allocate.
func (v *photoViewer) variant(m model.Message, variant *model.MessageMedia) model.Message {
	st := v.thumbState[m.Key.MessageID]
	if st == nil {
		st = &viewerThumbState{}
		v.thumbState[m.Key.MessageID] = st
	}
	if st.mid.Media == nil || st.mid.Media.ID != variant.ID {
		copy := *variant
		if copy.Preview == nil {
			copy.Preview = m.Media.Preview
		}
		st.mid = m.WithMedia(&copy)
	}
	return st.mid
}

// thumb returns the message showing the variant of m that covers a square
// thumbnail of side pixels, cached per photo so that frames do not allocate.
func (v *photoViewer) thumb(m model.Message, side int) *model.Message {
	st := v.thumbState[m.Key.MessageID]
	if st == nil {
		st = &viewerThumbState{}
		v.thumbState[m.Key.MessageID] = st
	}
	if st.size != side || st.msg.Media == nil {
		variant := *m.Media.Variant(side, side)
		if variant.Preview == nil {
			variant.Preview = m.Media.Preview
		}
		st.msg, st.size = m.WithMedia(&variant), side
	}
	return &st.msg
}

func (v *photoViewer) layoutStrip(gtx layout.Context, items []model.Message, current int, area image.Rectangle) {
	side, gap := gtx.Dp(viewerThumb), gtx.Dp(viewerThumbGap)
	pitch := side + gap
	width := min(area.Dx(), len(items)*pitch)
	x := area.Min.X + (area.Dx()-width)/2
	y := area.Min.Y + (area.Dy()-side)/2
	visible := max(1, width/pitch)
	if v.center {
		v.strip.Position.First = max(0, min(current-visible/2, len(items)-visible))
		v.strip.Position.Offset = 0
		v.center = false
	}
	stripGtx := gtx
	stripGtx.Constraints = layout.Exact(image.Pt(width, side))
	offset(stripGtx, image.Pt(x, y), func(gtx layout.Context) layout.Dimensions {
		dims := v.strip.Layout(gtx, len(items), func(gtx layout.Context, i int) layout.Dimensions {
			m := items[i]
			var t *model.Message
			if target, ok := v.viewerTarget(m); ok {
				t = v.thumb(target, side)
			}
			st := v.thumbState[m.Key.MessageID]
			return layout.Inset{Right: unit.Dp(float32(gap) / gtx.Metric.PxPerDp)}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return st.click.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					pointer.CursorPointer.Add(gtx.Ops)
					size := image.Pt(side, side)
					gtx.Constraints = layout.Exact(size)
					defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(6)).Push(gtx.Ops).Pop()
					fillRect(gtx, token.NewMatColorFromHexRGB(0x2a2f37), size)
					status := v.thumbs.StatusFit(*t, false, size, true)
					im := status.Frame
					if im == nil {
						im = status.Preview
					}
					if im != nil {
						if i != current {
							defer paint.PushOpacity(gtx.Ops, .55).Pop()
						}
						widget.Image{Src: v.images.Op(im), Fit: widget.Cover}.Layout(gtx)
					}
					if i == current {
						w := float32(gtx.Dp(2))
						paint.FillShape(gtx.Ops, color.NRGBA{R: 255, G: 255, B: 255, A: 230}, clip.Stroke{Path: clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(6)).Path(gtx.Ops), Width: w}.Op())
					}
					return layout.Dimensions{Size: size}
				})
			})
		})
		v.dragStrip(gtx, width)
		return dims
	})
}

// dragStrip lets a mouse drag the strip and a vertical wheel scroll it. It
// sits over the thumbnails but passes the pointer on, so a click still
// selects one; once the pointer moves far enough to be a drag, it takes the
// pointer and the thumbnail under it sees a cancelled press, not a click.
func (v *photoViewer) dragStrip(gtx layout.Context, width int) {
	dragHorizontalStrip(gtx, width, &v.drag, &v.strip, v.invalidate)
}
func (v *photoViewer) scrollStrip(px float32) {
	scrollHorizontalStrip(&v.drag, &v.strip, v.invalidate, px)
}

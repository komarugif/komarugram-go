// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gio-mw/exp/powersave"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/gpu/headless"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/miniapp"
)

// Both the thumbnail and gallery show the same square demo photo.
type viewerAvatarRenderStore struct{ *mockstore.Store }

func (s viewerAvatarRenderStore) ProfilePhoto(id int64) (model.Message, bool) {
	m, ok := s.Store.ProfilePhoto(3)
	m.Key.ChatID = id
	return m, ok
}

func (s viewerAvatarRenderStore) Avatar(id int64) (model.Message, bool) { return s.ProfilePhoto(id) }

func (s viewerAvatarRenderStore) PeerStoryRing(int64) (model.StoryPeer, bool) {
	return model.StoryPeer{}, false
}

// TestRenderViewerOpening clicks the demo's album or avatar and saves the
// source, travel, fade and the open viewer on the same frame clock.
func TestRenderViewerOpening(t *testing.T) {
	dir := os.Getenv("VIEWER_OPEN_PNG_DIR")
	if dir == "" {
		t.Skip("set VIEWER_OPEN_PNG_DIR to render")
	}
	skipFrames(t)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	size := image.Pt(960, 740)
	window, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Fatal(err)
	}
	defer window.Release()
	w := &appwindow.Window{Motion: motion.New(func() {})}
	defer w.Motion.Close()
	w.Motion.SetMode(powersave.ModeOn)
	epoch := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	now := epoch
	store := mockstore.New(now, 0)
	avatar := os.Getenv("VIEWER_OPEN_AVATAR")
	var source model.Store = store
	if avatar != "" {
		source = viewerAvatarRenderStore{store}
	}
	a := New(w, source, Services{MiniApps: miniappprefs.New(miniapp.Ephemeral)})
	defer a.Close()
	album, ok := store.Landmark("album")
	if !ok {
		t.Fatal("demo has no album")
	}
	switch avatar {
	case "":
		a.OpenMessage(album.ChatID, album.MessageID)
	case "header", "info":
		a.OpenChat(album.ChatID)
		if avatar == "info" {
			chat, _ := chatByID(store.Chats(), album.ChatID)
			a.info.Open(chat)
		}
	case "forum":
		a.OpenChat(mockstore.DemoForum)
	case "profile":
		a.section = section{kind: sectionProfile}
		a.profile.open(store.Me())
	default:
		t.Fatalf("unknown VIEWER_OPEN_AVATAR mode %q", avatar)
	}

	var router input.Router
	var ops op.Ops
	metric := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	theme := testTheme(layout.Context{Metric: metric}, appColors{}.scheme(a.dark()))
	frame := func(at time.Time) {
		ops.Reset()
		gtx := layout.Context{Ops: &ops, Source: router.Source(), Now: at, Constraints: layout.Exact(size), Metric: metric, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, theme)
		a.Update(gtx)
		a.Layout(gtx)
		router.Frame(&ops)
	}
	save := func(name string) {
		t.Helper()
		if err := window.Frame(&ops); err != nil {
			t.Fatal(err)
		}
		img := image.NewRGBA(image.Rectangle{Max: size})
		if err := window.Screenshot(img); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(filepath.Join(dir, name+".png"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, img); err != nil {
			t.Fatal(err)
		}
	}
	frame(now)
	now = now.Add(2 * time.Second)
	// The lower-right image is cropped in the album.
	tilePos := f32.Pt(850, 475)
	if avatar == "" {
		waitFor(t, "album thumbnails", func() { frame(now) }, func() bool {
			row := a.history.rows[album.MessageID]
			_, pixels := a.history.media.Stats()
			loading := a.history.media.LoadingSeen()
			return row != nil && len(row.album) > 1 && pixels > 0 && loading == 0
		})
	} else {
		frame(now)
		waitFor(t, "avatar", func() { frame(now) }, func() bool {
			_, pixels := a.avatars.media.Stats()
			return pixels > 0
		})
		switch avatar {
		case "profile":
			p := a.profile.info
			tilePos = layout.FPt(p.cover.avatar.Min.Add(p.cover.avatar.Size().Div(2)).Add(a.drop.area.Min).Add(image.Pt(p.margin, 0)))
		case "info":
			p := a.info
			tilePos = layout.FPt(p.cover.avatar.Min.Add(p.cover.avatar.Size().Div(2)).Add(image.Pt((size.X-640)/2, (size.Y-740)/2)))
		default:
			tilePos = layout.FPt(a.drop.area.Min.Add(image.Pt(36, 28)))
		}
	}
	save("01-chat")
	click := func(pos f32.Point, at, release time.Time) {
		stamp := at.Sub(epoch)
		router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Time: stamp})
		frame(at)
		router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: pos, Time: stamp})
		frame(at)
		router.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos, Time: stamp})
		frame(at)
		router.Queue(pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pos, Time: release.Sub(epoch) + time.Millisecond})
		frame(release)
	}
	click(tilePos, now, now)
	v := a.viewer
	if !v.open || v.opening.source == nil || v.opening.tile.picture == nil {
		t.Fatalf("click at %v did not open the viewer with the source thumbnail: open=%t source=%t picture=%t gallery=%v releases=%v", tilePos, v.open, v.opening.source != nil, v.opening.tile.picture != nil, v.gallery, v.openPointer.releases)
	}
	if avatar != "" && (!v.opening.tile.scaleCorners || v.opening.tile.shape.NW == 0) {
		t.Fatal("the avatar opening did not start with rounded corners")
	}
	save("02-start")
	frame(now.Add(80 * time.Millisecond))
	save("03-travel")
	frame(now.Add(viewerOpenMove))
	save("04-arrival")
	frame(now.Add(viewerOpenMove + 35*time.Millisecond))
	save("05-fade")
	frame(now.Add(viewerOpenMove + 80*time.Millisecond))
	save("05-fade-late")
	frame(now.Add(viewerOpenMove + viewerOpenFade))
	save("06-open")

	router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	frame(now.Add(time.Second))
	if v.open {
		t.Fatal("Escape did not close the viewer immediately")
	}
	save("07-closed")

	for _, interrupted := range []struct {
		name    string
		at      time.Duration
		clickAt f32.Point
	}{
		{"08-interrupted-travel", 80 * time.Millisecond, f32.Point{}},
		{"08-interrupted-backdrop", 160 * time.Millisecond, f32.Pt(30, 480)},
		{"08-interrupted-fade", viewerOpenMove + 35*time.Millisecond, f32.Pt(float32(size.X-32), float32(viewerBar/2))},
	} {
		now = now.Add(2 * time.Second)
		click(tilePos, now, now)
		before := now.Add(interrupted.at)
		frame(before)
		if !v.open || v.opening.source == nil {
			t.Fatal("click did not reopen the viewer from the tile")
		}
		save(interrupted.name + "-before")
		dismissedAt := before.Add(20 * time.Millisecond)
		if interrupted.clickAt == (f32.Point{}) {
			router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
			frame(dismissedAt)
		} else {
			click(interrupted.clickAt, before, dismissedAt)
		}
		if !v.open || !v.opening.returning() {
			t.Fatalf("%s did not start a return at %v", interrupted.name, interrupted.at)
		}
		if got := v.opening.elapsed(dismissedAt); got != interrupted.at {
			t.Fatalf("return started at %v, last frame was at %v", got, interrupted.at)
		}
		save(interrupted.name + "-start")
		frame(dismissedAt.Add(interrupted.at - 35*time.Millisecond))
		if !v.open || !v.opening.returning() {
			t.Fatal("viewer closed before the picture reached its tile")
		}
		save(interrupted.name + "-return")
		frame(dismissedAt.Add(interrupted.at))
		if v.open {
			t.Fatal("viewer stayed open after the return")
		}
		save(interrupted.name + "-closed")
	}

	w.Motion.SetMode(powersave.ModeOff)
	now = now.Add(time.Second)
	frame(now)
	click(tilePos, now, now)
	if !v.open || !v.opening.start.IsZero() {
		t.Fatal("viewer did not open immediately with animations off")
	}
	save("09-animations-off")
}

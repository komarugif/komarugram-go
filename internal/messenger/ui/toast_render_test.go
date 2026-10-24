// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/miniapp"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/exp"
	"gio-mw/exp/appearance"
	"gio-mw/wdk"

	"gioui.org/app"
	"gioui.org/gpu/headless"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// TestRenderToasts draws a toast in every place that has one, in the light
// and the dark theme, into TOAST_PNG_DIR:
//
//	TOAST_PNG_DIR=/tmp/toasts go test ./internal/messenger/ui -run RenderToasts
func TestRenderToasts(t *testing.T) {
	dir := os.Getenv("TOAST_PNG_DIR")
	if dir == "" {
		t.Skip("set TOAST_PNG_DIR to a directory")
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// From the root, where the demo's media are.
	t.Chdir("../../..")
	l := localization.For("ru")
	for _, dark := range []bool{false, true} {
		theme := "light"
		if dark {
			theme = "dark"
		}
		name := func(scene string) string { return filepath.Join(dir, scene+"-"+theme+".png") }

		// The history, over the floating and the classic composer.
		for _, classic := range []bool{false, true} {
			p := newChatPage(mockstore.New(time.Now(), 0), func() {})
			p.images = &imageOps{}
			p.classic = func() bool { return classic }
			p.toast.Show("Не удалось отправить сообщение: нет соединения с Telegram")
			scene := "history-floating"
			if classic {
				scene = "history-classic"
			}
			renderToast(t, name(scene), image.Pt(720, 640), dark, func(gtx layout.Context) {
				p.images.BeginFrame()
				p.media.BeginFrame()
				p.Layout(gtx, model.Chat{ID: 2}, l, false)
				p.layoutDialogs(gtx, l)
				p.media.EndFrame()
				p.images.EndFrame()
			})
			p.Close()
		}

		// The picker, which has its own at its bottom.
		p := newChatPage(mockstore.New(time.Now(), 0), func() {})
		p.images = &imageOps{}
		// The composer is on chat 2 already: a change of chat closes the
		// picker.
		p.composer.chat = 2
		p.composer.pickerOpen = true
		p.composer.tab = model.PickerStickers
		p.composer.page, _ = p.composer.source.Picker(context.Background(), model.PickerRequest{Tab: model.PickerStickers})
		p.composer.pickerToast.Show("Не удалось загрузить стикеры: нет соединения")
		renderToast(t, name("picker"), image.Pt(720, 700), dark, func(gtx layout.Context) {
			p.images.BeginFrame()
			p.media.BeginFrame()
			p.Layout(gtx, model.Chat{ID: 2}, l, false)
			p.media.EndFrame()
			p.images.EndFrame()
		})

		// A dialog: under it when there is room, over its bottom when not.
		p.composer.pickerOpen = false
		p.stickers.pack = &model.StickerSet{Title: "Коты и собаки", Count: 8, Items: []model.PickerItem{
			{Emoji: "🐈"}, {Emoji: "🐕"}, {Emoji: "🐩"}, {Emoji: "🐱"}, {Emoji: "🐶"}, {Emoji: "🦊"}, {Emoji: "🐻"}, {Emoji: "🐼"},
		}}
		for scene, size := range map[string]image.Point{"dialog-below": image.Pt(720, 640), "dialog-inside": image.Pt(720, 420)} {
			p.stickers.modal.Open()
			p.stickers.modal.Toast("Архив сохранён: /home/user/Загрузки/Коты и собаки.zip")
			renderToast(t, name(scene), size, dark, func(gtx layout.Context) {
				p.images.BeginFrame()
				p.media.BeginFrame()
				p.stickers.layout(gtx, p, l)
				p.media.EndFrame()
				p.images.EndFrame()
			})
		}
		p.Close()

		// What the chat list tells: over the window with no chat open, and
		// in the chat open.
		store := mockstore.New(time.Now(), 0)
		for _, scene := range []string{"window", "window-chat"} {
			w := &appwindow.Window{Window: new(app.Window), Motion: motion.New(func() {})}
			a := New(w, store, Services{})
			if scene == "window-chat" {
				a.open(chatPick{ID: 2})
			}
			a.chats.toast.Show("Не удалось закрепить чат: превышен лимит закреплённых чатов, открепите какой-нибудь из них")
			renderToast(t, name(scene), image.Pt(1100, 640), dark, func(gtx layout.Context) {
				a.Update(gtx)
				a.Layout(gtx)
			})
			a.Close()
			w.Motion.Close()
		}

		// A settings page, and the profile, which share scrollPage.
		settings := newSettingsPage(motion.New(func() {}), miniappprefs.New(miniapp.Ephemeral), nil, func() {}, nil,
			func() string { return "" }, themeAuto, "ru", func(themeMode) {}, func(string) {})
		settings.images = &imageOps{}
		settings.toast.Show("Не удалось загрузить устройства: нет соединения")
		renderToast(t, name("settings"), image.Pt(900, 640), dark, func(gtx layout.Context) {
			settings.images.BeginFrame()
			settings.Update(gtx, themeAuto, "ru")
			settings.Layout(gtx, themeAuto, appearance.Light, dark, l)
			settings.images.EndFrame()
		})

		// The media viewer, over its strip.
		var start model.Message
		for _, m := range store.History(2).Messages {
			if m.Kind == model.MessagePhoto && m.Media != nil && len(m.Media.Variants) > 0 {
				start = m
			}
		}
		images := &imageOps{}
		v := newMediaViewer(store, images, func() {})
		v.popout = func(int64, model.Message, mediaList) {}
		v.Open(2, start, nil)
		v.toast.Show("Фото сохранено: /home/user/Изображения/photo.jpg")
		renderToast(t, name("photo-viewer"), image.Pt(1100, 720), dark, func(gtx layout.Context) {
			images.BeginFrame()
			v.Layout(gtx, l, false)
			images.EndFrame()
		})
		v.Destroy()
	}
}

// renderToast draws frames for a second and a half of real time, so that
// media decode and the toast fades in, and saves the last one.
func renderToast(t *testing.T, path string, size image.Point, dark bool, draw func(gtx layout.Context)) {
	t.Helper()
	win, err := headless.NewWindow(size.X, size.Y)
	if err != nil {
		t.Fatal(err)
	}
	defer win.Release()
	ops := new(op.Ops)
	for end := time.Now().Add(1500 * time.Millisecond); time.Now().Before(end); time.Sleep(30 * time.Millisecond) {
		ops.Reset()
		gtx := layout.Context{Ops: ops, Now: time.Now(), Constraints: layout.Exact(size), Metric: unit.Metric{PxPerDp: 1.25, PxPerSp: 1.25}, Values: map[string]any{}}
		s := schemes.SchemeBaselineLight()
		if dark {
			s = schemes.SchemeBaselineDark()
		}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, s))
		exp.Background(gtx)
		draw(gtx)
	}
	if err := win.Frame(ops); err != nil {
		t.Fatal(err)
	}
	img := image.NewRGBA(image.Rectangle{Max: size})
	if err := win.Screenshot(img); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
}

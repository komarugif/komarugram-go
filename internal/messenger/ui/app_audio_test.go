// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"os"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/miniappprefs"
	"komarugram/internal/motion"
	"komarugram/pkg/audio"
	"komarugram/pkg/miniapp"
)

// What plays has its bar over the settings and the profile too, and a
// click on it there goes back to the chat at the message.
func TestAudioBarOverOtherPages(t *testing.T) {
	w := &appwindow.Window{Motion: motion.New(func() {})}
	defer w.Motion.Close()
	store := mockstore.New(time.Now(), 0)
	a := New(w, store, Services{MiniApps: miniappprefs.New(miniapp.Shared)})
	t.Cleanup(a.Close)
	a.history.audio.play = func(src audio.Source) (audioPlayback, error) { return &silentPlayback{src: src, playing: true}, nil }
	var voice model.Message
	for _, m := range store.History(2).Messages {
		if m.Kind == model.MessageVoice {
			voice = m
			break
		}
	}
	a.history.chat = 2
	a.history.audio.toggle(a.history, voice, -1)
	waitAudio(t, "it did not start", func() bool { return a.history.audio.state(voice).playing })

	var page image.Point
	frame := func(gtx layout.Context) {
		a.withAudioBar(gtx, a.catalog(), func(gtx layout.Context) layout.Dimensions {
			page = gtx.Constraints.Max
			return a.settings.Layout(gtx, a.themeMode(), a.window.Appearance.Scheme(), a.dark(), a.catalog())
		})
	}
	if path := os.Getenv("AWAY_PNG"); path != "" {
		renderFrames(t, image.Pt(700, 500), path, frame)
	}
	gtx := layout.Context{Ops: new(op.Ops), Constraints: layout.Exact(image.Pt(700, 500)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	a.withAudioBar(gtx, a.catalog(), func(gtx layout.Context) layout.Dimensions {
		page = gtx.Constraints.Max
		return layout.Dimensions{Size: page}
	})
	if page.Y != 500-48 {
		t.Fatalf("the page under the bar is %v", page)
	}
	a.section = section{kind: sectionSettings}
	a.history.openAudio(voice)
	if a.section.kind == sectionSettings || a.selected != 2 {
		t.Fatalf("section %+v, chat %d", a.section, a.selected)
	}
}

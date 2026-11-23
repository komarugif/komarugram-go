// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"os"
	"sync"
	"testing"
	"time"

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
	wdk.InitMaterialThemeInContext(gtx, testTheme(gtx, schemes.SchemeBaselineLight()))
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

// The song on a profile opens the playlist, whose songs play one after
// another in the window's player, told in its bar, which goes on through
// them once the dialog closed; a click on the bar goes back to the
// playlist, in no chat: the account's own on its page, another's in the
// info.
func TestProfilePlaylistPlays(t *testing.T) {
	w := &appwindow.Window{Motion: motion.New(func() {})}
	defer w.Motion.Close()
	store := mockstore.New(time.Now(), 0)
	a := New(w, store, Services{MiniApps: miniappprefs.New(miniapp.Shared)})
	t.Cleanup(a.Close)
	var played []*silentPlayback
	var mu sync.Mutex
	a.history.audio.play = func(src audio.Source) (audioPlayback, error) {
		s := &silentPlayback{src: src, playing: true}
		mu.Lock()
		played = append(played, s)
		mu.Unlock()
		return s, nil
	}
	me := store.Me()
	gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(900, 800)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, testTheme(gtx, schemes.SchemeBaselineLight()))
	for _, c := range []struct {
		name string
		info *chatInfo
		open func()
	}{
		{"own", a.profile.info, func() { a.section = section{kind: sectionProfile}; a.profile.open(me) }},
		{"another's", a.info, func() { chat, _ := chatByID(store.Chats(), 2); a.info.Open(chat) }},
	} {
		mu.Lock()
		played = nil
		mu.Unlock()
		c.open()
		awaitShared(t, c.info)
		waitAudio(t, c.name+": the profile did not come", func() bool { c.info.drainProfile(); return c.info.profile.Music != nil })
		c.info.music.click.Click()
		c.info.update(gtx, a.catalog())
		awaitShared(t, c.info)
		if c.info.section != model.SharedSavedMusic || len(c.info.messages) < 2 || !c.info.messages[0].Playlist {
			t.Fatalf("%s: the song opened section %q with %d songs", c.name, c.info.section, len(c.info.messages))
		}
		first, second := c.info.messages[0], c.info.messages[1]
		// What a click on the song's button does.
		c.info.renderer.audio.toggle(c.info.renderer, first, -1)
		waitAudio(t, c.name+": the song did not play", func() bool { return a.history.audio.state(first).playing })
		if !a.history.audioBarLive() {
			t.Fatalf("%s: the bar does not tell the song", c.name)
		}
		mu.Lock()
		song := played[0]
		mu.Unlock()
		song.end()
		waitAudio(t, c.name+": the playlist did not go on", func() bool { return a.history.audio.state(second).playing })

		// Away from the playlist, the bar goes on through it, and goes
		// back to it.
		c.info.goBack()
		a.info.Close()
		if before, _ := a.history.audio.around(); !before {
			t.Fatalf("%s: away from the playlist, the bar sees no song before the second", c.name)
		}
		a.history.audio.skip(-1)
		waitAudio(t, c.name+": the bar did not go back to the first", func() bool { return a.history.audio.state(first).playing })
		a.section = section{kind: sectionSettings}
		bar := func() {
			gtx.Ops.Reset()
			a.withAudioBar(gtx, a.catalog(), func(gtx layout.Context) layout.Dimensions { return layout.Dimensions{Size: gtx.Constraints.Max} })
		}
		bar()
		a.history.audioBar.bar.click.Click()
		bar()
		if c.info.section != model.SharedSavedMusic || !c.info.visible {
			t.Fatalf("%s: the bar opened section %q, shown %v", c.name, c.info.section, c.info.visible)
		}
		if c.info == a.profile.info && a.section.kind != sectionProfile {
			t.Fatalf("the bar opened section %+v, not the profile", a.section)
		}
		a.history.audio.stop()
	}
}

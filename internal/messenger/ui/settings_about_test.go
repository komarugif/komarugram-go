// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/app"
	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/appwindow"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/internal/motion"
	"komarugram/pkg/rectanim"
)

// communityStore finds the community's chat by its username, as Telegram
// would, or finds nothing.
type communityStore struct {
	*linkStore
	chat  model.Chat
	found bool
}

func (s *communityStore) ResolveUsername(_ context.Context, name string) (model.Chat, error) {
	if s.found && name == community {
		return s.chat, nil
	}
	return model.Chat{}, model.ErrLinkNotFound
}

// The community opens in the client from the About section, among the
// chats; a name Telegram does not know is told under its row.
func TestAboutOpensTheCommunity(t *testing.T) {
	for _, found := range []bool{true, false} {
		mock := mockstore.New(time.Now(), 0)
		store := &communityStore{linkStore: &linkStore{Store: mock}, chat: model.Chat{ID: 4242, Title: "KomaruGram"}, found: found}
		w := &appwindow.Window{Window: new(app.Window), Motion: motion.New(func() {})}
		a := New(w, store, Services{})
		a.section = section{kind: sectionSettings}
		a.settings.section = settingsAbout
		if a.settings.about.openCommunity == nil {
			t.Fatal("no community row with a store that resolves usernames")
		}
		a.settings.about.openCommunity()
		deadline := time.Now().Add(2 * time.Second)
		for a.selected != store.chat.ID && a.settings.about.communityProblem == "" && time.Now().Before(deadline) {
			a.Update(layout.Context{Ops: new(op.Ops), Now: time.Now(), Constraints: layout.Exact(image.Pt(900, 700))})
			time.Sleep(5 * time.Millisecond)
		}
		if found && (a.selected != store.chat.ID || !a.section.showsChats()) {
			t.Errorf("found: selected %d, section %v", a.selected, a.section.kind)
		}
		if !found && (a.selected == store.chat.ID || a.settings.about.communityProblem == "") {
			t.Errorf("not found: selected %d, problem %q", a.selected, a.settings.about.communityProblem)
		}
		a.Close()
		w.Motion.Close()
	}
}

// The pictures from GitHub take the icons' places once they come: the
// mark drawn in the icons' color, the avatar as it is.
func TestAboutTakesThePictures(t *testing.T) {
	mark := []byte(`<svg width="16" height="16" viewBox="0 0 16 16" xmlns="http://www.w3.org/2000/svg"><path d="M0 0H16V16H0Z"/></svg>`)
	var avatar bytes.Buffer
	png.Encode(&avatar, image.NewRGBA(image.Rect(0, 0, 4, 4)))
	v := newAboutView(nil)
	// The mascot's scenes are another test's.
	v.mascot.load = func(context.Context) ([]*rectanim.Scene, error) { return nil, errors.New("none") }
	var asked []string
	v.fetch = func(_ context.Context, url string) ([]byte, error) {
		asked = append(asked, url)
		if url == githubMark {
			return mark, nil
		}
		return avatar.Bytes(), nil
	}
	v.open()
	deadline := time.Now().Add(2 * time.Second)
	for v.fetching && time.Now().Before(deadline) {
		v.Update(layout.Context{Ops: new(op.Ops)})
		time.Sleep(time.Millisecond)
	}
	if v.mark == nil {
		t.Fatal("the mark was not taken")
	}
	if user := maintainer(); user != "" && (v.avatar == nil || len(asked) != 2 || asked[1] != "https://github.com/"+user+".png?size=96") {
		t.Fatalf("avatar %v, asked %q", v.avatar != nil, asked)
	}
	img := tintedMark(v.mark, 8, color.NRGBA{R: 10, G: 20, B: 30, A: 255})
	if c := img.NRGBAAt(4, 4); c != (color.NRGBA{R: 10, G: 20, B: 30, A: 255}) {
		t.Fatalf("the mark is %v inside", c)
	}
	// Both came: opening again asks for nothing.
	asked = nil
	v.open()
	if v.fetching || len(asked) != 0 {
		t.Fatal("fetched again")
	}
}

// A scene of the mascot's kind, but not the mascot: a square.
const squareScene = `{"version": 2, "name": "%s", "viewBox": [0, 0, 10, 10], "stage": 1, "duration": 1,
 "nodes": [{"id": 0, "parent": -1, "kind": "rect", "x": 0, "y": 0, "w": 10, "h": 10, "fill": "#DD775B"}],
 "tracks": [{"delay": 0, "duration": 1, "repeat": true, "events": [{"node": 0, "prop": "x", "start": 0, "duration": 1, "ease": "linear", "from": 0, "to": 5}]}]}`

// The mascot's scenes load from a folder, and a click on the stage goes
// on to the next one; without scenes there is no card.
func TestMascotScenes(t *testing.T) {
	dir := t.TempDir()
	write := func(name, data string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("index.json", `{"version": 1, "animations": [{"name": "a", "file": "a.json"}, {"name": "b", "file": "b.json"}]}`)
	write("a.json", fmt.Sprintf(squareScene, "a"))
	write("b.json", fmt.Sprintf(squareScene, "b"))
	t.Setenv("KOMARUGRAM_MASCOT", dir)

	v := newAboutView(nil)
	v.fetch = func(context.Context, string) ([]byte, error) { return nil, errors.New("offline") }
	// Still, so that only a click changes the scene.
	v.animate = func() bool { return false }
	l := localization.For("ru")
	h := &focusHarness{draw: func(gtx layout.Context) {
		v.Update(gtx)
		v.Layout(gtx, l)
	}}
	v.open()
	deadline := time.Now().Add(2 * time.Second)
	for len(v.mascot.scenes) == 0 && time.Now().Before(deadline) {
		h.frame()
		time.Sleep(time.Millisecond)
	}
	if len(v.mascot.scenes) != 2 || v.mascot.scenes[0].Name != "a" {
		t.Fatalf("scenes %v", v.mascot.scenes)
	}
	if !clickColumn(h, 450, 819, 0, -4, func() bool { return v.mascot.index == 1 }) {
		t.Fatal("a click on the stage did not go on to the next scene")
	}
	// Under the stage, the link to the scenes' repository.
	browserOpened.Store("")
	if !clickColumn(h, 450, 819, 0, -4, func() bool { return browserOpened.Load() == "https://"+mascotRepository }) {
		t.Fatal("no link to the scenes' repository")
	}

	// A file the index names outside its folder is refused.
	write("index.json", `{"animations": [{"file": "../a.json"}]}`)
	if _, err := loadScenes(context.Background(), dir, nil); err == nil {
		t.Error("a scene outside the folder was read")
	}
	// No scenes, no card.
	empty := newMascotView(func() {}, nil)
	gtx := layout.Context{Ops: new(op.Ops), Constraints: layout.Exact(image.Pt(400, 400))}
	if d := empty.Layout(gtx, l, true); d.Size != (image.Point{}) {
		t.Errorf("a card without scenes: %v", d.Size)
	}
}

// A scene plays over and over: only a click goes on to the next one.
func TestMascotStaysOnItsScene(t *testing.T) {
	a, err := rectanim.Parse([]byte(fmt.Sprintf(squareScene, "a")))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := rectanim.Parse([]byte(fmt.Sprintf(squareScene, "b")))
	v := newMascotView(func() {}, nil)
	v.scenes = []*rectanim.Scene{a, b}
	start := time.Now()
	for _, after := range []time.Duration{0, 5 * time.Second, time.Minute} {
		gtx := layout.Context{Ops: new(op.Ops), Now: start.Add(after), Constraints: layout.Exact(image.Pt(400, 400)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
		v.Layout(gtx, localization.For("ru"), true)
		if v.index != 0 {
			t.Fatalf("after %v: scene %d", after, v.index)
		}
	}
}

// With the animations off the mascot stands in its scene's rest frame,
// and plays under the pointer only, from the start each time it comes.
func TestMascotPlaysUnderThePointer(t *testing.T) {
	s, err := rectanim.Parse([]byte(strings.Replace(fmt.Sprintf(squareScene, "a"), `"stage": 1,`, `"stage": 1, "rest": 0.5,`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	v := newMascotView(func() {}, nil)
	v.scenes = []*rectanim.Scene{s}
	l := localization.For("ru")
	h := &focusHarness{draw: func(gtx layout.Context) {
		v.Update(gtx)
		v.Layout(gtx, l, false)
	}}
	move := func(y float32) {
		h.router.Queue(pointer.Event{Kind: pointer.Move, Source: pointer.Mouse, Position: f32.Pt(450, y)})
		h.frame()
		h.frame()
	}
	h.frame()
	if !v.started.IsZero() {
		t.Fatal("playing without the pointer")
	}
	move(50) // over the stage
	if v.started.IsZero() {
		t.Fatal("not playing under the pointer")
	}
	move(700) // away, below the card
	if !v.started.IsZero() {
		t.Fatal("still playing after the pointer left")
	}
}

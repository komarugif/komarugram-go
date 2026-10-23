// SPDX-License-Identifier: Unlicense OR MIT
package shortcuts

import (
	"gioui.org/io/event"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/layout"
	"gioui.org/op"
	"image"
	"reflect"
	"testing"
)

func TestPhysicalBindingSurvivesLayoutAndRemap(t *testing.T) {
	var r input.Router
	b := Bindings{"app.settings": {{Key: "Q", Modifiers: key.ModCtrl | key.ModAlt}}}
	commands := Select("app.settings")
	poll := func() (string, bool) {
		g := layout.Context{Ops: new(op.Ops), Source: r.Source(), Constraints: layout.Exact(image.Pt(100, 100))}
		id, e, ok := Next(g, nil, b, commands)
		r.Frame(g.Ops)
		return id, ok && e.State == key.Press
	}
	poll()
	for _, name := range []key.Name{"Q", "Й", "'"} {
		r.Queue(key.Event{Name: name, Physical: "Q", Modifiers: key.ModCtrl | key.ModAlt, State: key.Press})
		id, ok := poll()
		if !ok || id != "app.settings" {
			t.Fatalf("layout %s lost binding", name)
		}
	}
	r.Queue(key.Event{Name: "Q", Physical: "A", Modifiers: key.ModCtrl | key.ModAlt, State: key.Press})
	if _, ok := poll(); ok {
		t.Fatal("logical symbol matched wrong physical key")
	}
	r.Queue(key.Event{Name: ",", Physical: ",", Modifiers: key.ModCtrl, State: key.Press})
	if _, ok := poll(); ok {
		t.Fatal("old default survived remap")
	}
	b["app.settings"] = nil
	r.Queue(key.Event{Name: "Q", Physical: "Q", Modifiers: key.ModCtrl | key.ModAlt, State: key.Press})
	if _, ok := poll(); ok {
		t.Fatal("disabled binding fired")
	}
}
func TestScopeConflictsAndValidation(t *testing.T) {
	c, _ := Find("app.settings")
	chord := primary("C")
	if len(Conflicts(c, chord, Commands(), nil)) == 0 {
		t.Fatal("global command collided with copy")
	}
	v, _ := Find("viewer.save")
	if len(Conflicts(v, primary("A"), Commands(), nil)) != 0 {
		t.Fatal("disjoint context collided")
	}
	for _, text := range []string{"Ctrl+Alt+T", "Shift+F9", "Ctrl+;", "Super+Space"} {
		c, err := Parse(text)
		if err != nil || !ValidKey(c.Key) {
			t.Fatal(text, err)
		}
	}
	for _, text := range []string{"Caps+Q", "Ctrl+Ctrl+", "Ctrl+unknown"} {
		if _, err := Parse(text); err == nil {
			t.Fatal(text)
		}
	}
	if Validate(c, Chord{Key: "A"}) == nil || Validate(c, Chord{Key: key.NameReturn, Modifiers: key.ModCtrl}) == nil {
		t.Fatal("unsafe capture accepted")
	}
	b := Bindings{"example.count": {{Key: "T", Modifiers: key.ModCtrl | key.ModAlt}}}
	copy := b.Clone()
	copy["example.count"][0].Key = "Q"
	if reflect.DeepEqual(b, copy) {
		t.Fatal("mutable snapshot")
	}
}

func TestCommandRouterLeavesFocusEventsForEditor(t *testing.T) {
	var r input.Router
	var target byte
	g := layout.Context{Ops: new(op.Ops), Source: r.Source(), Constraints: layout.Exact(image.Pt(100, 100))}
	g.Event(key.FocusFilter{Target: &target})
	event.Op(g.Ops, &target)
	r.Frame(g.Ops)
	r.Source().Execute(key.FocusCmd{Tag: &target})
	g.Ops = new(op.Ops)
	if _, _, ok := Next(g, &target, nil, Select("composer.paste")); ok {
		t.Fatal("command route consumed an editor focus event")
	}
	ev, ok := g.Event(key.FocusFilter{Target: &target})
	focus, isFocus := ev.(key.FocusEvent)
	if !ok || !isFocus || !focus.Focus {
		t.Fatal("editor lost focus notification", ev)
	}
}

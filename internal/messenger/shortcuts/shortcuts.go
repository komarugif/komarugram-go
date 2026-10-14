// SPDX-License-Identifier: Unlicense OR MIT

// Package shortcuts owns stable command IDs and user-assigned physical chords.
// It never registers system-wide grabs or supplies application authority.
package shortcuts

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/layout"
)

const AllModifiers = key.ModCtrl | key.ModCommand | key.ModShift | key.ModAlt | key.ModSuper

type Chord struct {
	Key       key.Name      `json:"key"`
	Modifiers key.Modifiers `json:"modifiers"`
}
type Bindings map[string][]Chord

func (b Bindings) Clone() Bindings {
	if b == nil {
		return nil
	}
	out := make(Bindings, len(b))
	for id, v := range b {
		out[id] = slices.Clone(v)
	}
	return out
}
func (b Bindings) For(c Command) []Chord {
	if v, ok := b[c.ID]; ok {
		return slices.Clone(v)
	}
	return slices.Clone(c.Default)
}

type Command struct {
	ID, Label, Scope string
	Default          []Chord
	ReadOnly         bool
}

func command(id, scope, label string, chords ...Chord) Command {
	return Command{ID: id, Label: label, Scope: scope, Default: chords}
}
func primary(k key.Name) Chord { return Chord{k, key.ModShortcut} }

var builtinCommands = []Command{
	command("app.settings", "app", "keys.settings", primary(",")),
	command("app.search", "app", "keys.search"), command("app.saved", "app", "keys.saved"), command("app.profile", "app", "keys.profile"), command("app.theme", "app", "keys.theme"),
	command("chat.search", "history", "keys.chat_search"),
	command("history.copy", "history", "keys.copy", primary("C")), command("history.select_all", "history", "keys.select_all", primary("A")),
	command("composer.paste", "composer", "keys.paste", primary("V")),
	command("viewer.previous", "viewer", "keys.previous", Chord{key.NameLeftArrow, 0}), command("viewer.next", "viewer", "keys.next", Chord{key.NameRightArrow, 0}),
	command("viewer.first", "viewer", "keys.first", Chord{key.NameHome, 0}), command("viewer.last", "viewer", "keys.last", Chord{key.NameEnd, 0}),
	command("viewer.zoom_in", "viewer", "keys.zoom_in", primary("="), Chord{"=", key.ModShortcut | key.ModShift}), command("viewer.zoom_out", "viewer", "keys.zoom_out", primary("-")), command("viewer.zoom_reset", "viewer", "keys.zoom_reset", primary("0")),
	command("viewer.copy", "viewer", "keys.copy", primary("C")), command("viewer.save", "viewer", "keys.save", primary("S")),
	command("article.find", "article", "keys.find", primary("F")), command("article.zoom_in", "article", "keys.zoom_in", primary("="), Chord{"=", key.ModShortcut | key.ModShift}), command("article.zoom_out", "article", "keys.zoom_out", primary("-")), command("article.zoom_reset", "article", "keys.zoom_reset", primary("0")),
	command("article.back", "article", "keys.back", Chord{key.NameLeftArrow, key.ModAlt}, primary("[")), command("article.forward", "article", "keys.forward", Chord{key.NameRightArrow, key.ModAlt}, primary("]")),
	{ID: "fixed.dismiss", Label: "keys.dismiss", Scope: "fixed", Default: []Chord{{Key: key.NameEscape}}, ReadOnly: true},
	{ID: "fixed.send", Label: "keys.send", Scope: "fixed", Default: []Chord{{Key: key.NameReturn}}, ReadOnly: true},
	{ID: "fixed.editor_copy", Label: "keys.editor_copy", Scope: "editor", Default: []Chord{primary("C")}, ReadOnly: true},
	{ID: "fixed.editor_cut", Label: "keys.editor_cut", Scope: "editor", Default: []Chord{primary("X")}, ReadOnly: true},
	{ID: "fixed.editor_paste", Label: "keys.editor_paste", Scope: "editor", Default: []Chord{primary("V")}, ReadOnly: true},
	{ID: "fixed.editor_select_all", Label: "keys.editor_select_all", Scope: "editor", Default: []Chord{primary("A")}, ReadOnly: true},
	{ID: "fixed.editor_undo", Label: "keys.editor_undo", Scope: "editor", Default: []Chord{primary("Z"), {Key: "Z", Modifiers: key.ModShortcut | key.ModShift}}, ReadOnly: true},
}

func Commands() []Command {
	out := slices.Clone(builtinCommands)
	for i := range out {
		out[i].Default = slices.Clone(out[i].Default)
	}
	return out
}

func Find(id string) (Command, bool) {
	for _, c := range builtinCommands {
		if c.ID == id {
			c.Default = slices.Clone(c.Default)
			return c, true
		}
	}
	return Command{}, false
}
func ValidKey(k key.Name) bool {
	if len(k) == 1 && strings.Contains("ABCDEFGHIJKLMNOPQRSTUVWXYZ1234567890-=[]\\;'`,./", string(k)) {
		return true
	}
	if n, err := strconv.Atoi(strings.TrimPrefix(string(k), "F")); strings.HasPrefix(string(k), "F") && err == nil && n >= 1 && n <= 24 {
		return true
	}
	return slices.Contains([]key.Name{key.NameLeftArrow, key.NameRightArrow, key.NameUpArrow, key.NameDownArrow, key.NameHome, key.NameEnd, key.NamePageUp, key.NamePageDown, key.NameDeleteBackward, key.NameDeleteForward, key.NameTab, key.NameReturn, key.NameEnter, key.NameEscape, key.NameSpace, "IntlBackslash"}, k)
}
func Validate(c Command, chord Chord) error {
	if !ValidKey(chord.Key) || chord.Modifiers&^AllModifiers != 0 {
		return fmt.Errorf("invalid chord")
	}
	if chord.Key == key.NameEscape || chord.Key == key.NameTab || chord.Key == key.NameReturn || chord.Key == key.NameEnter {
		return fmt.Errorf("reserved dialog key")
	}
	if chord.Modifiers&(key.ModCtrl|key.ModCommand|key.ModAlt|key.ModSuper) == 0 && c.Scope != "viewer" && !strings.HasPrefix(string(chord.Key), "F") {
		return fmt.Errorf("a modifier is required")
	}
	if chord.Modifiers == key.ModShift && len(chord.Key) == 1 {
		return fmt.Errorf("a shortcut modifier is required")
	}
	return nil
}
func Overlap(a, b string) bool {
	if a == b {
		return true
	}
	if a == "fixed" || b == "fixed" {
		return false
	}
	return (a == "app" && b != "article") || (b == "app" && a != "article") || (a == "article" && b == "history") || (b == "article" && a == "history") || (a == "editor" && (b == "article" || b == "composer")) || (b == "editor" && (a == "article" || a == "composer"))
}
func Conflicts(candidate Command, chord Chord, catalog []Command, bindings Bindings) []Command {
	var out []Command
	for _, c := range catalog {
		if c.ID == "fixed.editor_paste" && candidate.ID == "composer.paste" {
			continue
		}
		if c.ID == candidate.ID || (c.ReadOnly && c.Scope == "fixed") || !Overlap(c.Scope, candidate.Scope) {
			continue
		}
		if slices.Contains(bindings.For(c), chord) {
			out = append(out, c)
		}
	}
	return out
}
func (c Chord) String() string {
	var parts []string
	for _, v := range []struct {
		m key.Modifiers
		s string
	}{{key.ModCtrl, "Ctrl"}, {key.ModCommand, "⌘"}, {key.ModAlt, "Alt"}, {key.ModShift, "Shift"}, {key.ModSuper, "Super"}} {
		if c.Modifiers.Contain(v.m) {
			parts = append(parts, v.s)
		}
	}
	return strings.Join(append(parts, string(c.Key)), "+")
}

// Next is shared by application, article, viewer and focused selection routes.
// A binding is matched by hardware position; text editing still uses Name.
func Next(gtx layout.Context, focus event.Tag, bindings Bindings, commands []Command) (string, key.Event, bool) {
	var filters []event.Filter
	for _, c := range commands {
		for _, b := range bindings.For(c) {
			filters = append(filters, key.Filter{Focus: focus, Physical: b.Key, Required: b.Modifiers})
		}
	}
	if len(filters) == 0 {
		return "", key.Event{}, false
	}
	ev, ok := gtx.Event(filters...)
	if !ok {
		return "", key.Event{}, false
	}
	e, ok := ev.(key.Event)
	if !ok {
		return "", key.Event{}, true
	}
	position := e.Physical
	if position == "" {
		position = e.Name
	}
	for _, c := range commands {
		for _, b := range bindings.For(c) {
			if b.Key == position && b.Modifiers == e.Modifiers {
				return c.ID, e, true
			}
		}
	}
	return "", e, true
}
func Select(ids ...string) []Command {
	var out []Command
	for _, id := range ids {
		if c, ok := Find(id); ok {
			out = append(out, c)
		}
	}
	return out
}

// Parse supports typing a combination that the desktop consumes before it can
// be recorded (for example a Plasma global shortcut).
func Parse(text string) (Chord, error) {
	parts := strings.Split(strings.TrimSpace(text), "+")
	var out Chord
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if i == len(parts)-1 {
			aliases := map[string]key.Name{"LEFT": key.NameLeftArrow, "RIGHT": key.NameRightArrow, "UP": key.NameUpArrow, "DOWN": key.NameDownArrow, "HOME": key.NameHome, "END": key.NameEnd, "PAGEUP": key.NamePageUp, "PAGEDOWN": key.NamePageDown, "SPACE": key.NameSpace, "ESC": key.NameEscape, "ENTER": key.NameReturn, "TAB": key.NameTab}
			out.Key = key.Name(strings.ToUpper(part))
			if v, ok := aliases[strings.ToUpper(part)]; ok {
				out.Key = v
			}
		} else {
			switch strings.ToLower(part) {
			case "ctrl", "control":
				out.Modifiers |= key.ModCtrl
			case "alt", "option":
				out.Modifiers |= key.ModAlt
			case "shift":
				out.Modifiers |= key.ModShift
			case "super", "meta", "win":
				out.Modifiers |= key.ModSuper
			case "cmd", "command", "⌘":
				out.Modifiers |= key.ModCommand
			default:
				return Chord{}, fmt.Errorf("invalid modifier")
			}
		}
	}
	if !ValidKey(out.Key) {
		return Chord{}, fmt.Errorf("invalid key")
	}
	return out, nil
}
func ValidateBindings(b Bindings) error {
	if len(b) > 512 {
		return fmt.Errorf("too many shortcut commands")
	}
	for id, list := range b {
		if len(id) == 0 || len(id) > 256 || len(list) > 4 {
			return fmt.Errorf("invalid shortcut binding")
		}
		c, known := Find(id)
		if known && c.ReadOnly {
			return fmt.Errorf("standard input keys cannot be reassigned")
		}
		if !known {
			c = Command{ID: id, Scope: "history"}
		}
		for _, chord := range list {
			if err := Validate(c, chord); err != nil {
				return err
			}
		}
	}
	return nil
}

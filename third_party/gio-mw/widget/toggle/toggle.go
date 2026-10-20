// SPDX-License-Identifier: Unlicense OR MIT

package toggle

import (
	"gio-mw/token"
	"gio-mw/wdk"
	"slices"

	"gioui.org/layout"
	"gioui.org/widget"
)

type Toggle[T comparable] struct {
	clickable map[T]*widget.Clickable
	disabled  map[T]bool
	animation map[T]*animation
	onChange  func([]T)
	options   []T
	values    []T
	updated   bool
}

func (t *Toggle[T]) GetValues() []T {
	return t.values
}

func (t *Toggle[T]) SetValues(values []T) {
	for _, value := range values {
		if !slices.Contains(t.options, value) {
			panic("toggle.Toggle: value must be in options")
		}
	}
	t.values = values
}

func (t *Toggle[T]) Disable() *Toggle[T] {
	for value := range t.clickable {
		t.disabled[value] = true
	}
	return t
}

func (t *Toggle[T]) Enable() *Toggle[T] {
	for value := range t.clickable {
		t.disabled[value] = false
	}
	return t
}

func (t *Toggle[T]) DisableOption(value T) *Toggle[T] {
	t.disabled[value] = true
	return t
}

func (t *Toggle[T]) EnableOption(value T) *Toggle[T] {
	t.disabled[value] = false
	return t
}

func (t *Toggle[T]) ToggleDisable() *Toggle[T] {
	for value := range t.clickable {
		t.disabled[value] = !t.disabled[value]
	}
	return t
}

func (t *Toggle[T]) Layout(gtx layout.Context, labels map[T]string) layout.Dimensions {
	t.update(gtx)
	style := widgetStyle[T]{
		Toggle:  t,
		tLabels: labels,
		tTheme:  BuildTheme(gtx),
	}
	return style.layout(gtx)
}

func (t *Toggle[T]) update(gtx layout.Context) {
	changed := false
	for value, clickable := range t.clickable {
		if clickable.Clicked(gtx) {
			index := slices.Index(t.values, value)
			if index >= 0 {
				t.values = slices.Delete(t.values, index, index+1)
			} else {
				t.values = append(t.values, value)
			}
			changed = true
		}
	}
	if changed {
		if t.onChange != nil {
			t.onChange(t.values)
		}
	}
}

// animation holds the animated drawing parameters of one switch.
type animation struct {
	position     wdk.FloatTween // 0 when unselected, 1 when selected.
	handleSize   wdk.FloatTween // In pixels.
	track        wdk.ColorTween
	trackOutline wdk.ColorTween
	handle       wdk.ColorTween
	icon         wdk.ColorTween
	stateLayer   wdk.ColorTween
}

func (t *Toggle[T]) getAnimation(value T) *animation {
	if t.animation == nil {
		t.animation = make(map[T]*animation, len(t.options))
	}
	a, ok := t.animation[value]
	if !ok {
		a = &animation{
			position:   wdk.FloatTween{Duration: token.DurationMedium1},
			handleSize: wdk.FloatTween{Duration: token.DurationShort3},
			stateLayer: wdk.ColorTween{Duration: token.DurationShort3},
		}
		t.animation[value] = a
	}
	return a
}

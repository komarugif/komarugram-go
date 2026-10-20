// SPDX-License-Identifier: Unlicense OR MIT

package radio

import (
	"gio-mw/token"
	"gio-mw/wdk"
	"slices"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"
)

type Kind int

const (
	ButtonKind Kind = iota
	LeadingKind
	TrailingKind
)

// Radios hold the state of a set of radio buttons that NewRadios can create.
//
// The Layout labels map argument is used to provide a label for each radio button.
type Radios[T comparable] struct {
	animation map[T]*animation
	clickable map[T]*widget.Clickable
	disabled  map[T]bool
	onChange  func(T)
	options   []T
	value     T
	updated   bool
}

func (r *Radios[T]) GetValue() T {
	return r.value
}

func (r *Radios[T]) SetValue(value T) {
	if slices.Contains(r.options, value) {
		r.value = value
	} else {
		panic("radio.Radios: value must be in options")
	}
}

func (r *Radios[T]) Disable() {
	for value := range r.clickable {
		r.disabled[value] = true
	}
}

func (r *Radios[T]) Enable() {
	for value := range r.clickable {
		r.disabled[value] = false
	}
}

func (r *Radios[T]) DisableOption(value T) *Radios[T] {
	r.disabled[value] = true
	return r
}

func (r *Radios[T]) EnableOption(value T) *Radios[T] {
	r.disabled[value] = false
	return r
}

func (r *Radios[T]) ToggleDisable() {
	for value := range r.clickable {
		r.disabled[value] = !r.disabled[value]
	}
}

func (r *Radios[T]) Update(gtx layout.Context) {
	for value, clickable := range r.clickable {
		if clickable.Clicked(gtx) {
			r.value = value
			if r.onChange != nil {
				r.onChange(value)
			}
			gtx.Execute(op.InvalidateCmd{})
		}
	}
	r.updated = true
}

func (r *Radios[T]) Layout(gtx layout.Context, kind Kind, labels map[T]string) layout.Dimensions {
	if len(r.options) == 0 {
		return layout.Dimensions{}
	}
	if len(labels) != len(r.options) {
		panic("radio.Radios: labels and options must have the same length")
	}
	if r.updated == false {
		panic("radio.Radios: call Update before Layout")
	}
	style := widgetStyle[T]{
		Radios:  r,
		rKind:   kind,
		rLabels: labels,
		rTheme:  BuildTheme(gtx),
	}
	return style.layout(gtx)
}

// animation holds the animated drawing parameters of one radio button.
type animation struct {
	dot        wdk.FloatTween // Scale of the selected dot, 0 to 1.
	icon       wdk.ColorTween
	stateLayer wdk.ColorTween
}

func (r *Radios[T]) getAnimation(value T) *animation {
	if r.animation == nil {
		r.animation = make(map[T]*animation, len(r.options))
	}
	a, ok := r.animation[value]
	if !ok {
		a = &animation{
			dot:        wdk.FloatTween{Duration: token.DurationMedium1},
			stateLayer: wdk.ColorTween{Duration: token.DurationShort3},
		}
		r.animation[value] = a
	}
	return a
}

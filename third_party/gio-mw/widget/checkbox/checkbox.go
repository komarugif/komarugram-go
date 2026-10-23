// SPDX-License-Identifier: Unlicense OR MIT

package checkbox

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

type Checkboxes[T comparable] struct {
	animation       map[T]*animation
	parentAnimation *animation
	parent          *widget.Clickable
	clickable       map[T]*widget.Clickable
	disabled        map[T]bool
	error           map[T]bool
	onChange        func([]T)
	options         []T
	values          []T
	updated         bool
}

func (c *Checkboxes[T]) GetValues() []T {
	return c.values
}

func (c *Checkboxes[T]) SetValues(values []T) {
	for _, value := range values {
		if !slices.Contains(c.options, value) {
			panic("checkbox.Checkboxes: value must be in options")
		}
	}
	c.values = values
}

func (c *Checkboxes[T]) Disable() {
	for _, value := range c.options {
		c.disabled[value] = true
	}
}

func (c *Checkboxes[T]) Enable() {
	for _, value := range c.options {
		c.disabled[value] = false
	}
}

func (c *Checkboxes[T]) DisableOption(value T) *Checkboxes[T] {
	c.disabled[value] = true
	return c
}

func (c *Checkboxes[T]) EnableOption(value T) *Checkboxes[T] {
	c.disabled[value] = false
	return c
}

func (c *Checkboxes[T]) ToggleDisable() {
	for _, value := range c.options {
		c.disabled[value] = !c.disabled[value]
	}
}

func (c *Checkboxes[T]) ToggleError() {
	for _, value := range c.options {
		c.error[value] = !c.error[value]
	}
}

func (c *Checkboxes[T]) Update(gtx layout.Context) {
	if c.parent.Clicked(gtx) {
		selectedCount := len(c.values)
		optionsCount := len(c.options)
		if selectedCount == optionsCount {
			c.values = []T{}
		} else {
			// Select all
			c.values = []T{}
			for _, value := range c.options {
				c.values = append(c.values, value)
			}
		}
		if c.onChange != nil {
			c.onChange(c.values)
		}
		gtx.Execute(op.InvalidateCmd{})
	}
	for value, clickable := range c.clickable {
		if clickable.Clicked(gtx) {
			foundIndex := slices.Index(c.values, value)
			if foundIndex == -1 {
				c.values = append(c.values, value)
			} else {
				c.values = slices.Delete(c.values, foundIndex, foundIndex+1)
			}
			if c.onChange != nil {
				c.onChange(c.values)
			}
			gtx.Execute(op.InvalidateCmd{})
		}
	}
	c.updated = true
}

func (c *Checkboxes[T]) Layout(gtx layout.Context, labels map[T]string) layout.Dimensions {
	return c.LayoutWithKind(gtx, LeadingKind, labels)
}

func (c *Checkboxes[T]) LayoutWithParent(gtx layout.Context, parentLabel string, labels map[T]string) layout.Dimensions {
	c.validateLayoutPreconditions(labels)

	style := widgetStyle[T]{
		Checkboxes: c,
		cKind:      LeadingKind,
		cLabel:     parentLabel,
		cLabels:    labels,
		cTheme:     BuildTheme(gtx),
	}
	return style.layoutWithParent(gtx)
}

func (c *Checkboxes[T]) LayoutWithKind(gtx layout.Context, kind Kind, labels map[T]string) layout.Dimensions {
	c.validateLayoutPreconditions(labels)

	style := widgetStyle[T]{
		Checkboxes: c,
		cKind:      kind,
		cLabels:    labels,
		cTheme:     BuildTheme(gtx),
	}
	return style.layoutWithKind(gtx)
}

func (c *Checkboxes[T]) validateLayoutPreconditions(labels map[T]string) {
	if len(c.options) == 0 {
		panic("checkbox.Checkboxes: no options provided")
	}
	if len(labels) != len(c.options) {
		panic("checkbox.Checkboxes: labels and options must have the same length")
	}
	if !c.updated {
		panic("checkbox.Checkboxes: call Update before Layout")
	}
}

// animation holds the animated drawing parameters of one checkbox.
type animation struct {
	check      wdk.FloatTween // Drawn part of the check mark, 0 to 1.
	dash       wdk.FloatTween // Morph from the check mark (0) to the dash (1).
	container  wdk.ColorTween
	outline    wdk.ColorTween
	icon       wdk.ColorTween
	stateLayer wdk.ColorTween
}

func newAnimation() *animation {
	return &animation{
		check:      wdk.FloatTween{Duration: token.DurationMedium1},
		dash:       wdk.FloatTween{Duration: token.DurationShort4},
		container:  wdk.ColorTween{Duration: token.DurationShort3},
		outline:    wdk.ColorTween{Duration: token.DurationShort3},
		stateLayer: wdk.ColorTween{Duration: token.DurationShort3},
	}
}

func (c *Checkboxes[T]) getAnimation(value T) *animation {
	if c.animation == nil {
		c.animation = make(map[T]*animation, len(c.options))
	}
	a, ok := c.animation[value]
	if !ok {
		a = newAnimation()
		c.animation[value] = a
	}
	return a
}

func (c *Checkboxes[T]) getParentAnimation() *animation {
	if c.parentAnimation == nil {
		c.parentAnimation = newAnimation()
	}
	return c.parentAnimation
}

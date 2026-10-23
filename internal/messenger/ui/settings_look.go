// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"strconv"
	"time"

	"gio-mw/token"
	"gio-mw/widget/slider"
	"gio-mw/widget/toggle"

	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
)

// lookSettings is the part of the appearance settings that changes how
// messages and avatars are drawn, as AyuGram's customization does.
type lookSettings struct {
	look    func() preferences.Look
	setLook func(preferences.Look)
	bubble  *slider.Slider
	avatar  *slider.Slider
	seconds *toggle.Toggle[string]
	edited  *textField
	deleted *textField
	// marks are the marks as the fields had them last.
	marks [2]string
}

func newLookSettings() *lookSettings {
	s := &lookSettings{edited: newTextField(0, ""), deleted: newTextField(0, "")}
	change := func(apply func(*preferences.Look)) {
		if s.setLook == nil {
			return
		}
		l := s.look()
		apply(&l)
		s.setLook(l)
	}
	steps := func(n int) []int {
		out := make([]int, n+1)
		for i := range out {
			out[i] = i
		}
		return out
	}
	s.bubble = slider.StandardSlider(steps(preferences.BubbleRadiusMax), preferences.BubbleRadiusMax, func(v int) {
		change(func(l *preferences.Look) { l.BubbleRadius = v })
	})
	s.avatar = slider.StandardSlider(steps(preferences.AvatarRound), preferences.AvatarRound, func(v int) {
		change(func(l *preferences.Look) { l.AvatarCorners = v })
	})
	s.seconds = toggle.NewToggle([]string{"seconds"}, nil, func(values []string) {
		change(func(l *preferences.Look) { l.Seconds = len(values) == 1 })
	})
	return s
}

// Update saves the marks typed.
func (s *lookSettings) Update(gtx layout.Context) {
	if s.look == nil || s.setLook == nil {
		return
	}
	l := s.look()
	marks := [2]string{s.edited.Text(), s.deleted.Text()}
	if marks != s.marks {
		s.marks = marks
		if marks[0] != l.EditedMark || marks[1] != l.DeletedMark {
			l.EditedMark, l.DeletedMark = marks[0], marks[1]
			s.setLook(l)
		}
	}
}

// Layout draws the sliders, the switch and the fields, with a message and
// an avatar drawn as they will be.
func (s *lookSettings) Layout(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	look := s.look()
	if s.bubble.GetValue() != look.BubbleRadius {
		s.bubble.SetValue(look.BubbleRadius)
	}
	if s.avatar.GetValue() != look.AvatarCorners {
		s.avatar.SetValue(look.AvatarCorners)
	}
	if look.Seconds != (len(s.seconds.GetValues()) == 1) {
		if look.Seconds {
			s.seconds.SetValues([]string{"seconds"})
		} else {
			s.seconds.SetValues(nil)
		}
	}
	if s.marks == ([2]string{}) && (look.EditedMark != "" || look.DeletedMark != "") {
		s.edited.editor.SetText(look.EditedMark)
		s.deleted.editor.SetText(look.DeletedMark)
		s.marks = [2]string{look.EditedMark, look.DeletedMark}
	}
	// The preview draws as the settings are now.
	withLook(gtx, look)
	value := func(title string, n int) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, title+": "+strconv.Itoa(n), token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
		})
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("look.title"), token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
		}),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return s.preview(gtx, l) }),
		vspace(12),
		value(l.T("look.bubble"), look.BubbleRadius),
		layout.Rigid(s.bubble.Layout),
		vspace(8),
		value(l.T("look.avatar"), look.AvatarCorners),
		layout.Rigid(s.avatar.Layout),
		vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return s.seconds.Layout(gtx, map[string]string{"seconds": l.T("look.seconds")})
		}),
		vspace(12),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return s.edited.Layout(gtx, l.T("look.edited_mark")+" · "+l.T("history.edited"), false)
		}),
		vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return s.deleted.Layout(gtx, l.T("look.deleted_mark")+" · "+l.T("history.deleted_mark"), false)
		}),
	)
}

// preview is an avatar and a bubble as the look draws them.
func (s *lookSettings) preview(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	m := model.Message{Text: l.T("look.preview"), EditedAt: previewTime, Deleted: true, Date: previewTime}
	return layout.Flex{Alignment: layout.End}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return avatar(gtx, 3, model.KindUser, "Ayu", 36)
		}),
		layout.Rigid(layout.Spacer{Width: 8}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Stack{}.Layout(gtx,
				layout.Expanded(func(gtx layout.Context) layout.Dimensions {
					size := gtx.Constraints.Min
					r := bubbleRadiusOf(gtx)
					fillRounded(gtx, sc.SurfaceContainerHigh, size, r)
					return layout.Dimensions{Size: size}
				}),
				layout.Stacked(func(gtx layout.Context) layout.Dimensions {
					return layout.Inset{Top: 8, Bottom: 7, Left: 12, Right: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{Axis: layout.Vertical, Alignment: layout.End}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return label(gtx, m.Text, token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
							}),
							vspace(4),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								gtx.Constraints.Min = image.Point{}
								return messageFooter(gtx, m, l, false, previewSendState(gtx, m))
							}),
						)
					})
				}),
			)
		}),
	)
}

// previewTime is the time of the preview's message.
var previewTime = time.Date(2026, 9, 25, 12, 34, 56, 0, time.Local)

// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"strings"
	"time"

	"gio-mw/token"
	"gio-mw/widget/scroll"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/widget"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// forwardPicker is the dialog that picks the chat to forward to: what the
// history forwards, and what the media viewer shows. Who opens it sends.
type forwardPicker struct {
	modal   modal
	search  widget.Editor
	list    scroll.List
	rows    map[int64]*surface
	cancel  surface
	busy    bool
	results chan error
	target  model.Chat
	loader  loadingIndicator
	// from, ids and selection are what the history forwards: messages of
	// chat from, the selected ones when selection is set, which are
	// unselected once they are.
	from      int64
	ids       []model.MessageID
	selection bool
}

// open opens the picker, its search empty; the search takes the keyboard.
func (f *forwardPicker) open(gtx layout.Context) {
	f.search.SingleLine = true
	f.search.SetText("")
	f.list = scroll.List{List: layout.List{Axis: layout.Vertical}}
	if f.rows == nil {
		f.rows = map[int64]*surface{}
	}
	f.modal.field = &f.search
	f.modal.Open()
	gtx.Execute(op.InvalidateCmd{})
}

// targets are the chats of chats one may send to that match the search,
// Saved Messages first.
func (f *forwardPicker) targets(chats []model.Chat, rights model.RightsSource, l localization.Catalog) []model.Chat {
	query := strings.ToLower(strings.TrimSpace(f.search.Text()))
	var saved, out []model.Chat
	for _, c := range chats {
		if rights != nil && !rights.CanSend(c.ID) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(l.ChatTitle(c)), query) {
			continue
		}
		if c.Kind == model.KindSaved {
			saved = append(saved, c)
		} else {
			out = append(out, c)
		}
	}
	return append(saved, out...)
}

// send sends to target with send, in the background, the picker busy until
// result takes what it brought.
func (f *forwardPicker) send(ctx context.Context, target model.Chat, send func(ctx context.Context, to int64) error, invalidate func()) {
	if f.busy {
		return
	}
	f.busy, f.target = true, target
	f.results = make(chan error, 1)
	results := f.results
	go func() {
		defer crash.Recover("forward", func(e *crash.Panic) { results <- e; invalidate() })
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		results <- send(ctx, target.ID)
		invalidate()
	}()
}

// result is what send brought, done once it has; on an error the picker
// stays open, telling it, and closes otherwise.
func (f *forwardPicker) result(l localization.Catalog) (err error, done bool) {
	if f.results == nil {
		return nil, false
	}
	select {
	case err = <-f.results:
		f.busy, f.results = false, nil
		if err != nil {
			f.modal.Toast(l.T("history.forward_failed") + ": " + mediaErrorText(err))
		} else {
			f.modal.Close()
		}
		return err, true
	default:
		return nil, false
	}
}

// layout draws the picker when it is open, of targets, and reports the
// chat clicked.
func (f *forwardPicker) layout(gtx layout.Context, l localization.Catalog, targets []model.Chat, avatar avatarLayout) (model.Chat, bool) {
	if !f.modal.Shown() {
		return model.Chat{}, false
	}
	var picked model.Chat
	clicked := false
	if !f.busy {
		if f.cancel.Clicked(gtx) {
			f.modal.Close()
		}
		for _, c := range targets {
			if r := f.rows[c.ID]; r != nil && r.Clicked(gtx) {
				picked, clicked = c, true
			}
		}
	}
	sc := scheme(gtx)
	f.modal.Layout(gtx, f.busy, func(gtx layout.Context) layout.Dimensions {
		width := min(gtx.Constraints.Max.X, gtx.Dp(420))
		height := min(gtx.Constraints.Max.Y-gtx.Dp(48), gtx.Dp(560))
		gtx.Constraints = layout.Exact(image.Pt(width, max(height, 0)))
		return f.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = gtx.Constraints.Max
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("history.forward_to"), token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
				}),
				vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(40))
					fillRounded(gtx, sc.SurfaceContainerHigh, size, gtx.Dp(20))
					return layout.Inset{Top: 10, Bottom: 8, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return flatEditor(gtx, &f.search, l.T("chat.search"))
					})
				}),
				vspace(8),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if f.busy {
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return f.loader.sized(gtx, l, 32)
						})
					}
					if len(targets) == 0 {
						return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Inset{Top: 24}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
								return label(gtx, l.T("chat.not_found"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 1)
							})
						})
					}
					return f.list.Layout(gtx, len(targets), func(gtx layout.Context, i int) layout.Dimensions {
						return f.row(gtx, targets[i], l, avatar)
					})
				}),
				vspace(8),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						if f.busy {
							gtx = gtx.Disabled()
						}
						return textButton(gtx, &f.cancel, l.T("history.cancel"))
					})
				}),
			)
		}, defaultCardPadding)
	})
	return picked, clicked
}

// row draws a chat of the picker: its avatar and title.
func (f *forwardPicker) row(gtx layout.Context, c model.Chat, l localization.Catalog, avatarOf avatarLayout) layout.Dimensions {
	r := f.rows[c.ID]
	if r == nil {
		r = new(surface)
		f.rows[c.ID] = r
	}
	sc := scheme(gtx)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(52))
	style := surfaceStyle{radius: gtx.Dp(10), background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor}
	return r.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		avatarPx := gtx.Dp(40)
		offset(gtx, image.Pt(gtx.Dp(6), (size.Y-avatarPx)/2), func(gtx layout.Context) layout.Dimensions {
			if avatarOf != nil {
				return avatarOf(gtx, c.ID, c.Kind, l.ChatTitle(c), 40)
			}
			return avatar(gtx, c.ID, c.Kind, l.ChatTitle(c), 40)
		})
		textX := gtx.Dp(6) + avatarPx + gtx.Dp(12)
		text := gtx
		text.Constraints = layout.Constraints{Max: image.Pt(max(size.X-textX-gtx.Dp(6), 0), size.Y)}
		macro := op.Record(gtx.Ops)
		dims := label(text, l.ChatTitle(c), token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
		call := macro.Stop()
		offset(gtx, image.Pt(textX, (size.Y-dims.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return dims
		})
		return layout.Dimensions{Size: size}
	})
}

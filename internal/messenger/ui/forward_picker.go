// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"time"

	"gio-mw/token"
	"gio-mw/widget/checkbox"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/paint"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// forwardPicker is the dialog that picks the chat to forward to: what the
// history forwards, and what the media viewer shows. Who opens it sends.
type forwardPicker struct {
	folders      func() []model.Folder
	goToMessage  bool
	goTo         *checkbox.Checkboxes[string]
	navigate     func(model.Chat)
	confirming   bool
	hasMedia     bool
	allowOptions bool
	options      model.ForwardOptions
	// Non-nil for a partial album selection. The IDs identify the subsets
	// that need captions removed by the server.
	omitAlbumCaptions        map[model.MessageID]bool
	dropAuthor, dropCaptions *checkbox.Checkboxes[string]
	back, confirm            surface
	chatPickerList
	modal   modal
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
	// songs, in place of ids, are songs of a profile's playlist: no
	// messages, they are sent anew, as Telegram Desktop sends a saved
	// music item (SendExistingDocument).
	songs []model.Message
}

// open opens the picker, its search empty; the search takes the keyboard.
func (f *forwardPicker) open(gtx layout.Context) {
	f.goToMessage = false
	f.goTo = checkbox.NewCheckboxes([]string{"go"}, nil, func(values []string) {
		f.goToMessage = len(values) > 0
	})
	f.confirming = false
	f.options = model.ForwardOptions{DropAuthor: f.mustDropAuthor()}
	var author []string
	if f.options.DropAuthor {
		author = []string{"author"}
	}
	f.dropAuthor = checkbox.NewCheckboxes([]string{"author"}, author, func(values []string) {
		f.options.DropAuthor = len(values) > 0
		if !f.options.DropAuthor && f.omitAlbumCaptions == nil {
			f.options.DropCaptions = false
			f.dropCaptions.SetValues(nil)
		}
	})
	if f.options.DropAuthor {
		f.dropAuthor.Disable()
	}
	var captions []string
	if f.omitAlbumCaptions != nil {
		captions = []string{"captions"}
	}
	f.dropCaptions = checkbox.NewCheckboxes([]string{"captions"}, captions, func(values []string) {
		f.options.DropCaptions = f.omitAlbumCaptions == nil && f.options.DropAuthor && f.hasMedia && len(values) > 0
	})
	f.modal.back = func() bool {
		if f.confirming {
			f.confirming = false
			f.modal.field = &f.search
			f.modal.focus = true
			return true
		}
		return false
	}
	f.reset()
	f.modal.field = &f.search
	f.modal.Open()
	gtx.Execute(op.InvalidateCmd{})
}

// Telegram removes attribution when it strips captions. If this applies
// to every item, the confirmation can only send copies.
func (f *forwardPicker) mustDropAuthor() bool {
	return len(f.ids) > 0 && len(f.omitAlbumCaptions) == len(f.ids)
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
			if f.goToMessage && f.navigate != nil {
				f.navigate(f.target)
			}
		}
		return err, true
	default:
		return nil, false
	}
}

func (f *forwardPicker) recipients(gtx layout.Context, chats []model.Chat, rights model.RightsSource, l localization.Catalog) []model.Chat {
	f.filters = f.filters[:0]
	if f.folders != nil {
		for _, folder := range f.folders() {
			title := folder.Title
			if folder.ID == 0 {
				title = l.T("nav.all")
			}
			f.filters = append(f.filters, pickerFilter{id: folder.ID, title: title, match: folder.Contains})
		}
	}
	return f.targets(gtx, chats, rights, l)
}

// layout draws the picker when it is open, of targets, and reports the
// chat clicked.
func (f *forwardPicker) layout(gtx layout.Context, l localization.Catalog, targets []model.Chat, avatar avatarLayout) (model.Chat, bool) {
	if !f.modal.Shown() {
		return model.Chat{}, false
	}
	if f.confirming {
		return f.layoutConfirmation(gtx, l)
	}
	if !f.busy {
		if f.cancel.Clicked(gtx) {
			f.modal.Close()
		}
		if c, ok := f.clicked(gtx, targets); ok {
			f.target, f.confirming = c, true
			f.modal.field, f.modal.focus = nil, true
		}
	}
	if f.confirming {
		return f.layoutConfirmation(gtx, l)
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
					return f.searchField(gtx, l)
				}),
				vspace(8),
				layout.Rigid(f.layoutFilters),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					if f.busy {
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return f.loader.sized(gtx, l, 32)
						})
					}
					return f.layoutRows(gtx, l, targets, avatar)
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
	return model.Chat{}, false
}

// layoutConfirmation asks before sending, keeping the picker and its search.
func (f *forwardPicker) layoutConfirmation(gtx layout.Context, l localization.Catalog) (model.Chat, bool) {
	input := gtx
	if f.busy {
		input = input.Disabled()
	}
	f.dropAuthor.Update(input)
	canDropCaptions := f.omitAlbumCaptions == nil && f.allowOptions && f.hasMedia && f.options.DropAuthor
	if canDropCaptions {
		f.dropCaptions.Enable()
		f.dropCaptions.Update(input)
	} else {
		f.dropCaptions.Disable()
		f.dropCaptions.Update(input.Disabled())
	}
	f.goTo.Update(input)
	if !f.busy {
		if f.back.Clicked(gtx) {
			f.modal.back()
		}
		if f.confirm.Clicked(gtx) && f.confirming {
			return f.target, true
		}
	}
	sc := scheme(gtx)
	f.modal.Layout(gtx, f.busy, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(420))
		return f.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.Format("history.forward_confirm", map[string]string{"chat": l.ChatTitle(f.target)}), token.TypestyleTitleMedium, sc.Surface.OnColor, 0)
				}),
				vspace(16),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if !f.allowOptions {
						return layout.Dimensions{}
					}
					if f.busy {
						gtx = gtx.Disabled()
					}
					return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if f.mustDropAuthor() {
								defer paint.PushOpacity(gtx.Ops, .38).Pop()
							}
							return f.dropAuthor.Layout(gtx, map[string]string{"author": l.T("history.forward_no_author")})
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if !canDropCaptions {
								defer paint.PushOpacity(gtx.Ops, .38).Pop()
							}
							return f.dropCaptions.Layout(gtx, map[string]string{"captions": l.T("history.forward_no_caption")})
						}),
					)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if f.navigate == nil {
						return layout.Dimensions{}
					}
					if f.busy {
						gtx = gtx.Disabled()
					}
					key := "history.forward_go_to"
					if f.options.DropAuthor {
						key = "history.forward_go_to_sent"
					}
					return f.goTo.Layout(gtx, map[string]string{"go": l.T(key)})
				}),
				vspace(16),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if f.busy {
						return f.loader.centered(gtx, l, 24)
					}
					text := l.T("history.select_forward")
					if f.options.DropAuthor {
						text = l.T("composer.send")
					}
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Spacing: layout.SpaceStart, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return textButton(gtx, &f.back, l.T("history.forward_back"))
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &f.confirm, text) }),
					)
				}),
			)
		}, defaultCardPadding)
	})
	return model.Chat{}, false
}

func forwardWithOptions(ctx context.Context, source model.MessageForwarder, from int64, ids []model.MessageID, to int64, options model.ForwardOptions) error {
	if s, ok := source.(model.MessageForwarderWithOptions); ok {
		return s.ForwardMessagesWithOptions(ctx, from, ids, to, options)
	}
	if options.DropAuthor || options.DropCaptions || len(options.DropAlbumCaptions) > 0 {
		return errors.New("forwarding options are not supported")
	}
	return source.ForwardMessages(ctx, from, ids, to)
}

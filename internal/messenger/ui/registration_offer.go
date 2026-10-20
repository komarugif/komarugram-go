// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"strconv"
	"time"

	"gio-mw/token"
	"gio-mw/widget/button"
	"gio-mw/widget/checkbox"

	"gioui.org/layout"
	"gioui.org/op"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// registrationOfferDelay is how long after the window opens the points are
// first looked for, the chat list loaded by then; registrationOfferEvery,
// how often after that.
const (
	registrationOfferDelay = 20 * time.Second
	registrationOfferEvery = 30 * time.Minute
)

// registrationOffer asks the user to send the registration points the
// account's chats tell (model.RegistrationPointSource) to the maintainers
// of the table the info estimates registrations by. It asks when there are
// RegistrationPointsMin points the user was not asked about; the points can
// be sent only once the user ticked the warning that they tell who the
// user's contacts are.
type registrationOffer struct {
	source model.RegistrationPointSource
	modal  modal
	agree  *checkbox.Checkboxes[string]
	send   *button.Button
	later  *button.Button
	never  *button.Button
	// next is when the points are looked for again; zero while they are.
	next   time.Time
	found  chan registrationFound
	points []model.RegistrationPoint
	// done tells what came of sending.
	done chan error
}

type registrationFound struct {
	points  []model.RegistrationPoint
	unasked int
	never   bool
}

func newRegistrationOffer(source model.RegistrationPointSource) *registrationOffer {
	o := &registrationOffer{source: source, send: button.Filled(), later: button.Text(), never: button.Text(),
		found: make(chan registrationFound, 1), done: make(chan error, 1), next: time.Now().Add(registrationOfferDelay)}
	o.agree = checkbox.NewCheckboxes([]string{"agree"}, nil, func([]string) {})
	return o
}

// Update looks for the points when it is time, opens the dialog when there
// are enough, and answers it; toast tells what came of sending.
func (o *registrationOffer) Update(gtx layout.Context, toast func(string), l localization.Catalog, invalidate func()) {
	select {
	case err := <-o.done:
		if err != nil {
			toast(l.T("registration_points.failed") + ": " + mediaErrorText(err))
		} else {
			toast(l.T("registration_points.sent"))
		}
	default:
	}
	select {
	case f := <-o.found:
		o.next = time.Now().Add(registrationOfferEvery)
		if !f.never && f.unasked >= model.RegistrationPointsMin && !o.modal.Shown() {
			o.points = f.points
			o.agree.SetValues(nil)
			o.modal.Open()
		}
	default:
	}
	if !o.next.IsZero() && !o.modal.Shown() {
		if wait := time.Until(o.next); wait > 0 {
			gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(wait)})
		} else {
			o.next = time.Time{}
			go func() {
				defer crash.Recover("registration points", nil)
				ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
				defer cancel()
				points, unasked, never, err := o.source.RegistrationPoints(ctx)
				if err != nil {
					// Asked again at the next turn.
					never, unasked = false, 0
				}
				o.found <- registrationFound{points, unasked, never}
				invalidate()
			}()
		}
	}
	if !o.modal.Shown() {
		return
	}
	o.agree.Update(gtx)
	if len(o.agree.GetValues()) == 1 {
		o.send.Enable()
	} else {
		o.send.Disable()
	}
	if o.send.Clicked(gtx) {
		o.sendPoints(invalidate)
	}
	if o.later.Clicked(gtx) {
		o.answer(false)
	}
	if o.never.Clicked(gtx) {
		o.answer(true)
	}
}

// sendPoints sends the points asked about and closes the dialog, but only
// once the user ticked the warning: the button is disabled until then, and
// this is the check that holds whatever the button does.
func (o *registrationOffer) sendPoints(invalidate func()) {
	if len(o.agree.GetValues()) != 1 || o.points == nil {
		return
	}
	points := o.points
	o.points = nil
	o.modal.Close()
	go func() {
		defer crash.Recover("registration points send", nil)
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		o.done <- o.source.SendRegistrationPoints(ctx, points)
		invalidate()
	}()
}

// answer closes the dialog without sending: the points are asked about
// again only with new ones, never again with never.
func (o *registrationOffer) answer(never bool) {
	points := o.points
	o.points = nil
	o.modal.Close()
	go func() {
		defer crash.Recover("registration points postpone", nil)
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = o.source.PostponeRegistrationPoints(ctx, points, never)
	}()
}

func (o *registrationOffer) Layout(gtx layout.Context, l localization.Catalog) {
	if !o.modal.Shown() {
		return
	}
	sc := scheme(gtx)
	body := l.Format("registration_points.body", map[string]string{"count": strconv.Itoa(len(o.points)), "user": "@" + model.RegistrationPointsRecipient})
	shown := o.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(480))
		return o.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("registration_points.title"), token.TypestyleTitleMedium, sc.Surface.OnColor, 2)
				}),
				vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, body, token.TypestyleBodyMedium, sc.Surface.OnColor, 0)
				}),
				vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("registration_points.warning"), token.TypestyleBodyMedium, sc.Error.Color, 0)
				}),
				vspace(8),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return o.agree.Layout(gtx, map[string]string{"agree": l.T("registration_points.agree")})
				}),
				vspace(16),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Spacing: layout.SpaceStart, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return o.never.Layout(gtx, l.T("registration_points.never"))
						}),
						layout.Rigid(layout.Spacer{Width: 8}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return o.later.Layout(gtx, l.T("registration_points.later"))
						}),
						layout.Rigid(layout.Spacer{Width: 8}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return o.send.Layout(gtx, l.T("registration_points.send"))
						}),
					)
				}),
			)
		}, defaultCardPadding)
	})
	if !shown && o.points != nil {
		// Escape or a click beside the dialog answers "not now".
		o.answer(false)
	}
}

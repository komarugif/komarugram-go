// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"time"

	"gio-mw/token"
	"gioui.org/layout"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

type membershipOperation int

const (
	membershipPrepare membershipOperation = iota
	membershipJoin
	membershipLeave
)

type membershipResult struct {
	operation membershipOperation
	plan      model.LeavePlan
	err       error
}
type membershipControl struct {
	modal                  modal
	chat                   int64
	busy, ready            bool
	broadcast              bool
	plan                   model.LeavePlan
	results                chan membershipResult
	cancel, confirm, retry surface
	loader                 loadingIndicator
}

func (p *chatPage) membershipSource() model.MembershipStore {
	s, _ := p.source.(model.MembershipStore)
	return s
}
func (p *chatPage) canLeaveChat() bool {
	s := p.membershipSource()
	if s == nil || p.threadRoot != 0 || p.frozen.Frozen() {
		return false
	}
	m := s.Membership(p.chat)
	return m.Known && !m.Left
}
func (p *chatPage) startMembership(chat int64, operation membershipOperation) {
	d := &p.membership
	source := p.membershipSource()
	if source == nil || d.busy {
		return
	}
	d.chat, d.busy = chat, true
	d.broadcast = source.Membership(chat).Broadcast
	d.results = make(chan membershipResult, 1)
	results, confirmed := d.results, d.plan
	ctx := context.Background()
	if p.composer != nil {
		ctx = p.composer.ctx
	}
	go func() {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		result := membershipResult{operation: operation}
		switch operation {
		case membershipJoin:
			result.err = source.JoinChat(ctx, chat)
		case membershipPrepare:
			result.plan, result.err = source.PrepareLeave(ctx, chat)
		case membershipLeave:
			result.err = source.LeaveChat(ctx, chat, confirmed)
		}
		results <- result
		p.invalidate()
	}()
}
func (p *chatPage) askLeave() {
	if !p.canLeaveChat() || p.membership.busy {
		return
	}
	d := &p.membership
	d.ready = false
	d.plan = model.LeavePlan{}
	d.modal.Open()
	p.startMembership(p.chat, membershipPrepare)
}
func membershipError(err error, l localization.Catalog) string {
	switch {
	case errors.Is(err, model.ErrJoinVerification):
		return l.T("membership.verification")
	case errors.Is(err, model.ErrJoinRequested):
		return l.T("membership.request_sent")
	case errors.Is(err, model.ErrLeavePlanChanged):
		return l.T("membership.changed")
	case errors.Is(err, model.ErrOwnerCannotLeave):
		return l.T("membership.owner_blocked")
	default:
		return mediaErrorText(err)
	}
}
func (p *chatPage) updateMembership(chat int64, l localization.Catalog) {
	d := &p.membership
	if d.chat != chat {
		d.modal.Hide()
	}
	if d.results == nil {
		return
	}
	select {
	case result := <-d.results:
		d.busy = false
		d.results = nil
		if d.chat != chat {
			return
		}
		if result.err != nil {
			if d.modal.Shown() {
				d.modal.Toast(membershipError(result.err, l))
			} else {
				p.toast.Show(membershipError(result.err, l))
			}
			if errors.Is(result.err, model.ErrLeavePlanChanged) {
				d.ready = false
			}
			return
		}
		switch result.operation {
		case membershipPrepare:
			d.plan = result.plan
			d.ready = true
		case membershipLeave:
			d.modal.Close()
			key := "membership.left_group"
			if d.broadcast {
				key = "membership.left_channel"
			}
			if p.membershipNotice != nil {
				p.membershipNotice(l.T(key))
			} else {
				p.toast.Show(l.T(key))
			}
		}
	default:
	}
}
func leaveExplanation(plan model.LeavePlan, l localization.Catalog) string {
	if plan.SuccessorID == 0 {
		return ""
	}
	key := "membership.successor"
	if plan.BasicGroup {
		key += "_basic"
	}
	return l.Format(key, map[string]string{"user": plan.SuccessorName})
}
func (p *chatPage) membershipDialog(gtx layout.Context, l localization.Catalog) {
	d := &p.membership
	if !d.modal.Shown() {
		return
	}
	if !d.busy {
		if d.cancel.Clicked(gtx) {
			d.modal.Close()
		}
		if d.ready && d.confirm.Clicked(gtx) {
			p.startMembership(d.chat, membershipLeave)
		}
		if !d.ready && d.retry.Clicked(gtx) {
			p.startMembership(d.chat, membershipPrepare)
		}
	}
	d.modal.Layout(gtx, d.busy, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(440))
		return d.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			key := "membership.sure_group"
			if d.broadcast {
				key = "membership.sure_channel"
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T(key), token.TypestyleTitleMedium, scheme(gtx).Surface.OnColor, 0)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					text := leaveExplanation(d.plan, l)
					if text == "" {
						return layout.Dimensions{}
					}
					return layout.Inset{Top: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return label(gtx, text, token.TypestyleBodyMedium, scheme(gtx).Surface.OnColor, 0)
					})
				}),
				vspace(16),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if d.busy {
						return d.loader.centered(gtx, l, 24)
					}
					return layout.Flex{Spacing: layout.SpaceStart}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &d.cancel, l.T("history.cancel")) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if !d.ready {
								return textButton(gtx, &d.retry, l.T("composer.retry"))
							}
							return textButtonColor(gtx, &d.confirm, l.T("membership.leave"), scheme(gtx).Error.Color)
						}),
					)
				}),
			)
		}, defaultCardPadding)
	})
}

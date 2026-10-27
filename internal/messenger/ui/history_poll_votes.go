// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"strconv"
	"time"

	"gio-mw/token"
	"gio-mw/widget/scroll"

	"gioui.org/layout"
	"gioui.org/unit"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

const (
	// pollVotesFirst is how many of who voted for an answer the dialog
	// shows at first, and pollVotesMore how many more each "Show more"
	// asks for, as Telegram Desktop's poll results box does.
	pollVotesFirst = 10
	pollVotesMore  = 50
)

// pollVotesDialog lists who voted for each answer of a public poll, as
// Telegram Desktop's results box does: a section an answer, with its share
// and its voters, and a button for more of them.
type pollVotesDialog struct {
	modal   modal
	msg     model.Message
	answers []pollVotesList
	close   surface
	list    scroll.List
	loader  loadingIndicator
	ctx     context.Context
	cancel  context.CancelFunc
}

// pollVotesList is who voted for an answer, as far as its pages have come.
type pollVotesList struct {
	answer  model.PollAnswer
	percent int
	items   []model.PollVote
	rows    []surface
	more    surface
	next    string
	asked   bool
	done    bool
	failed  bool
	loading bool
	results chan pollVotesResult
}

type pollVotesResult struct {
	page model.PollVotesPage
	err  error
}

// open shows who voted in the poll of m.
func (d *pollVotesDialog) open(p *chatPage, m model.Message) {
	d.stop()
	d.msg = m
	percents := m.Poll.Percents()
	for i, a := range m.Poll.Answers {
		if a.Voters == 0 {
			continue
		}
		l := pollVotesList{answer: a}
		if percents != nil {
			l.percent = percents[i]
		}
		d.answers = append(d.answers, l)
	}
	d.ctx, d.cancel = context.WithCancel(context.Background())
	d.modal.Open()
	p.invalidate()
}

func (d *pollVotesDialog) stop() {
	if d.cancel != nil {
		d.cancel()
	}
	*d = pollVotesDialog{}
}

// fetch asks for the next page of who voted for answer i.
func (d *pollVotesDialog) fetch(p *chatPage, i int) {
	store, ok := p.source.(model.PollStore)
	l := &d.answers[i]
	if !ok || l.loading || l.done || l.failed {
		return
	}
	limit := pollVotesMore
	if !l.asked {
		limit = pollVotesFirst
	}
	l.asked, l.loading = true, true
	l.results = make(chan pollVotesResult, 1)
	results, msg, option, offset, ctx := l.results, d.msg, l.answer.Option, l.next, d.ctx
	go func() {
		defer crash.Recover("poll votes", func(e *crash.Panic) { results <- pollVotesResult{err: e}; p.invalidate() })
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		page, err := store.PollVotes(ctx, msg, option, offset, limit)
		results <- pollVotesResult{page, err}
		p.invalidate()
	}()
}

// update takes the pages that came; a page that did not is told in the
// dialog's toast.
func (d *pollVotesDialog) update(loc localization.Catalog) {
	for i := range d.answers {
		l := &d.answers[i]
		if l.results == nil {
			continue
		}
		select {
		case r := <-l.results:
			l.loading, l.results = false, nil
			if r.err != nil {
				l.failed = true
				d.modal.Toast(loc.T("poll.votes_failed"))
				continue
			}
			l.items = append(l.items, r.page.List...)
			l.next = r.page.Next
			l.done = r.page.Next == "" || len(r.page.List) == 0
		default:
		}
	}
}

func (d *pollVotesDialog) layout(gtx layout.Context, p *chatPage, l localization.Catalog) {
	if !d.modal.Shown() {
		return
	}
	d.update(l)
	if !d.modal.closing {
		if d.close.Clicked(gtx) {
			d.modal.Close()
		}
		for i := range d.answers {
			a := &d.answers[i]
			if a.more.Clicked(gtx) {
				a.failed = false
				d.fetch(p, i)
			}
		}
	}
	title := l.T("poll.results_title")
	if d.msg.Poll != nil && d.msg.Poll.Quiz {
		title = l.T("poll.quiz_results_title")
	}
	shown := d.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		width := min(gtx.Constraints.Max.X, gtx.Dp(420))
		height := min(gtx.Constraints.Max.Y, gtx.Dp(560))
		gtx.Constraints = layout.Exact(image.Pt(width, height))
		return d.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			sc := scheme(gtx)
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, title, token.TypestyleTitleLarge, sc.Surface.OnColor, 1)
				}),
				vspace(4),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, d.msg.Poll.Question, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
				}),
				vspace(8),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return d.layoutList(gtx, p, l)
				}),
				vspace(8),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return textButton(gtx, &d.close, l.T("stickers.close"))
					})
				}),
			)
		}, defaultCardPadding)
	})
	if !shown {
		d.stop()
	}
}

// ready asks for the first page of who voted for each answer, all at once,
// and tells whether all of them came, or failed.
func (d *pollVotesDialog) ready(p *chatPage) bool {
	ready := true
	for i := range d.answers {
		a := &d.answers[i]
		if !a.asked {
			d.fetch(p, i)
		}
		if a.loading && len(a.items) == 0 {
			ready = false
		}
	}
	return ready
}

// pollVotesRow is a row of the dialog's list: the head of answer's
// section, one who voted for it, or its button for more.
type pollVotesRow struct {
	answer, voter int
	head, more    bool
}

// layoutList draws the sections of the answers once the first page of each
// came, one ring in the middle until then: the list comes in whole.
func (d *pollVotesDialog) layoutList(gtx layout.Context, p *chatPage, l localization.Catalog) layout.Dimensions {
	if !d.ready(p) {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return d.loader.sized(gtx, l, 32)
		})
	}
	var rows []pollVotesRow
	for i := range d.answers {
		a := &d.answers[i]
		rows = append(rows, pollVotesRow{answer: i, head: true})
		for j := range a.items {
			rows = append(rows, pollVotesRow{answer: i, voter: j})
		}
		for len(a.rows) < len(a.items) {
			a.rows = append(a.rows, surface{})
		}
		if !a.done {
			rows = append(rows, pollVotesRow{answer: i, more: true})
		}
	}
	d.list.Axis = layout.Vertical
	return d.list.Layout(gtx, len(rows), func(gtx layout.Context, i int) layout.Dimensions {
		row := rows[i]
		a := &d.answers[row.answer]
		switch {
		case row.head:
			return d.head(gtx, a, i > 0, l)
		case row.more:
			return d.moreRow(gtx, a, l)
		}
		return d.voter(gtx, p, &a.rows[row.voter], a.items[row.voter])
	})
}

// head draws the head of an answer's section: its text, its share and how
// many voted for it.
func (d *pollVotesDialog) head(gtx layout.Context, a *pollVotesList, gap bool, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	top := unit.Dp(0)
	if gap {
		top = 12
	}
	return layout.Inset{Top: top, Bottom: 4}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Baseline}.Layout(gtx,
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, a.answer.Text+" — "+strconv.Itoa(a.percent)+"%", token.TypestyleLabelLargeEmphasized, sc.Primary.Color, 0)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.Inset{Left: 8}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return label(gtx, pollVoters(d.msg.Poll, a.answer.Voters, l), token.TypestyleLabelMedium, sc.SurfaceVariant.OnColor, 1)
				})
			}),
		)
	})
}

// moreRow draws the button that asks for more of who voted for an answer,
// or the ring while they load.
func (d *pollVotesDialog) moreRow(gtx layout.Context, a *pollVotesList, l localization.Catalog) layout.Dimensions {
	if a.loading {
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.UniformInset(8).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return d.loader.sized(gtx, l, 24)
			})
		})
	}
	text := l.Count("poll.show_more", max(a.answer.Voters-len(a.items), 1), nil)
	if a.failed {
		text = l.T("history.retry")
	}
	return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return textButton(gtx, &a.more, text)
	})
}

// voter draws one who voted: their avatar and name. A click opens their
// chat, as in the dialog of who reacted, and closes the dialog.
func (d *pollVotesDialog) voter(gtx layout.Context, p *chatPage, s *surface, v model.PollVote) layout.Dimensions {
	sc := scheme(gtx)
	size := image.Pt(gtx.Constraints.Max.X, gtx.Dp(reactedRow))
	content := func(gtx layout.Context) layout.Dimensions {
		const avatarSize = 36
		offset(gtx, image.Pt(gtx.Dp(4), (size.Y-gtx.Dp(avatarSize))/2), func(gtx layout.Context) layout.Dimensions {
			return p.peerAvatar(gtx, v.ID, v.Name, avatarSize)
		})
		textX := gtx.Dp(4 + avatarSize + 12)
		textGtx := gtx
		textGtx.Constraints = layout.Constraints{Max: image.Pt(max(size.X-textX-gtx.Dp(8), 0), size.Y)}
		offset(textGtx, image.Pt(textX, (size.Y-gtx.Sp(20))/2), func(gtx layout.Context) layout.Dimensions {
			return label(gtx, v.Name, token.TypestyleBodyLarge, sc.Surface.OnColor, 1)
		})
		return layout.Dimensions{Size: size}
	}
	if p.openSender == nil || v.ID == 0 {
		return content(gtx)
	}
	if s.Clicked(gtx) && !d.modal.closing {
		p.openSender(reactedSender(d.msg, model.Reacted{PeerID: v.ID, Name: v.Name}))
		d.modal.Close()
	}
	style := surfaceStyle{radius: gtx.Dp(12), background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor}
	return s.Layout(gtx, size, style, content)
}

// pollStop asks before the account closes its poll for good, as Telegram
// Desktop does.
type pollStop struct {
	modal        modal
	msg          model.Message
	stop, cancel surface
}

func (s *pollStop) ask(m model.Message) {
	s.msg = m
	s.modal.Open()
}

func (s *pollStop) layout(gtx layout.Context, p *chatPage, l localization.Catalog) {
	if !s.modal.Shown() {
		return
	}
	if !s.modal.closing {
		if s.cancel.Clicked(gtx) {
			s.modal.Close()
		}
		if s.stop.Clicked(gtx) {
			if store, ok := p.source.(model.PollStore); ok {
				store.ClosePoll(s.msg, p.reportMedia)
			}
			s.modal.Close()
		}
	}
	s.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(360))
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return s.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			sc := scheme(gtx)
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("poll.stop_warning"), token.TypestyleBodyLarge, sc.Surface.OnColor, 0)
				}),
				vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return layout.Flex{}.Layout(gtx,
							layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &s.cancel, l.T("history.cancel")) }),
							layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								return textButtonColor(gtx, &s.stop, l.T("poll.stop_sure"), sc.Error.Color)
							}),
						)
					})
				}),
			)
		}, defaultCardPadding)
	})
}

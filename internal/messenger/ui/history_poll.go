// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"fmt"
	"image"
	"slices"
	"strconv"
	"time"

	"gio-mw/token"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"
	"gioui.org/op/paint"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// Polls and checklists in the history, as Telegram Desktop draws them
// (history_view_poll.cpp, history_view_todo_list.cpp): the question and the
// answers, which a click votes for until the results show, with a bar and
// a share each; the title of a checklist and its tasks, which a click marks
// done where the list allows it.

const (
	// pollWidth is the width a poll or a checklist takes in its bubble, at
	// most: near Telegram Desktop's msgMaxWidth, which a poll takes, so that
	// what is centered under it is centered in the bubble.
	pollWidth = 420
	// pollMark is the width of the column of the answers' shares, and
	// pollRadioMark of their circles and the tasks', before the results.
	pollMark      = 48
	pollRadioMark = 36
	// pollRadioInset is how far the circles stand from the start of the
	// row, which with pollBleed keeps them as far inside the highlight as
	// the proxies' radio buttons are in theirs.
	pollRadioInset = 4
	// pollBleed is how far the highlight of an answer or a task reaches
	// past its text on each side, into the bubble's padding.
	pollBleed = 8
	// pollRadio is the diameter of an answer's or a task's circle.
	pollRadio = 20
)

// pollCorrect is the color of a quiz's right answer, for the light and the
// dark themes, as strengthColors are: the theme has no color for it. A wrong
// one is in the theme's error color.
var pollCorrect = [2]token.MatColor{token.NewMatColorFromHexRGB(0x2E9E4F), token.NewMatColorFromHexRGB(0x6FCF8E)}

// pollState is what the page keeps of a poll or a checklist between
// frames: its row is made anew whenever the message changes, which a vote
// of anyone does.
type pollState struct {
	answers []surface
	// selected are the options chosen in a poll of several answers, not
	// sent yet.
	selected [][]byte
	// footer is the button under the answers: the vote, the results, the
	// votes.
	footer surface
	// solution is the button that shows a quiz's explanation, and
	// showSolution whether it is shown.
	solution     surface
	showSolution bool
	// authorResults is set while the author of a poll they did not vote
	// in looks at its results.
	authorResults bool
	// sent is set when a vote went from here, until the poll has it.
	sent bool
	// reveal is how far the results are shown, 0 before them and 1 with
	// them, which moves the answers between the two; fills are the shares
	// of the answers' bars, which move when the votes change.
	reveal wdk.FloatTween
	fills  []wdk.FloatTween
	// revealed is reveal as the last frame drew it.
	revealed float32
	// tasks are the rows of a checklist's tasks.
	tasks []surface
}

// pollStateOf is the state of the poll or checklist of message id.
func (p *chatPage) pollStateOf(id model.MessageID) *pollState {
	if p.polls == nil {
		p.polls = map[model.MessageID]*pollState{}
	}
	s := p.polls[id]
	if s == nil {
		s = &pollState{}
		p.polls[id] = s
	}
	return s
}

// pollColor is the color of an answer's bar and mark: a quiz's right
// answer green, the account's wrong one red, the others the primary.
func pollColor(gtx layout.Context, poll *model.Poll, a model.PollAnswer) token.MatColor {
	sc := scheme(gtx)
	if poll.Quiz {
		switch {
		case a.Correct:
			if isDark(gtx) {
				return pollCorrect[1]
			}
			return pollCorrect[0]
		case a.Chosen:
			return sc.Error.Color
		}
	}
	return sc.Primary.Color
}

// pollSubtitle names the kind of poll, as the line under its question does.
func pollSubtitle(poll *model.Poll, l localization.Catalog) string {
	switch {
	case poll.Closed:
		return l.T("poll.closed")
	case poll.Quiz && poll.Public:
		return l.T("poll.public_quiz")
	case poll.Quiz:
		return l.T("poll.anonymous_quiz")
	case poll.Public:
		return l.T("poll.public")
	}
	return l.T("poll.anonymous")
}

// pollVoters words how many voted, or answered a quiz.
func pollVoters(poll *model.Poll, n int, l localization.Catalog) string {
	switch {
	case n == 0 && poll.Quiz:
		return l.T("poll.answers_none")
	case n == 0:
		return l.T("poll.votes_none")
	case poll.Quiz:
		return l.Count("poll.answers", n, nil)
	}
	return l.Count("poll.count", n, nil)
}

// pollAuthorWaits tells whether m is the account's own poll, open, which it
// did not vote in: its author may look at the results first.
func pollAuthorWaits(m *model.Message) bool {
	return m.Outgoing && !m.Poll.Voted() && !m.Poll.Closed
}

// pollFooter is what is under a poll's answers, as Telegram Desktop's
// Poll::Footer chooses it: a button, or how many voted.
type pollFooter int

const (
	footerLabel pollFooter = iota
	footerVote
	footerResults
	footerAuthorVotes
	footerAuthorBack
)

func pollFooterOf(m *model.Message, st *pollState, canVote bool) pollFooter {
	poll := m.Poll
	show := poll.ShowResults() || st.authorResults
	switch {
	case pollAuthorWaits(m) && !st.authorResults && !(canVote && poll.Multiple && len(st.selected) > 0):
		if poll.Total > 0 {
			return footerAuthorVotes
		}
		return footerLabel
	case pollAuthorWaits(m) && st.authorResults:
		return footerAuthorBack
	}
	// Telegram Desktop's Poll::showVotersCount.
	var count bool
	switch {
	case poll.Voted() && !show:
		count = !poll.Public
	case show:
		count = poll.Total == 0 || !poll.Public
	default:
		count = !poll.Multiple
	}
	switch {
	case count:
		return footerLabel
	case show || poll.Voted() && poll.Public:
		return footerResults
	}
	return footerVote
}

// sendVote votes for options in the poll of m.
func (p *chatPage) sendVote(m model.Message, st *pollState, options [][]byte, l localization.Catalog) {
	store, ok := p.source.(model.PollStore)
	if !ok {
		return
	}
	st.sent = true
	store.Vote(m, options, p.reportMedia)
	if poll := m.Poll; poll.HideResults && !poll.Closed && !poll.Creator {
		p.toast.Show(l.T("poll.results_after_close"))
	}
}

// pollUpdate takes the clicks on the poll of m, before it is drawn.
func (p *chatPage) pollUpdate(gtx layout.Context, m *model.Message, st *pollState, canVote bool, l localization.Catalog) {
	poll := m.Poll
	if st.sent && poll.Voted() {
		// The vote came back: a quiz answered wrong explains itself.
		st.sent = false
		if poll.Quiz && poll.Solution != "" {
			for _, a := range poll.Answers {
				if a.Chosen && !a.Correct {
					st.showSolution = true
				}
			}
		}
	}
	if poll.Voted() || poll.Closed {
		st.selected = nil
	}
	for len(st.answers) < len(poll.Answers) {
		st.answers = append(st.answers, surface{})
	}
	show := poll.ShowResults() || st.authorResults
	for i, a := range poll.Answers {
		if !st.answers[i].Clicked(gtx) {
			continue
		}
		switch {
		case canVote && poll.Multiple:
			if j := slices.IndexFunc(st.selected, func(o []byte) bool { return bytes.Equal(o, a.Option) }); j >= 0 {
				st.selected = slices.Delete(st.selected, j, j+1)
			} else {
				st.selected = append(st.selected, a.Option)
			}
		case canVote:
			p.sendVote(*m, st, [][]byte{a.Option}, l)
		case show:
			p.toast.Show(pollVoters(poll, a.Voters, l))
		}
	}
	if st.solution.Clicked(gtx) {
		st.showSolution = !st.showSolution
	}
	if st.footer.Clicked(gtx) {
		switch pollFooterOf(m, st, canVote) {
		case footerVote:
			if canVote && len(st.selected) > 0 {
				p.sendVote(*m, st, st.selected, l)
			}
		case footerResults:
			p.pollVotes.open(p, *m)
		case footerAuthorVotes:
			st.authorResults = true
		case footerAuthorBack:
			st.authorResults = false
		}
	}
}

// pollLayout draws the poll of m: its question and kind, its answers, and
// what is under them.
func (p *chatPage) pollLayout(gtx layout.Context, m *model.Message, l localization.Catalog) layout.Dimensions {
	poll := m.Poll
	st := p.pollStateOf(m.Key.MessageID)
	_, canStore := p.source.(model.PollStore)
	canVote := canStore && m.Key.MessageID > 0 && !m.Local() && poll.CanVote() && !st.authorResults
	p.pollUpdate(gtx, m, st, canVote, l)
	show := poll.ShowResults() || st.authorResults
	percents := poll.Percents()
	target := float32(0)
	if show {
		target = 1
	}
	st.reveal.Duration = token.DurationMedium4
	reveal := st.reveal.Animate(gtx, target)
	st.revealed = reveal
	for len(st.fills) < len(poll.Answers) {
		st.fills = append(st.fills, wdk.FloatTween{Duration: token.DurationMedium4})
	}
	sc := scheme(gtx)
	gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(pollWidth))
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, poll.Question, token.TypestyleTitleMedium, sc.Surface.OnColor, 0)
		}),
		vspace(2),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.pollHeader(gtx, poll, st, show, l)
		}),
	}
	if st.showSolution && show && poll.Solution != "" {
		rows = append(rows, vspace(6), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return pollSolution(gtx, poll.Solution, l)
		}))
	}
	rows = append(rows, vspace(6))
	for i := range poll.Answers {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			percent := 0
			if percents != nil {
				percent = percents[i]
			}
			fill := st.fills[i].Animate(gtx, float32(percent)/100)
			return p.pollAnswer(gtx, poll, &st.answers[i], poll.Answers[i], st, canVote, reveal, percents != nil, percent, fill)
		}))
	}
	rows = append(rows, vspace(4), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return p.pollFooterLayout(gtx, m, st, canVote, l)
	}))
	if !poll.CloseDate.IsZero() && !poll.Closed {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			text := pollTimer(gtx, poll, l)
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, text, token.TypestyleLabelSmall, sc.SurfaceVariant.OnColor, 1)
			})
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// pollTimer words how long the poll has before it closes, and asks for the
// frame where that changes.
func pollTimer(gtx layout.Context, poll *model.Poll, l localization.Catalog) string {
	left := max(poll.CloseDate.Sub(gtx.Now), 0)
	if days := int(left / (24 * time.Hour)); days > 0 {
		gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(left - time.Duration(days)*24*time.Hour + time.Second)})
		key := "poll.ends_days"
		if poll.HideResults {
			key = "poll.results_days"
		}
		return l.Count(key, days, nil)
	}
	gtx.Execute(op.InvalidateCmd{At: gtx.Now.Add(left%time.Second + time.Millisecond)})
	s := int(left / time.Second)
	timer := fmt.Sprintf("%d:%02d", s/60%60, s%60)
	if s >= 3600 {
		timer = fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	key := "poll.ends_time"
	if poll.HideResults {
		key = "poll.results_time"
	}
	return l.Format(key, map[string]string{"time": timer})
}

// pollHeader draws the line under the question: the kind of poll, some of
// who voted in a public one, and the button of a quiz's explanation.
func (p *chatPage) pollHeader(gtx layout.Context, poll *model.Poll, st *pollState, show bool, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	// As high before the results as with them: the avatars of who voted
	// fit it, and the explanation's button is laid over it.
	height := max(lineHeight(gtx, token.TypestyleLabelMedium), gtx.Dp(18))
	width := gtx.Constraints.Max.X
	if poll.Quiz && show && poll.Solution != "" {
		size := image.Pt(gtx.Dp(28), gtx.Dp(28))
		offset(gtx, image.Pt(width-size.X, (height-size.Y)/2), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(size)
			col := sc.Primary.Color
			style := surfaceStyle{radius: size.Y / 2, background: col.SetOpacity(0), content: col, button: l.T("poll.solution")}
			return st.solution.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
				return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(20), gtx.Dp(20)))
					return iconSolution(gtx, col)
				})
			})
		})
	}
	gtx.Constraints = layout.Exact(image.Pt(max(width-gtx.Dp(32), 0), height))
	layout.Flex{Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, pollSubtitle(poll, l), token.TypestyleLabelMedium, sc.SurfaceVariant.OnColor, 1)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if !poll.Public || !show || len(poll.Recent) == 0 {
				return layout.Dimensions{}
			}
			return layout.Inset{Left: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.pollRecent(gtx, poll.Recent)
			})
		}),
	)
	return layout.Dimensions{Size: image.Pt(width, height)}
}

// pollRecent draws the avatars of some of who voted, each over the last.
func (p *chatPage) pollRecent(gtx layout.Context, voters []model.PollVoter) layout.Dimensions {
	const size, step = 18, 12
	n := min(len(voters), 3)
	for i := range n {
		v := voters[i]
		offset(gtx, image.Pt(gtx.Dp(unit.Dp(step*i)), 0), func(gtx layout.Context) layout.Dimensions {
			return p.peerAvatar(gtx, v.ID, v.Name, size)
		})
	}
	return layout.Dimensions{Size: image.Pt(gtx.Dp(unit.Dp(step*(n-1)+size)), gtx.Dp(size))}
}

// peerAvatar draws the avatar of a user or a chat id, named name.
func (p *chatPage) peerAvatar(gtx layout.Context, id int64, name string, size unit.Dp) layout.Dimensions {
	kind := model.KindUser
	if id < 0 {
		kind = model.KindGroup
	}
	if p.avatar != nil {
		return p.avatar(gtx, id, kind, name, size)
	}
	return avatar(gtx, id, kind, name, size)
}

// pollSolution draws a quiz's explanation, on a plate.
func pollSolution(gtx layout.Context, solution string, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	macro := op.Record(gtx.Ops)
	dims := layout.UniformInset(8).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.T("poll.solution"), token.TypestyleLabelLargeEmphasized, sc.Primary.Color, 1)
			}),
			vspace(2),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, solution, token.TypestyleBodyMedium, sc.Surface.OnColor, 0)
			}),
		)
	})
	call := macro.Stop()
	fillRounded(gtx, sc.Primary.Color.SetOpacity(.1), dims.Size, gtx.Dp(8))
	call.Add(gtx.Ops)
	return dims
}

// pollAnswer draws one answer: a circle to vote with until the results
// show, then its share, its bar, filled to fill, and whether the account
// chose it. reveal moves it from the one to the other: the text slides to
// its place over the bar, the bar grows, and the share, the bar and the
// mark fade in; the circle is there only at 0. Without animations reveal is
// 0 or 1.
func (p *chatPage) pollAnswer(gtx layout.Context, poll *model.Poll, s *surface, a model.PollAnswer, st *pollState, canVote bool, reveal float32, percents bool, percent int, fill float32) layout.Dimensions {
	sc := scheme(gtx)
	content := func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 6, Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			lerp := func(a, b int) int { return a + int(float32(b-a)*reveal+.5) }
			mark := gtx.Dp(pollMark)
			line := lineHeight(gtx, token.TypestyleBodyLarge)
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			// The text wraps as wide as the results leave it, before them
			// too, and the row keeps the room of the bar: a vote changes
			// neither its lines nor its height. Before the results the
			// text and the circle are centered in the row.
			d, thick, gap := gtx.Dp(14), gtx.Dp(4), gtx.Dp(8)
			bar := gap + thick/2 + (d+1)/2
			textGtx := gtx
			textGtx.Constraints = layout.Constraints{Max: image.Pt(max(gtx.Constraints.Max.X-mark, 0), gtx.Constraints.Max.Y)}
			macro := op.Record(gtx.Ops)
			dims := label(textGtx, a.Text, token.TypestyleBodyLarge, sc.Surface.OnColor, 0)
			text := macro.Stop()
			textH := max(dims.Size.Y, line)
			top := lerp(bar/2, 0)
			offset(gtx, image.Pt(lerp(gtx.Dp(pollRadioMark), mark), top), func(gtx layout.Context) layout.Dimensions {
				text.Add(gtx.Ops)
				return dims
			})
			if reveal == 0 {
				// The circle goes at once, and comes back at once: faded,
				// it crossed the share coming in.
				selected := slices.ContainsFunc(st.selected, func(o []byte) bool { return bytes.Equal(o, a.Option) })
				radio := gtx.Dp(pollRadio)
				offset(gtx, image.Pt(gtx.Dp(pollRadioInset), top+(line-radio)/2), func(gtx layout.Context) layout.Dimensions {
					return pollCircle(gtx, radio, selected, canVote)
				})
			}
			if reveal > 0 {
				defer paint.PushOpacity(gtx.Ops, reveal).Pop()
				if percents {
					pgtx := gtx
					pgtx.Constraints = layout.Constraints{Max: image.Pt(mark-gtx.Dp(8), textH)}
					lbl := op.Record(gtx.Ops)
					pd := label(pgtx, strconv.Itoa(percent)+"%", token.TypestyleLabelLargeEmphasized, sc.Surface.OnColor, 1)
					call := lbl.Stop()
					// The share is set right, against the answer.
					offset(gtx, image.Pt(mark-gtx.Dp(8)-pd.Size.X, top+(line-pd.Size.Y)/2), func(gtx layout.Context) layout.Dimensions {
						call.Add(gtx.Ops)
						return pd
					})
				}
				col := pollColor(gtx, poll, a)
				// The mark of an answer chosen is centered on its bar, under
				// the share: the bar keeps clear of the share, and the row
				// takes in the mark, which would stick out of its highlight.
				barTop := textH + gap
				center := barTop + thick/2
				width := float32(gtx.Constraints.Max.X - mark)
				filled := max(int(width*fill*reveal), thick)
				offset(gtx, image.Pt(mark, barTop), func(gtx layout.Context) layout.Dimensions {
					fillRounded(gtx, col, image.Pt(filled, thick), thick/2)
					return layout.Dimensions{}
				})
				if a.Chosen || poll.Quiz && a.Correct {
					offset(gtx, image.Pt(mark-gtx.Dp(8)-d, center-d/2), func(gtx layout.Context) layout.Dimensions {
						return pollChosen(gtx, d, col, poll.Quiz && a.Chosen && !a.Correct)
					})
				}
			}
			return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, textH+bar)}
		})
	}
	return pollRow(gtx, s, a.Text, content)
}

// pollRow draws content, an answer or a task, over a surface that takes
// its clicks and reaches pollBleed past it on each side, as Telegram
// Desktop's highlight spans the bubble.
func pollRow(gtx layout.Context, s *surface, name string, content layout.Widget) layout.Dimensions {
	sc := scheme(gtx)
	macro := op.Record(gtx.Ops)
	dims := content(gtx)
	call := macro.Stop()
	bleed := gtx.Dp(pollBleed)
	size := image.Pt(dims.Size.X+2*bleed, dims.Size.Y)
	style := surfaceStyle{radius: gtx.Dp(8), background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: name}
	offset(gtx, image.Pt(-bleed, 0), func(gtx layout.Context) layout.Dimensions {
		return s.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
			return offset(gtx, image.Pt(bleed, 0), func(gtx layout.Context) layout.Dimensions {
				call.Add(gtx.Ops)
				return dims
			})
		})
	})
	return dims
}

// lineHeight is how high a line of text in style is.
func lineHeight(gtx layout.Context, style token.Typestyle) int {
	macro := op.Record(gtx.Ops)
	dims := label(gtx, "Ag", style, scheme(gtx).Surface.OnColor, 1)
	macro.Stop()
	return dims.Size.Y
}

// pollCircle draws the circle of an answer or a task of diameter d: an
// outline, or, selected, a filled circle with a check mark. The outline is
// fainter unless active, where a click does nothing.
func pollCircle(gtx layout.Context, d int, selected, active bool) layout.Dimensions {
	sc := scheme(gtx)
	size := image.Pt(d, d)
	if selected {
		paint.FillShape(gtx.Ops, sc.Primary.Color.AsNRGBA(), clip.Ellipse{Max: size}.Op(gtx.Ops))
		inner := d * 3 / 4
		offset(gtx, image.Pt((d-inner)/2, (d-inner)/2), func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(inner, inner))
			return iconDone(gtx, sc.Primary.OnColor)
		})
		return layout.Dimensions{Size: size}
	}
	col := sc.SurfaceVariant.OnColor
	if !active {
		col = col.SetOpacity(.5)
	}
	width := float32(gtx.Dp(2))
	ring := clip.Ellipse{Min: image.Pt(int(width/2), int(width/2)), Max: image.Pt(d-int(width/2), d-int(width/2))}
	paint.FillShape(gtx.Ops, col.AsNRGBA(), clip.Stroke{Path: ring.Path(gtx.Ops), Width: width}.Op())
	return layout.Dimensions{Size: size}
}

// pollChosen draws the mark of an answer chosen, in col: a check mark, or
// a cross for a quiz's wrong answer.
func pollChosen(gtx layout.Context, d int, col token.MatColor, wrong bool) layout.Dimensions {
	size := image.Pt(d, d)
	paint.FillShape(gtx.Ops, col.AsNRGBA(), clip.Ellipse{Max: size}.Op(gtx.Ops))
	inner := d * 3 / 4
	offset(gtx, image.Pt((d-inner)/2, (d-inner)/2), func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(image.Pt(inner, inner))
		on := scheme(gtx).Primary.OnColor
		if wrong {
			return iconPollWrong(gtx, on)
		}
		return iconDone(gtx, on)
	})
	return layout.Dimensions{Size: size}
}

// pollFooterLayout draws what is under the answers.
func (p *chatPage) pollFooterLayout(gtx layout.Context, m *model.Message, st *pollState, canVote bool, l localization.Catalog) layout.Dimensions {
	poll := m.Poll
	sc := scheme(gtx)
	// The count and the buttons are as high, so that a vote moves nothing.
	plain := func(text string, style token.Typestyle, col token.MatColor) layout.Dimensions {
		gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, lineHeight(gtx, token.TypestyleLabelLarge)+gtx.Dp(14))
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return label(gtx, text, style, col, 1)
		})
	}
	var text string
	switch pollFooterOf(m, st, canVote) {
	case footerVote:
		text = l.T("poll.vote")
		if len(st.selected) == 0 {
			return plain(text, token.TypestyleLabelLarge, sc.Primary.Color.SetOpacity(.5))
		}
	case footerResults:
		text = l.T("poll.view_results")
		if poll.Public {
			text = l.Count("poll.view_votes", poll.Total, nil)
		}
	case footerAuthorVotes:
		text = l.Count("poll.admin_votes", poll.Total, map[string]string{"arrow": "→"})
	case footerAuthorBack:
		text = l.Format("poll.admin_back", map[string]string{"arrow": "←"})
	default:
		return plain(pollVoters(poll, poll.Total, l), token.TypestyleLabelMedium, sc.SurfaceVariant.OnColor)
	}
	// The button spans the poll, as Telegram Desktop's bottom button
	// does, its highlight reaching into the bubble's padding as the
	// answers' do.
	return pollRow(gtx, &st.footer, text, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return layout.Inset{Top: 7, Bottom: 7}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, text, token.TypestyleLabelLarge, sc.Primary.Color, 1)
			})
		})
	})
}

// todoUpdate takes the clicks on the tasks of m's checklist, before it is
// drawn: a task the account may mark is marked, and otherwise the toast
// tells why it may not.
func (p *chatPage) todoUpdate(gtx layout.Context, m *model.Message, st *pollState, l localization.Catalog) {
	todo := m.Todo
	for len(st.tasks) < len(todo.Items) {
		st.tasks = append(st.tasks, surface{})
	}
	store, canStore := p.source.(model.TodoStore)
	for i, item := range todo.Items {
		if !st.tasks[i].Clicked(gtx) || !canStore || m.Key.MessageID <= 0 || m.Local() {
			continue
		}
		switch {
		case m.ForwardFromID != 0 || !m.ForwardDate.IsZero() || m.ForwardName != "":
			p.toast.Show(l.T("todo.mark_forwarded"))
		case !model.CanCompleteTodo(*m, p.kind):
			p.toast.Show(l.Format("todo.mark_restricted", map[string]string{"user": m.SenderName}))
		case !p.premium():
			p.toast.Show(l.Format("todo.mark_premium", map[string]string{"link": l.T("todo.premium_link")}))
		default:
			store.ToggleTodo(*m, item.ID, !item.Done(), p.reportMedia)
		}
	}
}

// premium tells whether the account has Telegram Premium, as far as the
// store knows.
func (p *chatPage) premium() bool {
	if s, ok := p.source.(model.PremiumSource); ok {
		return s.Premium().Active
	}
	return false
}

// todoLayout draws the checklist of m: its title and kind, its tasks with
// who did each done, and how many are.
func (p *chatPage) todoLayout(gtx layout.Context, m *model.Message, l localization.Catalog) layout.Dimensions {
	todo := m.Todo
	st := p.pollStateOf(m.Key.MessageID)
	p.todoUpdate(gtx, m, st, l)
	sc := scheme(gtx)
	gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(pollWidth))
	gtx.Constraints.Min.X = gtx.Constraints.Max.X
	subtitle := l.T("todo.title")
	if todo.OthersCanComplete {
		subtitle = l.T("todo.title_group")
		if p.kind == model.KindUser || p.kind == model.KindBot {
			subtitle = l.T("todo.title_user")
		}
	}
	canComplete := model.CanCompleteTodo(*m, p.kind)
	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, todo.Title, token.TypestyleTitleMedium, sc.Surface.OnColor, 0)
		}),
		vspace(2),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, subtitle, token.TypestyleLabelMedium, sc.SurfaceVariant.OnColor, 1)
		}),
		vspace(6),
	}
	for i := range todo.Items {
		rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.todoTask(gtx, &st.tasks[i], todo.Items[i], canComplete, todo.OthersCanComplete)
		}))
	}
	rows = append(rows, vspace(4), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		total := strconv.Itoa(len(todo.Items))
		text := l.Format("todo.completed_none", map[string]string{"total": total})
		if done := todo.DoneCount(); done > 0 {
			text = l.Count("todo.completed", done, map[string]string{"total": total})
		}
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Inset{Top: 7, Bottom: 7}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, text, token.TypestyleLabelMedium, sc.SurfaceVariant.OnColor, 1)
			})
		})
	}))
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// todoTask draws a task: a circle the account marks it with, where it may,
// or a mark of whether it is done, its text, and who did it; showWho adds
// their avatar to the circle.
func (p *chatPage) todoTask(gtx layout.Context, s *surface, item model.TodoItem, canComplete, showWho bool) layout.Dimensions {
	sc := scheme(gtx)
	content := func(gtx layout.Context) layout.Dimensions {
		return layout.Inset{Top: 6, Bottom: 6}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			mark := gtx.Dp(pollRadioMark)
			if canComplete && showWho {
				// Room for who did a task, beside its circle.
				mark = gtx.Dp(pollMark)
			}
			line := lineHeight(gtx, token.TypestyleBodyLarge)
			gtx.Constraints.Min.X = gtx.Constraints.Max.X
			textGtx := gtx
			textGtx.Constraints = layout.Constraints{Max: image.Pt(max(gtx.Constraints.Max.X-mark, 0), gtx.Constraints.Max.Y)}
			dims := offset(textGtx, image.Pt(mark, 0), func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, item.Text, token.TypestyleBodyLarge, sc.Surface.OnColor, 0)
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						if !item.Done() || item.CompletedByName == "" {
							return layout.Dimensions{}
						}
						return label(gtx, item.CompletedByName, token.TypestyleLabelSmall, sc.SurfaceVariant.OnColor, 1)
					}),
				)
			})
			d := gtx.Dp(pollRadio)
			top := (line - d) / 2
			if canComplete && showWho && item.Done() && item.CompletedBy != 0 {
				// Who did it, peeking out of the circle, as Telegram
				// Desktop shows them in a list of several people's.
				offset(gtx, image.Pt(gtx.Dp(pollRadioInset)+d/2+gtx.Dp(2), top), func(gtx layout.Context) layout.Dimensions {
					return p.peerAvatar(gtx, item.CompletedBy, item.CompletedByName, pollRadio)
				})
			}
			offset(gtx, image.Pt(gtx.Dp(pollRadioInset), top), func(gtx layout.Context) layout.Dimensions {
				switch {
				case canComplete:
					return pollCircle(gtx, d, item.Done(), true)
				case item.Done():
					gtx.Constraints = layout.Exact(image.Pt(d, d))
					return iconDone(gtx, sc.Primary.Color)
				}
				dot := gtx.Dp(6)
				return offset(gtx, image.Pt((d-dot)/2, (d-dot)/2), func(gtx layout.Context) layout.Dimensions {
					paint.FillShape(gtx.Ops, sc.SurfaceVariant.OnColor.SetOpacity(.5).AsNRGBA(), clip.Ellipse{Max: image.Pt(dot, dot)}.Op(gtx.Ops))
					return layout.Dimensions{Size: image.Pt(dot, dot)}
				})
			})
			return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, max(dims.Size.Y, d))}
		})
	}
	return pollRow(gtx, s, item.Text, content)
}

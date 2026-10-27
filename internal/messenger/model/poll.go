// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"bytes"
	"cmp"
	"context"
	"slices"
	"time"
)

// Poll is the poll or the quiz of a message, and its results as the account
// sees them.
type Poll struct {
	// ID is Telegram's, by which its updates name the poll.
	ID       int64 `json:",omitempty"`
	Question string
	Answers  []PollAnswer
	// Total is how many voted.
	Total                  int
	Closed, Quiz, Multiple bool
	// Public is set for a poll whose voters are seen, beside their
	// answers.
	Public bool `json:",omitempty"`
	// HideResults keeps the results from all but the creator until the
	// poll is closed; Creator is set when the account made the poll.
	HideResults bool `json:",omitempty"`
	Creator     bool `json:",omitempty"`
	// NoRevote is set when a vote cannot be taken back.
	NoRevote bool `json:",omitempty"`
	// CloseDate is when the poll closes by itself, if it does.
	CloseDate time.Time `json:",omitzero"`
	// Solution explains a quiz's answer, once the account answered.
	Solution string `json:",omitempty"`
	// Recent are some of who voted in a public poll.
	Recent []PollVoter `json:",omitempty"`
}

// PollAnswer is one of the answers of a poll.
type PollAnswer struct {
	Text string
	// Option is the answer's identifier, which a vote names.
	Option          []byte `json:",omitempty"`
	Voters          int
	Chosen, Correct bool
}

// PollVoter is someone who voted.
type PollVoter struct {
	ID   int64
	Name string
}

// Voted tells whether the account voted.
func (p *Poll) Voted() bool {
	for _, a := range p.Answers {
		if a.Chosen {
			return true
		}
	}
	return false
}

// ShowResults tells whether the results are shown, as Telegram Desktop's
// Poll::showVotes: once the account voted or the poll is closed, and, when
// the poll hides them until it closes, only then or to its creator.
func (p *Poll) ShowResults() bool {
	if p.HideResults {
		return p.Closed || p.Creator
	}
	return p.Voted() || p.Closed
}

// CanVote tells whether the account may vote in the poll.
func (p *Poll) CanVote() bool {
	return !p.ShowResults() && !p.Voted() && !p.Closed
}

// CanRetract tells whether the account may take its vote back, as Telegram
// Desktop's menu offers it: not in a quiz, nor in a closed poll, nor where
// revoting is disabled.
func (p *Poll) CanRetract() bool {
	return p.Voted() && !p.Closed && !p.Quiz && !p.NoRevote
}

// Answer is the answer whose option is option, or nil.
func (p *Poll) Answer(option []byte) *PollAnswer {
	for i := range p.Answers {
		if bytes.Equal(p.Answers[i].Option, option) {
			return &p.Answers[i]
		}
	}
	return nil
}

// Percents are the shares of the answers' voters in whole percents, in the
// order of the answers, which add up to 100 where rounding lets them, as
// Telegram Desktop counts them (CountNicePercent): each share is rounded
// down, and what is left goes to those that lost the most to rounding, one
// percent each, a group of equal ones all or none. It is nil when nobody
// voted or an answer counts more voters than the poll.
func (p *Poll) Percents() []int {
	if p.Total <= 0 {
		return nil
	}
	type share struct{ index, percent, remainder int }
	shares := make([]share, len(p.Answers))
	left := 100
	for i, a := range p.Answers {
		if a.Voters > p.Total {
			return nil
		}
		percent := a.Voters * 100 / p.Total
		shares[i] = share{i, percent, a.Voters*100 - percent*p.Total}
		left -= percent
	}
	if left > 0 && left <= len(shares) {
		slices.SortStableFunc(shares, func(a, b share) int {
			if c := cmp.Compare(b.remainder, a.remainder); c != 0 {
				return c
			}
			return cmp.Compare(a.percent, b.percent)
		})
		for i := 0; i < len(shares); {
			j := i + 1
			for j < len(shares) && shares[j].percent == shares[i].percent && shares[j].remainder == shares[i].remainder {
				j++
			}
			// A share that lost nothing keeps its percent, and so do
			// those after it, which lost no more.
			if shares[i].remainder == 0 {
				break
			}
			if j-i <= left {
				left -= j - i
				for ; i < j; i++ {
					shares[i].percent++
				}
			} else {
				i = j
			}
		}
	}
	out := make([]int, len(shares))
	for _, s := range shares {
		out[s.index] = s.percent
	}
	return out
}

// Merge returns next, the poll as an update tells it, with what an update
// without the account's choices (a min one) does not know taken from p.
func (p *Poll) Merge(next Poll, min bool) Poll {
	if !min {
		return next
	}
	next.Answers = slices.Clone(next.Answers)
	for i := range next.Answers {
		if old := p.Answer(next.Answers[i].Option); old != nil {
			next.Answers[i].Chosen = old.Chosen
			next.Answers[i].Correct = next.Answers[i].Correct || old.Correct
		}
	}
	if next.Solution == "" {
		next.Solution = p.Solution
	}
	return next
}

// PollVote is someone's vote in a public poll.
type PollVote struct {
	PollVoter
	Date time.Time
}

// PollVotesPage is a page of the votes for an answer: Count in all, and Next
// to ask for the next page with, empty after the last.
type PollVotesPage struct {
	Count int
	List  []PollVote
	Next  string
}

// PollStore lets the account vote in polls.
type PollStore interface {
	// Vote chooses options in the poll of msg, or, with none, takes the
	// account's vote back. The poll changes when Telegram tells it has
	// the vote; report gets the error when it refuses.
	Vote(msg Message, options [][]byte, report func(error))
	// PollVotes lists who voted for option in the public poll of msg,
	// from offset.
	PollVotes(ctx context.Context, msg Message, option []byte, offset string, limit int) (PollVotesPage, error)
	// ClosePoll closes the poll of msg, which the account made, for good.
	ClosePoll(msg Message, report func(error))
}

// Todo is a message's checklist.
type Todo struct {
	Title string
	Items []TodoItem
	// OthersCanAppend and OthersCanComplete are set when others than its
	// author may add tasks to it, and mark them done.
	OthersCanAppend   bool `json:",omitempty"`
	OthersCanComplete bool `json:",omitempty"`
}

// TodoItem is a task of a checklist; one done has who did it, and when.
type TodoItem struct {
	ID              int
	Text            string
	CompletedBy     int64     `json:",omitempty"`
	CompletedByName string    `json:",omitempty"`
	Completed       time.Time `json:",omitzero"`
}

// Done tells whether the task is done.
func (t TodoItem) Done() bool {
	return !t.Completed.IsZero()
}

// DoneCount is how many tasks of the list are done.
func (t *Todo) DoneCount() int {
	n := 0
	for _, item := range t.Items {
		if item.Done() {
			n++
		}
	}
	return n
}

// Toggle returns the list with the task id done by who, at now, or, with
// done false, not done. The list itself is not changed.
func (t Todo) Toggle(id int, done bool, who PollVoter, now time.Time) Todo {
	t.Items = slices.Clone(t.Items)
	for i := range t.Items {
		if t.Items[i].ID != id {
			continue
		}
		if done {
			t.Items[i].CompletedBy, t.Items[i].CompletedByName, t.Items[i].Completed = who.ID, who.Name, now
		} else {
			t.Items[i].CompletedBy, t.Items[i].CompletedByName, t.Items[i].Completed = 0, "", time.Time{}
		}
	}
	return t
}

// CanCompleteTodo tells whether the account may mark the tasks of m's
// checklist, in a chat of kind, as Telegram Desktop's
// TodoList::canComplete: its own list, one in Saved Messages, or one whose
// author lets others, and not a forwarded one. Telegram asks for Premium
// besides.
func CanCompleteTodo(m Message, kind ChatKind) bool {
	if m.Todo == nil || m.Key.MessageID <= 0 || m.Local() {
		return false
	}
	if m.ForwardFromID != 0 || !m.ForwardDate.IsZero() || m.ForwardName != "" {
		return false
	}
	return m.Outgoing || kind == KindSaved || m.Todo.OthersCanComplete
}

// TodoStore lets the account mark the tasks of checklists done.
type TodoStore interface {
	// ToggleTodo marks the task id of the checklist of msg done or not.
	// The message changes at once; report gets the error when Telegram
	// refuses, and the change is undone.
	ToggleTodo(msg Message, id int, done bool, report func(error))
}

// CanClosePoll tells whether the account may close the poll of m, in a
// chat of kind, as Telegram Desktop's HistoryItem::canStopPoll: an open
// poll of its own message, not forwarded.
func CanClosePoll(m Message, kind ChatKind) bool {
	if m.Poll == nil || m.Poll.Closed || m.Key.MessageID <= 0 || m.Local() {
		return false
	}
	if m.ForwardFromID != 0 || !m.ForwardDate.IsZero() || m.ForwardName != "" {
		return false
	}
	return m.Outgoing || kind == KindSaved
}

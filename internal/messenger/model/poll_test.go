// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"slices"
	"testing"
	"time"
)

func pollOf(total int, voters ...int) *Poll {
	p := &Poll{Total: total}
	for i, v := range voters {
		p.Answers = append(p.Answers, PollAnswer{Option: []byte{byte('a' + i)}, Voters: v})
	}
	return p
}

// The shares add up to 100 where rounding lets them, as Telegram Desktop
// counts them.
func TestPollPercents(t *testing.T) {
	for _, c := range []struct {
		total  int
		voters []int
		want   []int
	}{
		{3, []int{2, 1}, []int{67, 33}},
		{3, []int{1, 1, 1}, []int{33, 33, 33}},
		{3, []int{1, 1, 1, 0}, []int{33, 33, 33, 0}},
		// Two equal shares, one percent left: neither gets it.
		{7, []int{3, 2, 2}, []int{43, 28, 28}},
		{6, []int{1, 1, 4}, []int{17, 17, 66}},
		{8, []int{1, 7}, []int{13, 87}},
		{4, []int{3, 2}, []int{75, 50}},
		{0, []int{0, 0}, nil},
		{2, []int{3, 0}, nil},
	} {
		if got := pollOf(c.total, c.voters...).Percents(); !slices.Equal(got, c.want) {
			t.Errorf("%d of %v: %v, want %v", c.total, c.voters, got, c.want)
		}
	}
}

func TestPollStates(t *testing.T) {
	p := pollOf(3, 2, 1)
	if p.ShowResults() || !p.CanVote() || p.CanRetract() {
		t.Error("a poll not voted in")
	}
	p.Answers[1].Chosen = true
	if !p.ShowResults() || p.CanVote() || !p.CanRetract() {
		t.Error("a poll voted in")
	}
	p.NoRevote = true
	if p.CanRetract() {
		t.Error("revoting disabled")
	}
	p.NoRevote, p.Quiz = false, true
	if p.CanRetract() {
		t.Error("a quiz answered")
	}
	hidden := pollOf(3, 2, 1)
	hidden.HideResults = true
	hidden.Answers[0].Chosen = true
	if hidden.ShowResults() {
		t.Error("results hidden until the poll closes")
	}
	hidden.Creator = true
	if !hidden.ShowResults() {
		t.Error("the creator sees the results")
	}
	closed := pollOf(3, 2, 1)
	closed.Closed = true
	if !closed.ShowResults() || closed.CanVote() {
		t.Error("a closed poll")
	}
}

func TestPollMerge(t *testing.T) {
	old := pollOf(3, 2, 1)
	old.Answers[1].Chosen = true
	old.Solution = "because"
	next := *pollOf(9, 5, 4)
	if got := old.Merge(next, true); !got.Answers[1].Chosen || got.Answers[1].Voters != 4 || got.Solution != "because" || got.Total != 9 {
		t.Errorf("min %+v", got)
	}
	if got := old.Merge(next, false); got.Answers[1].Chosen {
		t.Errorf("not min %+v", got)
	}
}

func TestTodoToggle(t *testing.T) {
	list := Todo{Items: []TodoItem{{ID: 1, Text: "a"}, {ID: 2, Text: "b"}}}
	now := time.Unix(100, 0)
	done := list.Toggle(2, true, PollVoter{ID: 7, Name: "Ann"}, now)
	if list.Items[1].Done() {
		t.Error("the list itself changed")
	}
	if it := done.Items[1]; !it.Done() || it.CompletedBy != 7 || it.CompletedByName != "Ann" || done.DoneCount() != 1 {
		t.Errorf("done %+v", done.Items)
	}
	if undone := done.Toggle(2, false, PollVoter{}, now); undone.DoneCount() != 0 || undone.Items[1].CompletedBy != 0 {
		t.Errorf("undone %+v", undone.Items)
	}
}

func TestCanCompleteTodo(t *testing.T) {
	m := Message{Key: MessageKey{MessageID: 5}, Todo: &Todo{}}
	if CanCompleteTodo(m, KindGroup) {
		t.Error("someone else's list")
	}
	if !CanCompleteTodo(m, KindSaved) {
		t.Error("a list in Saved Messages")
	}
	m.Todo.OthersCanComplete = true
	if !CanCompleteTodo(m, KindGroup) {
		t.Error("a list others may mark")
	}
	m.ForwardName = "Bob"
	if CanCompleteTodo(m, KindGroup) {
		t.Error("a forwarded list")
	}
	own := Message{Key: MessageKey{MessageID: 5}, Outgoing: true, Todo: &Todo{}}
	if !CanCompleteTodo(own, KindGroup) {
		t.Error("the account's own list")
	}
	if Editable(own, KindGroup, time.Now(), 0) {
		t.Error("a checklist is not edited as text")
	}
}

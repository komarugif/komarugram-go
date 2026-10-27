// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"os"
	"slices"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// pollStore is the menu store that takes votes and marks tasks, as a
// Telegram store does.
type pollStore struct {
	*menuStore
	premium bool
	votes   [][][]byte
	toggled []int
	closed  int
	// list answers PollVotes, when set.
	list func(option []byte) (model.PollVotesPage, error)
}

func (s *pollStore) Vote(m model.Message, options [][]byte, report func(error)) {
	s.votes = append(s.votes, options)
}
func (s *pollStore) PollVotes(_ context.Context, m model.Message, option []byte, offset string, limit int) (model.PollVotesPage, error) {
	if s.list != nil {
		return s.list(option)
	}
	return model.PollVotesPage{}, nil
}
func (s *pollStore) ClosePoll(m model.Message, report func(error)) { s.closed++ }
func (s *pollStore) ToggleTodo(m model.Message, id int, done bool, report func(error)) {
	s.toggled = append(s.toggled, id)
}
func (s *pollStore) Premium() model.Premium { return model.Premium{Active: s.premium} }

func answers(texts ...string) []model.PollAnswer {
	var out []model.PollAnswer
	for i, t := range texts {
		out = append(out, model.PollAnswer{Text: t, Option: []byte{byte('a' + i)}})
	}
	return out
}

// pressDown presses the left button across the row of message id, down
// from its top, or up from its bottom, until done tells that a press did
// what it waits for.
func (h *menuHarness) pressDown(id model.MessageID, done func() bool) {
	h.t.Helper()
	h.pressAcross(id, false, done)
}

func (h *menuHarness) pressUp(id model.MessageID, done func() bool) {
	h.t.Helper()
	h.pressAcross(id, true, done)
}

func (h *menuHarness) pressAcross(id model.MessageID, up bool, done func() bool) {
	h.t.Helper()
	center := h.messageAt(id)
	p := h.page
	var height float32
	for i, m := range p.messages {
		if m.Key.MessageID == id {
			height = float32(p.heights.Prefix(i+1) - p.heights.Prefix(i))
		}
	}
	for i := float32(0); i < height; i += 6 {
		y := center.Y - height/2 + i
		if up {
			y = center.Y + height/2 - i
		}
		for x := float32(100); x < 500; x += 80 {
			h.press(pointer.ButtonPrimary, f32.Pt(x, y))
			if done() {
				return
			}
		}
	}
	h.t.Fatalf("no press on message %d did it", id)
}

// A click on an answer votes for it, and on a task of a list the account
// may mark, marks it.
func TestPollVoteAndTodoClick(t *testing.T) {
	var store *pollStore
	h := newMenuHarnessOn(t, func(_ *menuStore, messages []model.Message) {
		messages[0].Text, messages[0].Kind = "", model.MessagePoll
		messages[0].Poll = &model.Poll{Question: "Lunch?", Total: 3, Answers: answers("Soup", "Salad")}
		messages[1].Text, messages[1].Outgoing = "", true
		messages[1].Todo = &model.Todo{Title: "Trip", Items: []model.TodoItem{{ID: 7, Text: "Tickets"}, {ID: 8, Text: "Hotel"}}}
	}, func(s *menuStore) model.ConversationStore {
		store = &pollStore{menuStore: s, premium: true}
		return store
	})
	h.frames(2)
	h.pressDown(1, func() bool { return len(store.votes) > 0 })
	if len(store.votes) != 1 || len(store.votes[0]) != 1 || string(store.votes[0][0]) != "a" {
		t.Errorf("votes %q", store.votes)
	}
	h.pressDown(2, func() bool { return len(store.toggled) > 0 })
	if !slices.Equal(store.toggled, []int{7}) {
		t.Errorf("toggled %v", store.toggled)
	}
}

// A task of someone else's list, which they keep for themselves, is not
// marked: the toast tells why.
func TestTodoRestricted(t *testing.T) {
	var store *pollStore
	h := newMenuHarnessOn(t, func(_ *menuStore, messages []model.Message) {
		messages[1].Text = ""
		messages[1].Todo = &model.Todo{Title: "Trip", Items: []model.TodoItem{{ID: 7, Text: "Tickets"}}}
	}, func(s *menuStore) model.ConversationStore {
		store = &pollStore{menuStore: s, premium: true}
		return store
	})
	h.frames(2)
	h.pressDown(2, func() bool { return h.page.toast.text != "" })
	if len(store.toggled) != 0 || h.page.toast.text != "Ann has restricted others from marking tasks as done." {
		t.Errorf("toggled %v, toast %q", store.toggled, h.page.toast.text)
	}
}

// A poll of several answers sends the answers chosen with its button.
func TestPollMultipleChoice(t *testing.T) {
	var store *pollStore
	h := newMenuHarnessOn(t, func(_ *menuStore, messages []model.Message) {
		messages[0].Text, messages[0].Kind = "", model.MessagePoll
		messages[0].Poll = &model.Poll{Question: "Lunch?", Multiple: true, Answers: answers("Soup", "Salad")}
	}, func(s *menuStore) model.ConversationStore {
		store = &pollStore{menuStore: s}
		return store
	})
	h.frames(2)
	st := h.page.pollStateOf(1)
	h.pressDown(1, func() bool { return len(st.selected) > 0 })
	if len(store.votes) != 0 || len(st.selected) != 1 || string(st.selected[0]) != "a" {
		t.Fatalf("votes %q, selected %q", store.votes, st.selected)
	}
	// The button is the last of the poll.
	h.pressUp(1, func() bool { return len(store.votes) > 0 })
	if len(store.votes) != 1 || len(store.votes[0]) != 1 || string(store.votes[0][0]) != "a" {
		t.Errorf("votes %q", store.votes)
	}
}

// The menu of a poll voted in takes the vote back, and the menu of the
// account's own poll closes it.
func TestPollMenu(t *testing.T) {
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore { return &pollStore{menuStore: s} })
	p := h.page
	voted := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: 5}, Poll: &model.Poll{Answers: answers("a", "b")}}
	voted.Poll.Answers[0].Chosen = true
	if got := p.menuActions(voted); !slices.Contains(got, actionRetractVote) || slices.Contains(got, actionStopPoll) {
		t.Errorf("voted: %v", got)
	}
	own := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: 6}, Outgoing: true, Poll: &model.Poll{Answers: answers("a", "b")}}
	if got := p.menuActions(own); slices.Contains(got, actionRetractVote) || !slices.Contains(got, actionStopPoll) {
		t.Errorf("own: %v", got)
	}
	own.Poll.Closed = true
	if got := p.menuActions(own); slices.Contains(got, actionStopPoll) {
		t.Errorf("closed: %v", got)
	}
}

// TestRenderPolls saves polls and checklists in their states, to look at:
// POLL_PNG=/tmp/polls.png.
func TestRenderPolls(t *testing.T) {
	path := os.Getenv("POLL_PNG")
	if path == "" {
		t.Skip("set POLL_PNG to a file")
	}
	h := newMenuHarnessOn(t, func(_ *menuStore, messages []model.Message) {
		set := func(i int, outgoing bool, poll *model.Poll, todo *model.Todo) {
			m := &messages[i]
			m.Text, m.Outgoing, m.Poll, m.Todo = "", outgoing, poll, todo
			if poll != nil {
				m.Kind = model.MessagePoll
			}
		}
		open := &model.Poll{Question: "Какое оформление выберем?", Total: 13, Answers: answers("Светлое", "Тёмное", "Как в системе")}
		voted := &model.Poll{Question: "Что добавить в следующую версию?", Total: 10, Public: true, Multiple: true, Answers: answers("Папки чатов", "Темы оформления", "Опросы с картинками", "Звонки"),
			Recent: []model.PollVoter{{ID: 2, Name: "Анна"}, {ID: 9, Name: "Дмитрий"}, {ID: 5, Name: "Ольга"}}}
		for i, v := range []int{5, 3, 0, 4} {
			voted.Answers[i].Voters = v
		}
		voted.Answers[0].Chosen, voted.Answers[3].Chosen = true, true
		quiz := &model.Poll{Question: "Какой протокол лежит в основе Telegram?", Total: 22, Quiz: true, Answers: answers("MTProto", "XMPP", "HTTP/3"),
			Solution: "MTProto — собственный протокол Telegram."}
		for i, v := range []int{15, 5, 2} {
			quiz.Answers[i].Voters = v
		}
		quiz.Answers[0].Correct, quiz.Answers[1].Chosen = true, true
		closed := &model.Poll{Question: "Где проведём встречу?", Total: 8, Closed: true, Answers: answers("В кафе", "В парке", "Онлайн")}
		for i, v := range []int{5, 1, 2} {
			closed.Answers[i].Voters = v
		}
		multiple := &model.Poll{Question: "Когда удобно созвониться?", Multiple: true, Public: true, CloseDate: time.Unix(1000, 0).Add(26 * time.Hour), Answers: answers("Утром", "Днём", "Вечером")}
		done := time.Unix(900, 0)
		set(13, false, open, nil)
		set(14, true, voted, nil)
		set(15, false, quiz, nil)
		set(16, false, closed, nil)
		set(17, false, multiple, nil)
		set(18, true, nil, &model.Todo{Title: "Подготовка к релизу", OthersCanComplete: true, Items: []model.TodoItem{
			{ID: 1, Text: "Собрать сборки для Windows, Linux и macOS", CompletedBy: 2, CompletedByName: "Анна Смирнова", Completed: done},
			{ID: 2, Text: "Проверить опросы и списки задач"},
			{ID: 3, Text: "Обновить переводы", CompletedBy: 5, CompletedByName: "Ольга", Completed: done},
		}})
		set(19, false, nil, &model.Todo{Title: "Покупки на выходные", Items: []model.TodoItem{
			{ID: 1, Text: "Хлеб", CompletedBy: 42, CompletedByName: "Ann", Completed: done}, {ID: 2, Text: "Молоко"},
		}})
	}, func(s *menuStore) model.ConversationStore {
		people := []model.PollVoter{{ID: 2, Name: "Анна Смирнова"}, {ID: 9, Name: "Дмитрий Козлов"}, {ID: 5, Name: "Ольга"}, {ID: 7, Name: "Игорь"}, {ID: 8, Name: "Мария"}}
		return &pollStore{menuStore: s, premium: true, list: func(option []byte) (model.PollVotesPage, error) {
			n := int(option[0]-'a') + 2
			page := model.PollVotesPage{Count: n}
			for i := range n {
				page.List = append(page.List, model.PollVote{PollVoter: people[(i+n)%len(people)]})
			}
			return page, nil
		}}
	})
	p := h.page
	// The quiz answered wrong shows its explanation; the poll of several
	// answers has one chosen.
	p.pollStateOf(16).showSolution = true
	p.pollStateOf(18).selected = [][]byte{[]byte("b")}
	l := localization.For("ru")
	for _, part := range []struct {
		file  string
		first int
	}{{path, 13}, {path + ".2.png", 17}} {
		p.list.Position.First, p.list.Position.Offset, p.list.Position.BeforeEnd = part.first, 0, true
		renderFrames(t, image.Pt(520, 1500), part.file, func(gtx layout.Context) {
			gtx.Now = time.Unix(1000, 0)
			p.images.BeginFrame()
			p.media.BeginFrame()
			p.Layout(gtx, model.Chat{ID: 1, Title: "Чат", Kind: model.KindGroup}, l, false)
			p.layoutDialogs(gtx, l)
			p.media.EndFrame()
			p.images.EndFrame()
		})
	}
	// The first poll a moment after a vote, its results coming in
	// (.reveal.png): the frames are 50 ms apart, the vote three before the
	// last.
	p.list.Position.First, p.list.Position.Offset, p.list.Position.BeforeEnd = 13, 0, true
	frame := 0
	renderFrames(t, image.Pt(520, 600), path+".reveal.png", func(gtx layout.Context) {
		if frame++; frame == 27 {
			m := &h.store.benchmarkHistory.h.Messages[13]
			voted := *m.Poll
			voted.Answers = slices.Clone(voted.Answers)
			voted.Total = 14
			for i, v := range []int{6, 5, 3} {
				voted.Answers[i].Voters = v
			}
			voted.Answers[1].Chosen = true
			m.Poll, m.ContentRevision = &voted, 2
			h.store.benchmarkHistory.h.Revision++
		}
		p.images.BeginFrame()
		p.media.BeginFrame()
		p.Layout(gtx, model.Chat{ID: 1, Title: "Чат", Kind: model.KindGroup}, l, false)
		p.media.EndFrame()
		p.images.EndFrame()
	})
	// The dialog of who voted in the public poll (.votes.png), once their
	// pages came.
	p.pollVotes.open(p, h.store.benchmarkHistory.h.Messages[14])
	for range 200 {
		if p.pollVotes.ready(p) {
			break
		}
		p.pollVotes.update(l)
		time.Sleep(5 * time.Millisecond)
	}
	renderFrames(t, image.Pt(520, 800), path+".votes.png", func(gtx layout.Context) {
		p.images.BeginFrame()
		p.media.BeginFrame()
		p.Layout(gtx, model.Chat{ID: 1, Title: "Чат", Kind: model.KindGroup}, l, false)
		p.layoutDialogs(gtx, l)
		p.media.EndFrame()
		p.images.EndFrame()
	})
}

// A vote leaves the poll as high as it was: the answers keep their lines
// and the room of their bars, and what is under them its height.
func TestPollKeepsHeightAfterVote(t *testing.T) {
	long := "A long answer that takes more than one line in the poll, as some do"
	h := newMenuHarnessOn(t, func(_ *menuStore, messages []model.Message) {
		messages[0].Text, messages[0].Kind = "", model.MessagePoll
		messages[0].Poll = &model.Poll{Question: "Lunch?", Total: 3, Public: true, Quiz: true, Answers: answers("Soup", long, "Salad"),
			Recent: []model.PollVoter{{ID: 2, Name: "Ann"}}}
	}, func(s *menuStore) model.ConversationStore { return &pollStore{menuStore: s} })
	h.frames(2)
	height := func() int64 { return h.page.heights.Prefix(1) - h.page.heights.Prefix(0) }
	before := height()
	m := &h.store.benchmarkHistory.h.Messages[0]
	voted := *m.Poll
	voted.Answers = slices.Clone(voted.Answers)
	voted.Answers[0].Voters, voted.Answers[1].Voters, voted.Answers[2].Voters = 1, 1, 1
	voted.Answers[1].Chosen, voted.Answers[0].Correct = true, true
	voted.Solution = "Soup is warm"
	m.Poll, m.ContentRevision = &voted, 2
	h.store.benchmarkHistory.h.Revision++
	h.frames(3)
	if after := height(); after != before {
		t.Errorf("the poll was %d high, %d after the vote", before, after)
	}
}

// The results come in over frames, as the answers move to their places;
// without animations they are there at once.
func TestPollResultsAnimate(t *testing.T) {
	for _, still := range []bool{false, true} {
		h := newMenuHarnessOn(t, func(_ *menuStore, messages []model.Message) {
			messages[0].Text, messages[0].Kind = "", model.MessagePoll
			messages[0].Poll = &model.Poll{Question: "Lunch?", Total: 3, Answers: answers("Soup", "Salad")}
		}, func(s *menuStore) model.ConversationStore { return &pollStore{menuStore: s} })
		h.still = still
		h.frames(2)
		st := h.page.pollStateOf(1)
		if st.revealed != 0 {
			t.Fatalf("before the vote: %v", st.revealed)
		}
		m := &h.store.benchmarkHistory.h.Messages[0]
		voted := *m.Poll
		voted.Answers = slices.Clone(voted.Answers)
		voted.Answers[0].Chosen, voted.Answers[0].Voters, voted.Answers[1].Voters = true, 2, 1
		m.Poll, m.ContentRevision = &voted, 2
		h.store.benchmarkHistory.h.Revision++
		h.frames(2)
		if mid := st.revealed; still && mid != 1 || !still && (mid <= 0 || mid >= 1) {
			t.Errorf("still %v: %v a frame after the vote", still, mid)
		}
		h.frames(60)
		if st.revealed != 1 {
			t.Errorf("still %v: %v at the end", still, st.revealed)
		}
	}
}

// The dialog of who voted asks for every answer's voters at once, and shows
// them when all came, one ring until then.
func TestPollVotesLoadTogether(t *testing.T) {
	asked := make(chan string, 2)
	release := make(chan struct{})
	var store *pollStore
	h := newMenuHarnessOn(t, nil, func(s *menuStore) model.ConversationStore {
		store = &pollStore{menuStore: s, list: func(option []byte) (model.PollVotesPage, error) {
			asked <- string(option)
			if string(option) == "b" {
				<-release
			}
			return model.PollVotesPage{Count: 1, List: []model.PollVote{{PollVoter: model.PollVoter{ID: 2, Name: "Ann"}}}}, nil
		}}
		return store
	})
	poll := &model.Poll{Question: "Lunch?", Total: 2, Public: true, Answers: answers("Soup", "Salad")}
	poll.Answers[0].Voters, poll.Answers[1].Voters, poll.Answers[0].Chosen = 1, 1, true
	d := &h.page.pollVotes
	d.open(h.page, model.Message{Key: model.MessageKey{ChatID: 1, MessageID: 1}, Poll: poll})
	if d.ready(h.page) {
		t.Fatal("ready before any page came")
	}
	got := map[string]bool{<-asked: true, <-asked: true}
	if !got["a"] || !got["b"] {
		t.Fatalf("asked for %v", got)
	}
	// The first answer's page came; the second's did not.
	deadline := time.Now().Add(5 * time.Second)
	for len(d.answers[0].items) == 0 && time.Now().Before(deadline) {
		d.update(localization.For("en"))
		time.Sleep(time.Millisecond)
	}
	if len(d.answers[0].items) != 1 || d.ready(h.page) {
		t.Fatalf("first page %v, ready %v", d.answers[0].items, d.ready(h.page))
	}
	close(release)
	for !d.ready(h.page) && time.Now().Before(deadline) {
		d.update(localization.For("en"))
		time.Sleep(time.Millisecond)
	}
	if !d.ready(h.page) || len(d.answers[1].items) != 1 {
		t.Errorf("after the last page: ready %v, %v", d.ready(h.page), d.answers[1].items)
	}
	d.stop()
}

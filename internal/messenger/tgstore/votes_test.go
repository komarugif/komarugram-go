// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/model"
)

func testPoll() tg.Poll {
	p := tg.Poll{ID: 77, Question: tg.TextWithEntities{Text: "Lunch?"}, Quiz: true, PublicVoters: true, CloseDate: 5000, Answers: []tg.PollAnswerClass{
		&tg.PollAnswer{Text: tg.TextWithEntities{Text: "Soup"}, Option: []byte("a")},
		&tg.PollAnswer{Text: tg.TextWithEntities{Text: "Salad"}, Option: []byte("b")},
	}}
	p.SetFlags()
	return p
}

// pollStore is a store with group 5 open, holding message 4 with a poll and
// message 6 with a checklist of the account's.
func pollStore(t *testing.T, invoke func(in bin.Encoder, out bin.Decoder) error) *Store {
	t.Helper()
	s := testStore(t)
	s.rememberPeers([]tg.UserClass{&tg.User{ID: 3, FirstName: "Ann"}}, []tg.ChatClass{&tg.Chat{ID: 5, Title: "team"}})
	chat := peerID(&tg.PeerChat{ChatID: 5})
	poll := &tg.Message{ID: 4, PeerID: &tg.PeerChat{ChatID: 5}, Date: 4, Media: &tg.MessageMediaPoll{Poll: testPoll(), Results: tg.PollResults{TotalVoters: 3,
		Results: []tg.PollAnswerVoters{{Option: []byte("a"), Voters: 2}, {Option: []byte("b"), Voters: 1}}}}}
	todo := &tg.Message{ID: 6, Out: true, PeerID: &tg.PeerChat{ChatID: 5}, Date: 6, Media: &tg.MessageMediaToDo{
		Todo: tg.TodoList{Title: tg.TextWithEntities{Text: "Trip"}, OthersCanComplete: true, List: []tg.TodoItem{
			{ID: 1, Title: tg.TextWithEntities{Text: "Tickets"}}, {ID: 2, Title: tg.TextWithEntities{Text: "Hotel"}},
		}},
		Completions: []tg.TodoCompletion{{ID: 2, CompletedBy: &tg.PeerUser{UserID: 3}, Date: 100}},
	}}
	for _, m := range []*tg.Message{poll, todo} {
		m.SetFlags()
	}
	msgs, err := s.ingest(context.Background(), []tg.MessageClass{poll, todo}, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	s.history.histories[chat] = &model.History{Messages: msgs}
	s.history.api = tg.NewClient(telegram.InvokeFunc(func(ctx context.Context, in bin.Encoder, out bin.Decoder) error { return invoke(in, out) }))
	return s
}

func historyMessage(t *testing.T, s *Store, id model.MessageID) model.Message {
	t.Helper()
	for _, m := range s.History(peerID(&tg.PeerChat{ChatID: 5})).Messages {
		if m.Key.MessageID == id {
			return m
		}
	}
	t.Fatalf("no message %d", id)
	return model.Message{}
}

// waitFor waits until ok holds of message id, for what a store's goroutine
// does.
func waitFor(t *testing.T, s *Store, id model.MessageID, ok func(model.Message) bool) model.Message {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if m := historyMessage(t, s, id); ok(m) || time.Now().After(deadline) {
			return m
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestConvertPollAndTodo(t *testing.T) {
	s := pollStore(t, func(bin.Encoder, bin.Decoder) error { return errors.New("offline") })
	poll := historyMessage(t, s, 4).Poll
	if poll == nil || poll.ID != 77 || poll.Question != "Lunch?" || !poll.Quiz || !poll.Public || poll.Total != 3 || poll.CloseDate.Unix() != 5000 {
		t.Fatalf("poll %+v", poll)
	}
	if len(poll.Answers) != 2 || string(poll.Answers[0].Option) != "a" || poll.Answers[0].Voters != 2 || poll.Answers[1].Text != "Salad" {
		t.Errorf("answers %+v", poll.Answers)
	}
	m := historyMessage(t, s, 6)
	todo := m.Todo
	if todo == nil || todo.Title != "Trip" || !todo.OthersCanComplete || len(todo.Items) != 2 || m.Text != "" {
		t.Fatalf("todo %+v, text %q", todo, m.Text)
	}
	if todo.Items[0].Done() || !todo.Items[1].Done() || todo.Items[1].CompletedBy != 3 || todo.Items[1].CompletedByName != "Ann" {
		t.Errorf("items %+v", todo.Items)
	}
}

// An update of a poll's results changes it; a min one, which does not tell
// the account's choice, keeps it.
func TestPollUpdate(t *testing.T) {
	s := pollStore(t, func(bin.Encoder, bin.Decoder) error { return errors.New("offline") })
	s.changeMessage(peerID(&tg.PeerChat{ChatID: 5}), 4, func(m *model.Message) { m.Poll.Answers[1].Chosen = true })
	results := tg.PollResults{Min: true, TotalVoters: 9, Results: []tg.PollAnswerVoters{{Option: []byte("a"), Voters: 5}, {Option: []byte("b"), Voters: 4}}}
	results.SetFlags()
	err := s.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessagePoll{PollID: 77, Results: results}}})
	if err != nil {
		t.Fatal(err)
	}
	poll := historyMessage(t, s, 4).Poll
	if poll.Total != 9 || poll.Answers[0].Voters != 5 || poll.Answers[1].Voters != 4 || !poll.Answers[1].Chosen || poll.Question != "Lunch?" {
		t.Errorf("after a min update %+v", poll)
	}
	// One that names the message, with the poll closed.
	closed := testPoll()
	closed.Closed = true
	closed.SetFlags()
	results = tg.PollResults{TotalVoters: 10, Results: []tg.PollAnswerVoters{{Option: []byte("a"), Voters: 6, Correct: true}, {Option: []byte("b"), Voters: 4, Chosen: true}}, Solution: "Soup is warm"}
	results.SetFlags()
	u := &tg.UpdateMessagePoll{Peer: &tg.PeerChat{ChatID: 5}, MsgID: 4, PollID: 77, Poll: closed, Results: results}
	u.SetFlags()
	if err := s.Handle(context.Background(), &tg.Updates{Updates: []tg.UpdateClass{u}}); err != nil {
		t.Fatal(err)
	}
	poll = historyMessage(t, s, 4).Poll
	if !poll.Closed || poll.Total != 10 || !poll.Answers[0].Correct || !poll.Answers[1].Chosen || poll.Solution != "Soup is warm" {
		t.Errorf("after an update of the poll %+v", poll)
	}
}

func TestVoteSends(t *testing.T) {
	sent := make(chan *tg.MessagesSendVoteRequest, 1)
	s := pollStore(t, func(in bin.Encoder, out bin.Decoder) error {
		req, ok := in.(*tg.MessagesSendVoteRequest)
		if !ok {
			t.Errorf("unexpected request %T", in)
			return nil
		}
		sent <- req
		results := tg.PollResults{TotalVoters: 4, Results: []tg.PollAnswerVoters{{Option: []byte("a"), Voters: 2}, {Option: []byte("b"), Voters: 2, Chosen: true}}}
		results.SetFlags()
		out.(*tg.UpdatesBox).Updates = &tg.Updates{Updates: []tg.UpdateClass{&tg.UpdateMessagePoll{PollID: 77, Results: results}}}
		return nil
	})
	msg := historyMessage(t, s, 4)
	s.Vote(msg, [][]byte{[]byte("b")}, func(err error) { t.Errorf("reported %v", err) })
	req := <-sent
	if req.MsgID != 4 || len(req.Options) != 1 || string(req.Options[0]) != "b" {
		t.Errorf("sent %+v", req)
	}
	if poll := waitFor(t, s, 4, func(m model.Message) bool { return m.Poll.Voted() }).Poll; !poll.Voted() || poll.Total != 4 {
		t.Errorf("poll %+v", poll)
	}
}

// A task is marked at once, and unmarked again when Telegram refuses.
func TestToggleTodoUndoneOnError(t *testing.T) {
	release := make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	sent := make(chan *tg.MessagesToggleTodoCompletedRequest, 1)
	s := pollStore(t, func(in bin.Encoder, out bin.Decoder) error {
		if req, ok := in.(*tg.MessagesToggleTodoCompletedRequest); ok {
			sent <- req
		}
		<-release
		return tgerr.New(400, "TODO_NOT_MODIFIED")
	})
	s.mu.Lock()
	s.me = model.Profile{ID: 10, FirstName: "Me"}
	s.mu.Unlock()
	reported := make(chan error, 1)
	s.ToggleTodo(historyMessage(t, s, 6), 1, true, func(err error) { reported <- err })
	req := <-sent
	if req.MsgID != 6 || len(req.Completed) != 1 || req.Completed[0] != 1 || len(req.Incompleted) != 0 {
		t.Errorf("sent %+v", req)
	}
	if item := historyMessage(t, s, 6).Todo.Items[0]; !item.Done() || item.CompletedBy != 10 || item.CompletedByName != "Me" {
		t.Errorf("before the answer %+v", item)
	}
	once.Do(func() { close(release) })
	if err := <-reported; !tgerr.Is(err, "TODO_NOT_MODIFIED") {
		t.Errorf("reported %v", err)
	}
	if item := waitFor(t, s, 6, func(m model.Message) bool { return !m.Todo.Items[0].Done() }).Todo.Items[0]; item.Done() {
		t.Errorf("after the refusal %+v", item)
	}
	if item := historyMessage(t, s, 6).Todo.Items[1]; item.CompletedBy != 3 {
		t.Errorf("the other task changed: %+v", item)
	}
}

// A poll or a checklist shows its question or title in the list, as
// Telegram Desktop's MediaPoll::toPreview and MediaTodoList::toPreview do.
func TestPollAndTodoPreview(t *testing.T) {
	s := pollStore(t, func(bin.Encoder, bin.Decoder) error { return errors.New("offline") })
	if p := messagePreview(historyMessage(t, s, 4), true); p.Media != "preview.poll" || p.Title != "Lunch?" {
		t.Errorf("poll %+v", p)
	}
	if p := messagePreview(historyMessage(t, s, 6), true); p.Media != "preview.todo" || p.Title != "Trip" {
		t.Errorf("checklist %+v", p)
	}
	raw := &tg.Message{ID: 9, PeerID: &tg.PeerChat{ChatID: 5}, Media: &tg.MessageMediaToDo{Todo: tg.TodoList{Title: tg.TextWithEntities{Text: "Trip  plan"}}}}
	if p, _ := preview(raw); p.Media != "preview.todo" || p.Title != "Trip plan" {
		t.Errorf("dialog's checklist %+v", p)
	}
}

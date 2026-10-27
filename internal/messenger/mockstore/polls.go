// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"bytes"
	"context"
	"strconv"
	"time"

	"komarugram/internal/messenger/model"
)

// The demo's polls and checklists: a poll to vote in, a public one of
// several answers, a quiz, a closed poll, and two checklists, one the
// account may mark and one it may not.

// demoQuiz is the quiz's right answer and its explanation, which Telegram
// tells only once the account answered.
var demoQuiz = struct {
	id       int64
	correct  []byte
	solution string
}{id: 3, correct: []byte("0"), solution: "MTProto — собственный протокол Telegram: шифрование, доставка и синхронизация между устройствами."}

// demoVoters are who voted in the public poll, by their answer.
var demoVoters = []model.PollVoter{{ID: 2, Name: "Анна Смирнова"}, {ID: 9, Name: "Дмитрий Козлов"}, {ID: 5, Name: "Ольга"}, {ID: 7, Name: "Игорь"}, {ID: 8, Name: "Мария"}}

func demoOptions(texts []string, voters []int) []model.PollAnswer {
	out := make([]model.PollAnswer, len(texts))
	for i, t := range texts {
		out[i] = model.PollAnswer{Text: t, Option: []byte(strconv.Itoa(i)), Voters: voters[i]}
	}
	return out
}

// demoPolls are the messages with polls and checklists, as add makes them:
// sender's name, outgoing, and what they hold.
func demoPolls() []model.Message {
	return []model.Message{
		{Kind: model.MessagePoll, Poll: &model.Poll{ID: 1, Question: "Какое оформление выберем?", Total: 13,
			Answers: demoOptions([]string{"Светлое", "Тёмное", "Как в системе"}, []int{6, 4, 3})}},
		{Kind: model.MessagePoll, Poll: &model.Poll{ID: 2, Question: "Что добавить в следующую версию?", Total: 5, Multiple: true, Public: true,
			Answers: demoOptions([]string{"Папки чатов", "Темы оформления", "Опросы с картинками", "Звонки"}, []int{3, 2, 1, 4}), Recent: demoVoters[:3]}},
		{Kind: model.MessagePoll, Poll: &model.Poll{ID: demoQuiz.id, Question: "Какой протокол лежит в основе Telegram?", Total: 21, Quiz: true,
			Answers: demoOptions([]string{"MTProto", "XMPP", "HTTP/3"}, []int{15, 4, 2})}},
		{Kind: model.MessagePoll, Poll: &model.Poll{ID: 4, Question: "Где проведём встречу?", Total: 8, Closed: true,
			Answers: demoOptions([]string{"В кафе", "В парке", "Онлайн"}, []int{5, 1, 2})}},
		{Outgoing: true, Todo: &model.Todo{Title: "Подготовка к релизу", OthersCanComplete: true, OthersCanAppend: true, Items: []model.TodoItem{
			{ID: 1, Text: "Собрать сборки для Windows, Linux и macOS", CompletedBy: 2, CompletedByName: "Анна Смирнова", Completed: time.Date(2026, 9, 21, 10, 0, 0, 0, time.Local)},
			{ID: 2, Text: "Проверить опросы и списки задач"},
			{ID: 3, Text: "Обновить переводы", CompletedBy: 5000000001, CompletedByName: "Алексей Петров", Completed: time.Date(2026, 9, 21, 11, 0, 0, 0, time.Local)},
			{ID: 4, Text: "Написать заметки к выпуску"},
		}}},
		{Todo: &model.Todo{Title: "Покупки на выходные", Items: []model.TodoItem{
			{ID: 1, Text: "Хлеб", CompletedBy: 42, CompletedByName: "Демонстрация", Completed: time.Date(2026, 9, 21, 9, 0, 0, 0, time.Local)},
			{ID: 2, Text: "Молоко"},
			{ID: 3, Text: "Кофе"},
		}}},
	}
}

// changeMessage changes the message key in place, as Telegram's updates would.
// The caller holds s.mu.
func (s *Store) changeMessage(key model.MessageKey, change func(*model.Message)) {
	h, ok := s.histories[key.ChatID]
	if !ok {
		return
	}
	for i := range h.Messages {
		if h.Messages[i].Key == key {
			m := h.Messages[i]
			change(&m)
			m.ContentRevision++
			h.Messages[i] = m
			h.Revision++
			s.histories[key.ChatID] = h
			return
		}
	}
}

// Vote implements model.PollStore: the vote counts at once, and a quiz
// tells its answer.
func (s *Store) Vote(msg model.Message, options [][]byte, report func(error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.changeMessage(msg.Key, func(m *model.Message) {
		if m.Poll == nil {
			return
		}
		p := *m.Poll
		p.Answers = append([]model.PollAnswer(nil), p.Answers...)
		voted := p.Voted()
		for i := range p.Answers {
			a := &p.Answers[i]
			if a.Chosen {
				a.Chosen = false
				a.Voters--
			}
			for _, o := range options {
				if bytes.Equal(o, a.Option) {
					a.Chosen = true
					a.Voters++
				}
			}
		}
		switch {
		case voted && len(options) == 0:
			p.Total--
		case !voted && len(options) > 0:
			p.Total++
		}
		if p.Quiz && len(options) > 0 && p.ID == demoQuiz.id {
			for i := range p.Answers {
				p.Answers[i].Correct = bytes.Equal(p.Answers[i].Option, demoQuiz.correct)
			}
			p.Solution = demoQuiz.solution
		}
		if p.Public {
			me := s.me
			p.Recent = append([]model.PollVoter{{ID: me.ID, Name: me.FirstName + " " + me.LastName}}, demoVoters[:2]...)
		}
		m.Poll = &p
	})
}

// PollVotes implements model.PollStore: the demo's people voted for each
// answer, as many as it counts, the account among them for its own.
func (s *Store) PollVotes(_ context.Context, msg model.Message, option []byte, offset string, limit int) (model.PollVotesPage, error) {
	s.mu.Lock()
	me := s.me
	s.mu.Unlock()
	a := msg.Poll.Answer(option)
	if a == nil {
		return model.PollVotesPage{}, nil
	}
	var all []model.PollVote
	date := time.Date(2026, 9, 22, 18, 0, 0, 0, time.Local)
	if a.Chosen {
		all = append(all, model.PollVote{PollVoter: model.PollVoter{ID: me.ID, Name: me.FirstName + " " + me.LastName}, Date: date})
	}
	n, _ := strconv.Atoi(string(option))
	for i := 0; len(all) < a.Voters; i++ {
		all = append(all, model.PollVote{PollVoter: demoVoters[(i+n)%len(demoVoters)], Date: date.Add(-time.Duration(i) * time.Hour)})
	}
	from, _ := strconv.Atoi(offset)
	from = min(from, len(all))
	to := min(from+limit, len(all))
	page := model.PollVotesPage{Count: len(all), List: all[from:to]}
	if to < len(all) {
		page.Next = strconv.Itoa(to)
	}
	return page, nil
}

// ClosePoll implements model.PollStore.
func (s *Store) ClosePoll(msg model.Message, report func(error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.changeMessage(msg.Key, func(m *model.Message) {
		if m.Poll != nil {
			p := *m.Poll
			p.Closed = true
			m.Poll = &p
		}
	})
}

// ToggleTodo implements model.TodoStore: the account marks the task.
func (s *Store) ToggleTodo(msg model.Message, id int, done bool, report func(error)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	me := s.me
	s.changeMessage(msg.Key, func(m *model.Message) {
		if m.Todo != nil {
			next := m.Todo.Toggle(id, done, model.PollVoter{ID: me.ID, Name: me.FirstName + " " + me.LastName}, time.Now())
			m.Todo = &next
		}
	})
}

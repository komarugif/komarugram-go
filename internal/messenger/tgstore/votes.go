// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"errors"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/model"
)

// Polls and checklists: votes, their results as updates bring them, and the
// tasks marked done (model.PollStore, model.TodoStore).

// Vote implements model.PollStore with messages.sendVote, whose updates
// bring the poll's new results.
func (s *Store) Vote(msg model.Message, options [][]byte, report func(error)) {
	s.pollCall(msg, "send vote", report, func(ctx context.Context, api *tg.Client, peer peerRecord) error {
		if options == nil {
			options = [][]byte{}
		}
		updates, err := api.MessagesSendVote(ctx, &tg.MessagesSendVoteRequest{Peer: peer.input(), MsgID: int(msg.Key.MessageID), Options: options})
		if err != nil {
			return err
		}
		s.readOnInteract(msg.Key.ChatID)
		return s.Handle(ctx, updates)
	})
}

// ClosePoll implements model.PollStore, as Telegram Desktop's Polls::close:
// the message's poll is sent again in an edit, closed. Its answers and
// solution are taken from the message as Telegram has it now.
func (s *Store) ClosePoll(msg model.Message, report func(error)) {
	s.pollCall(msg, "close poll", report, func(ctx context.Context, api *tg.Client, peer peerRecord) error {
		ids := []tg.InputMessageClass{&tg.InputMessageID{ID: int(msg.Key.MessageID)}}
		var res tg.MessagesMessagesClass
		var err error
		if peer.Kind == "channel" {
			res, err = api.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{Channel: &tg.InputChannel{ChannelID: peer.ID, AccessHash: peer.Hash}, ID: ids})
		} else {
			res, err = api.MessagesGetMessages(ctx, ids)
		}
		if err != nil {
			return err
		}
		mod, ok := res.AsModified()
		if !ok {
			return errMessageGone
		}
		var media *tg.MessageMediaPoll
		for _, raw := range mod.GetMessages() {
			if m, ok := raw.(*tg.Message); ok && m.ID == int(msg.Key.MessageID) {
				media, _ = m.Media.(*tg.MessageMediaPoll)
			}
		}
		if media == nil {
			return errMessageGone
		}
		poll := media.Poll
		poll.Closed = true
		input := &tg.InputMediaPoll{Poll: poll}
		if poll.Quiz {
			correct := []int{}
			for i, raw := range poll.Answers {
				a, ok := raw.(*tg.PollAnswer)
				if !ok {
					continue
				}
				for _, r := range media.Results.Results {
					if string(r.Option) == string(a.Option) && r.Correct {
						correct = append(correct, i)
					}
				}
			}
			input.SetCorrectAnswers(correct)
		}
		if media.Results.Solution != "" {
			input.SetSolution(media.Results.Solution)
			input.SetSolutionEntities(media.Results.SolutionEntities)
		}
		updates, err := api.MessagesEditMessage(ctx, &tg.MessagesEditMessageRequest{Peer: peer.input(), ID: int(msg.Key.MessageID), Media: input})
		if err != nil {
			return err
		}
		return s.Handle(ctx, updates)
	})
}

// pollCall runs call for msg's chat off the frame, and reports its error.
func (s *Store) pollCall(msg model.Message, where string, report func(error), call func(context.Context, *tg.Client, peerRecord) error) {
	c := s.history
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return
	}
	api, parent, peer := c.api, c.ctx, c.peers[msg.Key.ChatID]
	c.wg.Go(func() {
		defer crash.Recover(where, func(p *crash.Panic) { report(p) })
		if api == nil {
			report(errNotConnected)
			return
		}
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		if err := call(ctx, api, peer); err != nil && !errors.Is(err, context.Canceled) {
			report(err)
		}
	})
}

// PollVotes implements model.PollStore with messages.getPollVotes.
func (s *Store) PollVotes(ctx context.Context, msg model.Message, option []byte, offset string, limit int) (model.PollVotesPage, error) {
	c := s.history
	c.mu.Lock()
	api, peer := c.api, c.peers[msg.Key.ChatID]
	c.mu.Unlock()
	if api == nil {
		return model.PollVotesPage{}, errNotConnected
	}
	req := &tg.MessagesGetPollVotesRequest{Peer: peer.input(), ID: int(msg.Key.MessageID), Limit: limit}
	if option != nil {
		req.SetOption(option)
	}
	if offset != "" {
		req.SetOffset(offset)
	}
	res, err := api.MessagesGetPollVotes(ctx, req)
	if err != nil {
		return model.PollVotesPage{}, err
	}
	s.rememberPeers(res.Users, res.Chats)
	names := peerNamesOf(res.Users, res.Chats)
	page := model.PollVotesPage{Count: res.Count, Next: res.NextOffset}
	for _, raw := range res.Votes {
		var peer tg.PeerClass
		var date int
		switch v := raw.(type) {
		case *tg.MessagePeerVote:
			peer, date = v.Peer, v.Date
		case *tg.MessagePeerVoteInputOption:
			peer, date = v.Peer, v.Date
		case *tg.MessagePeerVoteMultiple:
			peer, date = v.Peer, v.Date
		default:
			continue
		}
		id := peerID(peer)
		page.List = append(page.List, model.PollVote{PollVoter: model.PollVoter{ID: id, Name: names[id]}, Date: time.Unix(int64(date), 0)})
	}
	return page, nil
}

// peerNamesOf names the users and chats of a response.
func peerNamesOf(users []tg.UserClass, chats []tg.ChatClass) map[int64]string {
	names := map[int64]string{}
	for _, u := range users {
		if u, ok := u.(*tg.User); ok {
			names[peerID(&tg.PeerUser{UserID: u.ID})] = personName(u)
		}
	}
	for _, ch := range chats {
		switch ch := ch.(type) {
		case *tg.Chat:
			names[peerID(&tg.PeerChat{ChatID: ch.ID})] = ch.Title
		case *tg.Channel:
			names[peerID(&tg.PeerChannel{ChannelID: ch.ID})] = ch.Title
		}
	}
	return names
}

// applyPoll takes the poll an update tells, into the message it names, or,
// when it names none, into every message shown that has the poll.
func (s *Store) applyPoll(u *tg.UpdateMessagePoll) {
	c := s.history
	c.mu.Lock()
	names := make(map[int64]string, len(c.peers))
	for id, p := range c.peers {
		names[id] = p.Name
	}
	var keys []model.MessageKey
	if u.Peer != nil && u.MsgID != 0 {
		keys = append(keys, model.MessageKey{ChatID: peerID(u.Peer), MessageID: model.MessageID(u.MsgID)})
	} else {
		for _, h := range c.histories {
			for _, m := range h.Messages {
				if m.Poll != nil && m.Poll.ID == u.PollID {
					keys = append(keys, m.Key)
				}
			}
		}
	}
	c.mu.Unlock()
	poll, hasPoll := u.GetPoll()
	for _, key := range keys {
		s.changeMessage(key.ChatID, int(key.MessageID), func(m *model.Message) {
			if m.Poll == nil {
				return
			}
			var next model.Poll
			if hasPoll {
				next = *convertPoll(poll, u.Results, names)
			} else {
				next = pollWithResults(*m.Poll, u.Results, names)
			}
			next = m.Poll.Merge(next, u.Results.Min)
			m.Poll = &next
		})
	}
}

// pollWithResults is p with the results an update tells, which leaves out
// what did not change.
func pollWithResults(p model.Poll, results tg.PollResults, names map[int64]string) model.Poll {
	p.Answers = append([]model.PollAnswer(nil), p.Answers...)
	if list, ok := results.GetResults(); ok {
		for i := range p.Answers {
			a := &p.Answers[i]
			a.Voters, a.Chosen, a.Correct = 0, false, false
			for _, r := range list {
				if string(r.Option) == string(a.Option) {
					a.Voters, a.Chosen, a.Correct = r.Voters, r.Chosen, r.Correct
				}
			}
		}
	}
	if total, ok := results.GetTotalVoters(); ok {
		p.Total = total
	}
	if recent, ok := results.GetRecentVoters(); ok {
		p.Recent = nil
		for _, peer := range recent {
			id := peerID(peer)
			p.Recent = append(p.Recent, model.PollVoter{ID: id, Name: names[id]})
		}
	}
	if solution, ok := results.GetSolution(); ok {
		p.Solution = solution
	}
	return p
}

// ToggleTodo implements model.TodoStore with messages.toggleTodoCompleted.
// The task is marked at once, by the account, and unmarked again when
// Telegram refuses.
func (s *Store) ToggleTodo(msg model.Message, id int, done bool, report func(error)) {
	c := s.history
	chat, msgID := msg.Key.ChatID, int(msg.Key.MessageID)
	me := s.Me()
	who := model.PollVoter{ID: me.ID, Name: personNameOf(me)}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closing {
		return
	}
	// The change is saved to the cache, which must not hold up a frame.
	c.wg.Go(func() {
		defer crash.Recover("toggle todo", func(p *crash.Panic) { report(p) })
		var before *model.Todo
		if !s.changeMessage(chat, msgID, func(m *model.Message) {
			if m.Todo == nil {
				return
			}
			before = m.Todo
			next := m.Todo.Toggle(id, done, who, time.Now())
			m.Todo = &next
		}) || before == nil {
			return
		}
		c.mu.Lock()
		api, parent, peer := c.api, c.ctx, c.peers[chat]
		c.mu.Unlock()
		err := errNotConnected
		if api != nil {
			ctx, cancel := context.WithTimeout(parent, 30*time.Second)
			defer cancel()
			req := &tg.MessagesToggleTodoCompletedRequest{Peer: peer.input(), MsgID: msgID, Completed: []int{}, Incompleted: []int{}}
			if done {
				req.Completed = []int{id}
			} else {
				req.Incompleted = []int{id}
			}
			var updates tg.UpdatesClass
			if updates, err = api.MessagesToggleTodoCompleted(ctx, req); err == nil {
				err = s.Handle(ctx, updates)
				s.readOnInteract(chat)
			}
		}
		if err == nil {
			return
		}
		// Undo, unless something changed the task since.
		s.changeMessage(chat, msgID, func(m *model.Message) {
			if m.Todo == nil {
				return
			}
			for _, item := range m.Todo.Items {
				if item.ID == id && item.Done() == done && (!done || item.CompletedBy == who.ID) {
					for _, old := range before.Items {
						if old.ID == id {
							next := m.Todo.Toggle(id, old.Done(), model.PollVoter{ID: old.CompletedBy, Name: old.CompletedByName}, old.Completed)
							m.Todo = &next
						}
					}
				}
			}
		})
		report(err)
	})
}

// personNameOf is the account's name, as personName gives a user's.
func personNameOf(p model.Profile) string {
	return personName(&tg.User{FirstName: p.FirstName, LastName: p.LastName})
}

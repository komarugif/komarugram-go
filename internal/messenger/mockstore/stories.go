// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"context"
	"errors"
	"slices"
	"time"

	"komarugram/internal/messenger/model"
)

// demoStories are the stories of a peer of the demo, the oldest first, and
// the last of them seen.
type demoStories struct {
	read    int
	stories []model.Message
}

// demoStoryPeers are who has stories in the demo, by the title of their
// chat, with the captions of their stories, and how many of them were
// seen; "" is the account.
var demoStoryPeers = []struct {
	title    string
	captions []string
	read     int
}{
	{"", []string{"Новый рабочий стол"}, 0},
	{"Анна Смирнова", []string{"Утро в горах", "", "Кофе перед походом"}, 0},
	{"Фото природы", []string{"Рассвет над Байкалом", "Туман над озером"}, 1},
	{"Дмитрий Козлов", []string{"Собрал клавиатуру"}, 0},
	{"Material Design", []string{"Новые токены движения"}, 1},
	{"Екатерина", []string{"", ""}, 2},
}

// stories are the demo's stories, made once.
func (s *Store) storiesLocked() map[int64]*demoStories {
	if s.stories != nil {
		return s.stories
	}
	s.stories = map[int64]*demoStories{}
	now := time.Now()
	photo := 1
	for i, p := range demoStoryPeers {
		peer, name := s.me.ID, s.me.FirstName
		if p.title != "" {
			at := slices.IndexFunc(s.chats, func(c model.Chat) bool { return c.Title == p.title })
			if at < 0 {
				continue
			}
			peer, name = s.chats[at].ID, p.title
		}
		d := &demoStories{read: p.read}
		for j, caption := range p.captions {
			posted := now.Add(-time.Duration(len(p.captions)-j) * time.Hour).Add(-time.Duration(i) * 7 * time.Minute)
			story := &model.Story{Likes: (i + j) % 4, Expires: posted.Add(24 * time.Hour)}
			if j == 0 && i == 2 {
				story.Liked = true
			}
			d.stories = append(d.stories, model.Message{
				Key:  model.MessageKey{ChatID: peer, MessageID: model.StoryMessage(j + 1)},
				Kind: model.MessagePhoto, Media: demoPhoto(photo), Text: caption, SenderName: name, Date: posted,
				Views: 40 * (i + 1) * (j + 2), Story: story,
			})
			photo++
		}
		s.stories[peer] = d
	}
	return s.stories
}

// StoryPeers implements model.StorySource.
func (s *Store) StoryPeers() []model.StoryPeer {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.StoryPeer
	for peer, d := range s.storiesLocked() {
		p := model.StoryPeer{ID: peer, Kind: model.KindUser, Self: peer == s.me.ID, Count: len(d.stories), Unread: len(d.stories) - d.read}
		if at := slices.IndexFunc(s.chats, func(c model.Chat) bool { return c.ID == peer }); at >= 0 {
			p.Kind, p.Title = s.chats[at].Kind, s.chats[at].Title
		}
		if p.Self {
			p.Title = s.me.FirstName
		}
		p.Last = d.stories[len(d.stories)-1].Date
		p.Open = d.stories[min(d.read, len(d.stories)-1)].Key.MessageID
		out = append(out, p)
	}
	// As Telegram's store orders them: the account, the unread, the latest.
	slices.SortFunc(out, func(a, b model.StoryPeer) int {
		switch {
		case a.Self != b.Self:
			return boolOrder(a.Self)
		case (a.Unread > 0) != (b.Unread > 0):
			return boolOrder(a.Unread > 0)
		}
		return b.Last.Compare(a.Last)
	})
	return out
}

func boolOrder(first bool) int {
	if first {
		return -1
	}
	return 1
}

// PeerStories implements model.StorySource.
func (s *Store) PeerStories(_ context.Context, peer int64) ([]model.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.storiesLocked()[peer]
	if d == nil {
		return nil, nil
	}
	return slices.Clone(d.stories), nil
}

// ReadStory implements model.StorySource.
func (s *Store) ReadStory(peer int64, id model.MessageID) {
	s.mu.Lock()
	d := s.storiesLocked()[peer]
	changed := false
	if d != nil && model.StoryID(id) > d.read {
		d.read, changed = model.StoryID(id), true
	}
	notify := s.changed
	s.mu.Unlock()
	if changed && notify != nil {
		notify()
	}
}

// LikeStory implements model.StorySource.
func (s *Store) LikeStory(_ context.Context, peer int64, id model.MessageID, like bool) error {
	s.mu.Lock()
	d := s.storiesLocked()[peer]
	at := -1
	if d != nil {
		at = slices.IndexFunc(d.stories, func(m model.Message) bool { return m.Key.MessageID == id })
	}
	if at < 0 {
		s.mu.Unlock()
		return errors.New("story unknown")
	}
	story := *d.stories[at].Story
	if story.Liked != like {
		story.Liked = like
		if like {
			story.Likes++
		} else {
			story.Likes--
		}
	}
	d.stories[at].Story = &story
	notify := s.changed
	s.mu.Unlock()
	if notify != nil {
		notify()
	}
	return nil
}

// StoryNow implements model.StorySource.
func (s *Store) StoryNow(peer int64, id model.MessageID) (model.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.storiesLocked()[peer]
	if d == nil {
		return model.Message{}, false
	}
	at := slices.IndexFunc(d.stories, func(m model.Message) bool { return m.Key.MessageID == id })
	if at < 0 {
		return model.Message{}, false
	}
	return d.stories[at], true
}

// demoChatStories are the messages of the demo's histories that carry
// stories, or reply to one, keyed and dated by the history. s.mu is held.
func (s *Store) demoChatStories() []model.Message {
	stories := s.storiesLocked()
	var out []model.Message
	carry := func(peer int64, n int, mention bool) {
		d := stories[peer]
		if d == nil || n >= len(d.stories) {
			return
		}
		story := d.stories[n]
		m := model.Message{Kind: story.Kind, Media: story.Media, Text: story.Text, SenderID: 42, SenderName: "Анна",
			StoryOf: &model.StoryRef{Peer: peer, ID: model.StoryID(story.Key.MessageID), From: story.SenderName, Mention: mention}}
		if mention {
			m.Kind, m.Text, m.Service = model.MessageService, "", &model.ServiceAction{Kind: model.ServiceStoryMention}
		}
		out = append(out, m)
	}
	peerOf := func(title string) int64 {
		if at := slices.IndexFunc(s.chats, func(c model.Chat) bool { return c.Title == title }); at >= 0 {
			return s.chats[at].ID
		}
		return 0
	}
	nature, anna := peerOf("Фото природы"), peerOf("Анна Смирнова")
	carry(nature, 0, false)
	out = append(out, model.Message{Kind: model.MessageText, SenderID: 42, SenderName: "Анна",
		StoryOf: &model.StoryRef{Peer: nature, ID: 9, From: "Фото природы", Expired: true}})
	carry(anna, 1, true)
	carry(nature, 1, false)
	out = append(out, model.Message{Kind: model.MessageText, Text: "Какой вид!", Outgoing: true,
		ReplyToStory: &model.StoryRef{Peer: anna, ID: 1, From: "Анна Смирнова"}})
	return out
}

// ChatStories implements model.ChatStoryGallery over the demo history.
func (s *Store) ChatStories(ctx context.Context, chat int64, anchor model.MessageID, dir, limit int) (model.GalleryPage, error) {
	var found []model.Message
	for _, m := range s.History(chat).Messages {
		if m.StoryShown() && (dir < 0 && m.Key.MessageID < anchor || dir > 0 && m.Key.MessageID > anchor) {
			found = append(found, m.AsStory())
		}
	}
	page := model.GalleryPage{More: len(found) > limit}
	if dir < 0 {
		page.Messages = found[max(0, len(found)-limit):]
	} else {
		page.Messages = found[:min(len(found), limit)]
	}
	return page, ctx.Err()
}

// Story implements model.StorySource with the demo's stories.
func (s *Store) Story(ctx context.Context, peer int64, id model.MessageID) (model.Message, error) {
	if m, ok := s.StoryNow(peer, id); ok {
		return m, ctx.Err()
	}
	return model.Message{}, errors.New("the story is no longer available")
}

// ShareStory implements model.StorySource: the demo sends nothing.
func (s *Store) ShareStory(context.Context, int64, model.MessageID, int64) error {
	return nil
}

var _ model.StorySource = (*Store)(nil)

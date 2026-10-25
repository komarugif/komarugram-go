package mockstore

import (
	"context"
	"komarugram/internal/messenger/model"
	"sort"
	"strconv"
	"time"
)

func (s *Store) SharedCounts(ctx context.Context, chat int64) (map[model.SharedKind]int, error) {
	out := map[model.SharedKind]int{}
	for _, k := range model.SharedKinds {
		p, _ := s.SharedMedia(ctx, chat, k, 0, 100)
		out[k] = p.Total
	}
	out[model.SharedGifts] = 9
	out[model.SharedStories] = 0
	out[model.SharedGroups] = 0
	return out, nil
}
func (s *Store) SharedMedia(ctx context.Context, chat int64, kind model.SharedKind, before model.MessageID, limit int) (model.SharedPage, error) {
	page := model.SharedPage{}
	for _, m := range s.History(chat).Messages {
		match := false
		switch kind {
		case model.SharedPhotos:
			match = m.Kind == model.MessagePhoto
		case model.SharedVideos:
			match = m.Kind == model.MessageVideo
		case model.SharedFiles:
			match = m.Kind == model.MessageFile
		case model.SharedMusic:
			match = m.Kind == model.MessageMusic
		case model.SharedVoice:
			match = m.Kind == model.MessageVoice || m.Kind == model.MessageRound
		case model.SharedGIFs:
			match = m.Kind == model.MessageGIF
		case model.SharedPolls:
			match = m.Poll != nil
		case model.SharedSaved:
			match = m.Key.MessageID%70 == 0
		case model.SharedLinks:
			for _, e := range m.Entities {
				if e.Kind == "url" {
					match = true
				}
			}
		}
		// A story's photo or video is no photo or video of the chat's, as
		// Telegram's filters have them.
		if match && m.StoryOf == nil {
			page.Total++
			if before == 0 || m.Key.MessageID < before {
				page.Messages = append(page.Messages, m)
			}
		}
	}
	sort.Slice(page.Messages, func(i, j int) bool { return page.Messages[i].Key.MessageID > page.Messages[j].Key.MessageID })
	page.More = len(page.Messages) > limit
	if page.More {
		page.Messages = page.Messages[:limit]
	}
	if len(page.Messages) > 0 {
		page.Next = page.Messages[len(page.Messages)-1].Key.MessageID
	}
	return page, nil
}

func (s *Store) SharedCollection(ctx context.Context, chat int64, kind model.SharedKind, cursor string, limit int) (model.SharedCollectionPage, error) {
	p := model.SharedCollectionPage{}
	if kind != model.SharedGifts {
		return p, nil
	}
	p.Total = 9
	start, _ := strconv.Atoi(cursor)
	end := min(p.Total, start+limit)
	for i := start; i < end; i++ {
		p.Messages = append(p.Messages, demoGift(chat, i))
	}
	if end < p.Total {
		p.Next = strconv.Itoa(end)
	}
	return p, nil
}

// demoGift is the demo's saved gift i of chat: every second one is unique.
func demoGift(chat int64, i int) model.Message {
	g := &model.Gift{ID: strconv.Itoa(i), Title: "Demo Gift", Stars: 100, SenderID: 2, SenderName: "Анна Смирнова"}
	if i%2 == 1 {
		g.Unique = true
		g.Number = 1000 + i
		g.Model = "Aurora"
		g.Symbol = "Flower"
		g.Backdrop = "Lavender"
		g.ModelRarity = 35
		g.SymbolRarity = 20
		g.BackdropRarity = 12
		g.Issued = 950
		g.Total = 1000
		g.CenterColor = 0xbba2e0
		g.EdgeColor = 0x765caa
		g.PatternColor = 0xe5cfff
		g.TextColor = 0xffffff
		g.HasBackdrop = true
		g.Pattern = &model.MessageMedia{ID: "demo/tgs", MIMEType: "application/x-tgsticker", Width: 512, Height: 512}
	}
	return model.Message{Key: model.MessageKey{AccountID: "demo", ChatID: chat, MessageID: model.MessageID(-i - 1)}, Date: time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC), Kind: model.MessageSticker, Media: &model.MessageMedia{ID: "demo/tgs", MIMEType: "application/x-tgsticker", Width: 512, Height: 512}, Gift: g, ContentRevision: 1}
}

// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"regexp"
	"slices"
	"strings"

	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/preferences"
)

// messageFilter hides others' messages as AyuGram's message filters do:
// those of blocked users in every chat, and those a pattern matches, or a
// reversed one does not, in channels, or in every chat with InChats.
type messageFilter struct {
	source preferences.Filters
	shared []filterPattern
	byChat map[int64][]filterPattern
}

type filterPattern struct {
	re       *regexp.Regexp
	reversed bool
}

// compileFilter compiles f's patterns; one that does not compile hides
// nothing.
func compileFilter(f preferences.Filters) *messageFilter {
	out := &messageFilter{source: f, byChat: map[int64][]filterPattern{}}
	for _, p := range f.Patterns {
		text := p.Text
		if p.CaseInsensitive {
			text = "(?i)" + text
		}
		re, err := regexp.Compile(text)
		if err != nil || p.Text == "" {
			continue
		}
		fp := filterPattern{re: re, reversed: p.Reversed}
		if p.Chat == 0 {
			out.shared = append(out.shared, fp)
		} else {
			out.byChat[p.Chat] = append(out.byChat[p.Chat], fp)
		}
	}
	return out
}

// same reports whether f was compiled from g.
func (f *messageFilter) same(g preferences.Filters) bool {
	a := f.source
	return a.Enabled == g.Enabled && a.InChats == g.InChats && a.HideBlocked == g.HideBlocked && slices.Equal(a.Patterns, g.Patterns)
}

// hides reports whether m, a message of chat, of kind, is hidden; blocked
// tells the peers the account blocked.
func (f *messageFilter) hides(m model.Message, chat int64, kind model.ChatKind, blocked func(int64) bool) bool {
	if f == nil || !f.source.Enabled || m.Outgoing || m.Kind == model.MessageService {
		return false
	}
	if f.source.HideBlocked && blocked != nil {
		// A private chat with a blocked user still shows its messages.
		if m.SenderID != 0 && m.SenderID != chat && blocked(m.SenderID) || m.ForwardFromID != 0 && blocked(m.ForwardFromID) {
			return true
		}
	}
	if kind != model.KindChannel && !f.source.InChats {
		return false
	}
	text := filterText(m)
	if text == "" {
		return false
	}
	for _, p := range append(f.byChat[chat], f.shared...) {
		if p.re.MatchString(text) != p.reversed {
			return true
		}
	}
	return false
}

// filterText is all the text of m that filters look at: its text or
// caption, a poll, a checklist, a file's name and a link's preview.
func filterText(m model.Message) string {
	parts := []string{m.Text}
	if m.Poll != nil {
		parts = append(parts, m.Poll.Question)
		for _, a := range m.Poll.Answers {
			parts = append(parts, a.Text)
		}
	}
	if m.Todo != nil {
		parts = append(parts, m.Todo.Title)
		for _, item := range m.Todo.Items {
			parts = append(parts, item.Text)
		}
	}
	if m.Media != nil && m.Media.FileName != "" {
		parts = append(parts, m.Media.FileName)
	}
	if w := m.WebPage; w != nil {
		parts = append(parts, w.Title, w.Description)
	}
	for _, part := range m.Attachments {
		parts = append(parts, part.Text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// filterMessages drops what the page's filter hides, unless the chat shows
// it; filtered counts what was dropped.
func (p *chatPage) filterMessages(msgs []model.Message, chat int64) []model.Message {
	p.filtered = 0
	if p.filter == nil || !p.filter.source.Enabled {
		return msgs
	}
	var blocked func(int64) bool
	if b, ok := p.source.(model.BlockedSource); ok {
		blocked = b.Blocked
	}
	out := make([]model.Message, 0, len(msgs))
	for _, m := range msgs {
		if p.filter.hides(m, chat, p.kind, blocked) {
			p.filtered++
			if !p.showFiltered[chat] {
				continue
			}
		}
		out = append(out, m)
	}
	return out
}

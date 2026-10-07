// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"komarugram/internal/messenger/model"
)

// The drafts bots stream (model.DraftStopper) come at the end of the
// history, as messages with Streaming set: a ring turns in their footer,
// their buttons do nothing, and they have no menu, no reactions and no
// place in a selection until they are messages.

// stoppableDraft reports whether a bot streams a draft in the chat that the
// account may stop.
func (p *chatPage) stoppableDraft() bool {
	if _, ok := p.source.(model.DraftStopper); !ok {
		return false
	}
	for i := len(p.messages) - 1; i >= 0 && p.messages[i].Streaming; i-- {
		if p.messages[i].Stoppable {
			return true
		}
	}
	return false
}

// stopStreamedDraft stops the newest draft a bot streams in the chat.
func (p *chatPage) stopStreamedDraft() {
	s, ok := p.source.(model.DraftStopper)
	if !ok {
		return
	}
	if err := s.StopStreamedDraft(p.chat); err != nil {
		p.toast.Show(mediaErrorText(err))
	}
	p.invalidate()
}

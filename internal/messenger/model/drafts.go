// SPDX-License-Identifier: Unlicense OR MIT

package model

// A bot may stream a message as it writes it (Telegram's live message
// streaming, https://core.telegram.org/api/bots/ai#live-message-streaming):
// typing updates carry the text so far, or the article, until the message
// it becomes is sent. A store shows such a draft at the end of its chat's
// history, or of the thread it is in, as a Message with Streaming set and
// an id below 0, which no other message has; the cache keeps none of it.
// The draft goes when the message it becomes comes, when the bot stops it,
// and after half a minute without news, as Telegram Desktop's
// HistoryStreamedDrafts.

// DraftStopper stops the drafts a bot streams in a chat, when the bot lets
// the account stop them (Message.Stoppable): the newest of them, as
// Telegram Desktop's stop button does.
type DraftStopper interface {
	StopStreamedDraft(chat int64) error
}

// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// stampTarget is the media the timestamps of a message's text name times
// of, as Telegram Desktop links them (AddTimestampLinks): the message's own
// video, video message, voice message or music, or its link's YouTube
// video, or else those of the message it replies to. limit is the latest
// time a timestamp may name; zero, there is no media, and the timestamps
// are text.
type stampTarget struct {
	// msg is the message whose media plays; for the video of a link, a
	// message of that video alone, as the link's card plays it.
	msg model.Message
	// url is the page of a YouTube video, which opens at the time.
	url   string
	limit time.Duration
}

// same reports whether t links the timestamps as o does.
func (t stampTarget) same(o stampTarget) bool {
	return t.msg.Key == o.msg.Key && t.msg.Kind == o.msg.Kind && t.url == o.url && t.limit == o.limit
}

// stampTargetOf is what the timestamps of m name times of: m's own media,
// or else the media of the message it replies to, once that is loaded.
func (p *chatPage) stampTargetOf(m model.Message) stampTarget {
	if t := stampTargetIn(m); t.limit > 0 {
		return t
	}
	if m.ReplyToMessageID == 0 {
		return stampTarget{}
	}
	replied, state, _ := p.referenced(m.Key.ChatID, m.ReplyToMessageID)
	if state != model.LookupFound {
		return stampTarget{}
	}
	return stampTargetIn(replied)
}

// stampTargetIn is the media of m that a timestamp may name a time of.
func stampTargetIn(m model.Message) stampTarget {
	limit := model.TimestampLimit(m)
	if limit <= 0 {
		return stampTarget{}
	}
	if w := m.WebPage; w != nil {
		if w.Video != nil {
			vm := model.Message{Key: m.Key, Kind: w.VideoKind, Media: w.Video, Date: m.Date, SenderID: m.SenderID, NoForwards: m.NoForwards}
			return stampTarget{msg: vm, limit: limit}
		}
		return stampTarget{msg: model.Message{Key: m.Key}, url: w.URL, limit: limit}
	}
	return stampTarget{msg: m, limit: limit}
}

// refreshStamps links the timestamps of r's text, the row of m, to what
// they name times of, writing its runs again when that changed: the
// message replied to may load after the row is made.
func (p *chatPage) refreshStamps(gtx layout.Context, r *messageRow, m model.Message, l localization.Catalog) {
	if !r.stampable {
		return
	}
	t := p.stampTargetOf(m)
	if t.same(r.stamp) {
		r.stamp = t
		return
	}
	r.stamp = t
	r.rewrite(gtx, m, l)
}

// openTimestamp plays what r's timestamp value names a time of from that
// time: in the client's player, as a voice message, music or a video
// message plays; in the external player, as a video does, or one playing
// there already moves to it; or a YouTube video's page, at the time.
func (p *chatPage) openTimestamp(gtx layout.Context, r *messageRow, value string, l localization.Catalog) {
	t := r.stamp
	at, ok := model.ParseTimestamp(value)
	if !ok || t.limit <= 0 || at > t.limit {
		return
	}
	switch {
	case t.url != "":
		p.askLink(model.TimestampURL(t.url, at))
	case internalAudio(t.msg) && (p.audioExternal == nil || !p.audioExternal()):
		p.audio.playFrom(p, t.msg, at)
	case t.msg.Kind == model.MessageGIF && t.msg.Media.Whole():
		// A GIF that plays in the chat goes there, and plays on.
		p.media.SeekGIF(t.msg, at, true)
		gtx.Execute(op.InvalidateCmd{})
	default:
		p.playVideo(gtx, t.msg, at, l)
	}
}

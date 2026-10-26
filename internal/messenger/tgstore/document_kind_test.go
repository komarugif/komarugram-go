// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// A document's kind is made by its attributes in their order, as Telegram
// Desktop makes it, and the chat list names it as the history shows it.
func TestDocumentKindByAttributeOrder(t *testing.T) {
	round := &tg.DocumentAttributeVideo{RoundMessage: true, Duration: 1.5, W: 384, H: 384}
	video := &tg.DocumentAttributeVideo{Duration: 3, W: 640, H: 360}
	for _, tc := range []struct {
		name  string
		mime  string
		attrs []tg.DocumentAttributeClass
		kind  model.MessageKind
		key   string
	}{
		// A video message whose sender added an audio attribute after its
		// video's, as the one in @komarugram_labs.
		{"round then audio", "video/mp4", []tg.DocumentAttributeClass{round, &tg.DocumentAttributeAudio{Duration: 1}}, model.MessageRound, "preview.round"},
		{"voice then video", "audio/ogg", []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{Voice: true, Duration: 2}, video}, model.MessageVoice, "preview.voice"},
		{"video then animated", "video/mp4", []tg.DocumentAttributeClass{video, &tg.DocumentAttributeAnimated{}}, model.MessageGIF, "preview.gif"},
		{"round then animated", "video/mp4", []tg.DocumentAttributeClass{round, &tg.DocumentAttributeAnimated{}}, model.MessageRound, "preview.round"},
		{"video sticker", "video/webm", []tg.DocumentAttributeClass{video, &tg.DocumentAttributeSticker{Alt: "😀", Stickerset: &tg.InputStickerSetEmpty{}}}, model.MessageSticker, "preview.sticker"},
		{"music", "audio/mpeg", []tg.DocumentAttributeClass{&tg.DocumentAttributeAudio{Title: "Song", Duration: 200}}, model.MessageMusic, "preview.music"},
		{"plain gif", "image/gif", nil, model.MessageGIF, "preview.gif"},
		{"file", "application/pdf", []tg.DocumentAttributeClass{&tg.DocumentAttributeFilename{FileName: "a.pdf"}}, model.MessageFile, "preview.file"},
	} {
		d := &tg.Document{ID: 1, MimeType: tc.mime, Attributes: tc.attrs}
		kind, meta, _ := documentMedia(d)
		if kind != tc.kind {
			t.Errorf("%s: kind %v, want %v", tc.name, kind, tc.kind)
		}
		if key, _ := mediaKey(&tg.MessageMediaDocument{Document: d}); key != tc.key {
			t.Errorf("%s: the chat list says %q, want %q", tc.name, key, tc.key)
		}
		if tc.name == "round then audio" && meta.Duration != 1500*time.Millisecond {
			t.Errorf("%s: lasts %v, the video's 1.5s", tc.name, meta.Duration)
		}
	}
}

// A message forwarded from a channel's post keeps which post it was, which
// its channel opens at; one from a user has none.
func TestForwardPost(t *testing.T) {
	channel := &tg.MessageFwdHeader{Date: 10, FromID: &tg.PeerChannel{ChannelID: 42}}
	channel.SetChannelPost(15)
	posted := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 5}}
	posted.SetFwdFrom(*channel)
	m, _ := convertMessage("a", posted, nil)
	if m.ForwardPostID != 15 || m.ForwardFromID != peerID(&tg.PeerChannel{ChannelID: 42}) {
		t.Fatalf("forward %d of %d", m.ForwardPostID, m.ForwardFromID)
	}
	user := &tg.MessageFwdHeader{Date: 10, FromID: &tg.PeerUser{UserID: 7}}
	user.SetChannelPost(3)
	sent := &tg.Message{ID: 2, PeerID: &tg.PeerUser{UserID: 5}}
	sent.SetFwdFrom(*user)
	m, _ = convertMessage("a", sent, nil)
	if m.ForwardPostID != 0 {
		t.Fatalf("a user's forward has post %d", m.ForwardPostID)
	}
}

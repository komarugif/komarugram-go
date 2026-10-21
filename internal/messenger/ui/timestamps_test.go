// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"strings"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// clickTimestamp makes the row of m, as the history does, and clicks its
// timestamp value; it fails when there is none.
func clickTimestamp(t *testing.T, p *chatPage, m model.Message, value string) *messageRow {
	t.Helper()
	l := localization.For("en")
	gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Values: map[string]any{}}
	r := newMessageRow(m, l, gtx.Now)
	p.refreshStamps(gtx, r, m, l)
	for _, run := range r.runs {
		if run.Action == "timestamp" && run.Value == value {
			p.activateRun(gtx, r, run)
			return r
		}
	}
	t.Fatalf("no timestamp %s in %q: %+v", value, m.Text, r.runs)
	return nil
}

// A timestamp in a reply to a voice message plays the voice message from
// its time, and moves it there while it plays.
func TestTimestampInReplyPlaysVoiceFromIt(t *testing.T) {
	p, _, find := audioHarnessPage(t)
	voice := find("demo/voice")
	reply := model.Message{Key: model.MessageKey{ChatID: voice.Key.ChatID, MessageID: 9001}, Kind: model.MessageText,
		Text: "listen from 0:05, not 0:09", ReplyToMessageID: voice.Key.MessageID, ContentRevision: 1}
	p.messages = []model.Message{voice, reply}

	// Past the end of the 7.3 seconds, a time names nothing.
	r := clickTimestamp(t, p, reply, "0:05")
	for _, run := range r.runs {
		if run.Action == "timestamp" && run.Value == "0:09" {
			t.Fatal("0:09 is linked in a voice message of 7 seconds")
		}
	}
	waitAudio(t, "it did not start", func() bool { return p.audio.state(voice).playing })
	if s := p.audio.state(voice); s.elapsed < 4900*time.Millisecond || s.elapsed > 5100*time.Millisecond {
		t.Fatalf("it plays from %v, not 0:05", s.elapsed)
	}
	reply.Text = "or from 0:02"
	clickTimestamp(t, p, reply, "0:02")
	if s := p.audio.state(voice); !s.playing || s.elapsed < 1900*time.Millisecond || s.elapsed > 2100*time.Millisecond {
		t.Fatalf("it did not move to 0:02: %+v", s)
	}
}

// Until the message replied to is there, the timestamps of a reply are
// text; once it is, they link to its media.
func TestTimestampLinksOnceRepliedLoads(t *testing.T) {
	p, _, find := audioHarnessPage(t)
	voice := find("demo/voice")
	reply := model.Message{Key: model.MessageKey{ChatID: voice.Key.ChatID, MessageID: 9001}, Kind: model.MessageText,
		Text: "0:03", ReplyToMessageID: voice.Key.MessageID, ContentRevision: 1}
	p.messages = []model.Message{{Key: model.MessageKey{ChatID: voice.Key.ChatID, MessageID: voice.Key.MessageID}, Kind: model.MessageText, Text: "edited"}, reply}
	l := localization.For("en")
	gtx := layout.Context{Ops: new(op.Ops), Now: time.Now(), Values: map[string]any{}}
	r := newMessageRow(reply, l, gtx.Now)
	p.refreshStamps(gtx, r, reply, l)
	if len(r.runs) != 1 || r.runs[0].Action != "" {
		t.Fatalf("a reply to text links its timestamp: %+v", r.runs)
	}
	p.messages[0] = voice
	p.refreshStamps(gtx, r, reply, l)
	if len(r.runs) != 1 || r.runs[0].Action != "timestamp" {
		t.Fatalf("a reply to a voice message does not link its timestamp: %+v", r.runs)
	}
}

// A timestamp in a message with a link to a YouTube video asks to open
// the video at its time.
func TestTimestampOpensYouTubeAtIt(t *testing.T) {
	p, _, _ := audioHarnessPage(t)
	m := model.Message{Key: model.MessageKey{ChatID: 2, MessageID: 9002}, Kind: model.MessageText,
		Text: "https://www.youtube.com/watch?v=abc from 1:05:30", ContentRevision: 1,
		Entities: []model.Entity{{Kind: "url", Offset: 0, Length: 36}},
		WebPage:  &model.WebPreview{URL: "https://www.youtube.com/watch?v=abc", Site: "YouTube", Type: "video"}}
	clickTimestamp(t, p, m, "1:05:30")
	if want := "https://www.youtube.com/watch?v=abc&t=3930"; p.link != want {
		t.Fatalf("asks to open %q, not %q", p.link, want)
	}
}

// In the history, a click on a timestamp of a reply to a link to a YouTube
// video asks to open the video at its time: the reply's row finds the
// message it replies to, and links its timestamps once it is drawn.
func TestTimestampClickInHistory(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
		messages[0].Text = "https://youtu.be/abc"
		messages[0].WebPage = &model.WebPreview{URL: "https://youtu.be/abc", Site: "YouTube", Type: "video", Duration: 600}
		messages[1].Text = strings.TrimSpace(strings.Repeat("0:15 ", 30))
		messages[1].ReplyToMessageID = 1
	})
	h.frames(2)
	r := h.page.rows[2]
	if r == nil || r.stamp.url == "" {
		t.Fatal("the reply's timestamps link to nothing")
	}
	// From the bottom of the bubble up, where its text is: a click on the
	// quote over it would show the message replied to.
	at := h.messageAt(2)
	for y := at.Y + 40; y > at.Y-10 && h.page.link == ""; y -= 4 {
		for x := float32(30); x < 420 && h.page.link == ""; x += 10 {
			// Clicks a moment apart, not a double click.
			h.now = h.now.Add(time.Second)
			h.press(pointer.ButtonPrimary, f32.Pt(x, y))
		}
	}
	if want := "https://youtu.be/abc?t=15"; h.page.link != want {
		t.Fatalf("asks to open %q, not %q", h.page.link, want)
	}
}

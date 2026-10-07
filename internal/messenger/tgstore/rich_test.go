// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

func plain(s string) tg.RichTextClass { return &tg.TextPlain{Text: s} }

// richFixture is a rich message with most kinds of blocks and of text, as
// Telegram sends it: encoded and decoded, which sets its flags. part marks
// it cut short.
func richFixture(part bool) tg.RichMessage {
	var b bin.Buffer
	r := richFixtureFields()
	r.Part = part
	if err := r.Encode(&b); err != nil {
		panic(err)
	}
	var out tg.RichMessage
	if err := out.Decode(&b); err != nil {
		panic(err)
	}
	return out
}

func richFixtureFields() tg.RichMessage {
	callback := &tg.InlineButtonTypeCallback{Data: []byte("d")}
	var buttons []tg.PageButton
	for i := 1; i <= 9; i++ {
		buttons = append(buttons, tg.PageButton{Text: plain(fmt.Sprintf("b%d", i)), Type: &tg.InlineButtonTypeURL{URL: "https://e.org"}})
	}
	return tg.RichMessage{
		Blocks: []tg.PageBlockClass{
			&tg.PageBlockTitle{Text: plain("Rich demo")},
			&tg.PageBlockParagraph{Text: &tg.TextConcat{Texts: []tg.RichTextClass{
				plain("😀 "), &tg.TextBold{Text: plain("bold")}, plain(" "),
				&tg.TextURL{Text: plain("link"), URL: "https://e.org"}, plain(" "),
				&tg.TextMentionName{Text: plain("Ann"), UserID: 7}, plain(" "),
				&tg.TextCustomEmoji{DocumentID: 5, Alt: "👍"}, plain(" "),
				&tg.TextMath{Source: "$x^2$"}, plain(" "),
				&tg.TextDate{Text: plain("tomorrow"), Date: 1700000000, ShortDate: true}, plain(" "),
				&tg.TextButton{Text: plain("Go"), Type: callback},
				&tg.TextAnchor{Text: plain(""), Name: "Intro"},
			}}},
			&tg.PageBlockPreformatted{Text: &tg.TextURL{Text: plain("fmt.Println()"), URL: "https://e.org"}, Language: " go "},
			&tg.PageBlockList{Items: []tg.PageListItemClass{
				&tg.PageListItemText{Checkbox: true, Checked: true, Text: plain("done")},
				&tg.PageListItemText{Text: plain("plain")},
				&tg.PageListItemBlocks{Blocks: []tg.PageBlockClass{&tg.PageBlockParagraph{Text: plain("para")}}},
			}},
			&tg.PageBlockOrderedList{Type: "i", Start: 3, Items: []tg.PageListOrderedItemClass{
				&tg.PageListOrderedItemText{Text: plain("three")},
				&tg.PageListOrderedItemText{Text: plain("nine"), Value: 9},
			}},
			&tg.PageBlockBlockquote{Collapsed: true, Text: plain("quoted"), Caption: plain("author")},
			&tg.PageBlockPhoto{PhotoID: 10, Caption: tg.PageCaption{Text: plain("A photo"), Credit: plain("by me")}},
			&tg.PageBlockTable{Title: plain("T"), Rows: []tg.PageTableRow{{Cells: []tg.PageTableCell{{Header: true, AlignCenter: true, Text: plain("a"), Colspan: 2}}}}},
			&tg.PageBlockMath{Source: "E=mc^2"},
			&tg.PageBlockButtonRow{AlignCenter: true, Buttons: buttons},
			&tg.PageBlockAnchor{Name: "#Sec%20One"},
			&tg.PageBlockDivider{},
		},
		Photos: []tg.PhotoClass{&tg.Photo{ID: 10, AccessHash: 1, FileReference: []byte{1}, DCID: 2, Sizes: []tg.PhotoSizeClass{
			&tg.PhotoSize{Type: "x", W: 800, H: 600, Size: 1000},
			&tg.PhotoSize{Type: "m", W: 320, H: 240, Size: 100},
		}}},
	}
}

const richFixtureSummary = "Rich demo\n😀 bold link Ann 👍 x^2 tomorrow Go\nfmt.Println()\n[x] done\n- plain\n- para\niii. three\nix. nine\nquoted\nauthor\nA photo\nby me\nT\nE=mc^2\nb1\nb2\nb3\nb4\nb5\nb6\nb7\nb8"

func entityOf(t *testing.T, m model.Message, kind string) model.Entity {
	t.Helper()
	for _, e := range m.Entities {
		if e.Kind == kind {
			return e
		}
	}
	t.Fatalf("no %s entity in %+v", kind, m.Entities)
	return model.Entity{}
}

// A rich message arrives with empty text: it gets its article, and its
// summary for text, as Telegram Desktop shows it.
func TestRichMessageConverts(t *testing.T) {
	msg := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 4}}
	msg.SetRichMessage(richFixture(true))
	m, _ := convertMessage("a", msg, nil)
	if m.Rich == nil || !m.Rich.Part {
		t.Fatalf("no page: %+v", m.Rich)
	}
	if m.Text != richFixtureSummary {
		t.Fatalf("summary %q", m.Text)
	}
	units := utf16.Encode([]rune(m.Text))
	text := func(e model.Entity) string {
		return string(utf16.Decode(units[e.Offset : e.Offset+e.Length]))
	}
	for kind, want := range map[string]string{"bold": "Rich demo", "url": "link", "emoji": "👍", "math": "x^2", "date": "tomorrow", "button": "Go", "pre": "fmt.Println()", "quote": "quoted\nauthor"} {
		if got := text(entityOf(t, m, kind)); got != want {
			t.Errorf("%s entity over %q, not %q", kind, got, want)
		}
	}
	if e := entityOf(t, m, "pre"); e.Language != "go" {
		t.Errorf("code language %q", e.Language)
	}
	if e := entityOf(t, m, "quote"); !e.Collapsed {
		t.Error("the quote is not collapsed")
	}
	if e := entityOf(t, m, "date"); e.Date != 1700000000 || e.DateFormat != model.DateShortDate {
		t.Errorf("date %+v", e)
	}
	if e := entityOf(t, m, "button"); e.Button == nil || e.Button.Kind != "callback" || string(e.Button.Data) != "d" || e.Button.Text != "Go" {
		t.Errorf("button %+v", e.Button)
	}
	bold := 0
	for _, e := range m.Entities {
		if e.Kind == "bold" {
			bold++
		}
		if e.Kind == "url" && e.URL == "tg://user?id=7" && text(e) != "Ann" {
			t.Errorf("mention by ID over %q", text(e))
		}
	}
	if bold != 2 {
		t.Errorf("%d bold entities, not the title and the word", bold)
	}

	blocks := m.Rich.Blocks
	if blocks[0].Kind != model.RichHeading || blocks[0].Level != 1 {
		t.Errorf("title %+v", blocks[0])
	}
	// The anchor at the end of the paragraph stays there, at its line.
	if p := blocks[1]; p.Anchor != "" || len(p.Text.Anchors) != 1 || p.Text.Anchors[0] != "intro" || p.Text.AnchorOffset(0) != model.UTF16Len(p.Text.Text) {
		t.Errorf("paragraph anchor %q, inline %v at %v", p.Anchor, p.Text.Anchors, p.Text.AnchorAt)
	}
	if code := blocks[2]; code.Language != "go" || len(code.Text.Entities) != 0 {
		t.Errorf("code %+v: a code block keeps no links", code)
	}
	if list := blocks[3]; len(list.Items) != 3 || list.Items[2].Text.Text != "para" || list.Items[2].Blocks != nil {
		t.Errorf("list %+v", list)
	}
	if photo := blocks[6]; photo.Kind != model.RichMediaBlock || len(photo.Media) != 1 || photo.Media[0].Media == nil || photo.Media[0].Media.ID != "photo/10/x" || photo.Media[0].Media.Width != 800 {
		t.Errorf("photo %+v", photo)
	}
	if table := blocks[7]; table.Rows[0].Cells[0].Colspan != 2 || table.Rows[0].Cells[0].Align != "center" || !table.Rows[0].Cells[0].Header {
		t.Errorf("table %+v", table)
	}
	if row := blocks[9]; len(row.Buttons) != richButtons || row.Align != "center" || row.Buttons[0].Button.URL != "https://e.org" {
		t.Errorf("buttons %+v", row)
	}
	if anchor := blocks[10]; anchor.Kind != model.RichAnchor || anchor.Anchor != "sec one" {
		t.Errorf("anchor %+v", anchor)
	}
}

// The article goes into the cache with its message, and changes its
// revision when it changes.
func TestRichMessageSurvivesCache(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	msg := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 4}}
	msg.SetRichMessage(richFixture(true))
	msgs, err := s.convert(ctx, []tg.MessageClass{msg}, false, ^uint64(0))
	if err != nil || len(msgs) != 1 {
		t.Fatalf("convert: %v, %v", msgs, err)
	}
	if _, ok := s.history.refs["photo/10/x"]; !ok {
		t.Fatal("no location for the article's photo")
	}
	if _, ok := s.history.refs["photo/10/m"]; !ok {
		t.Fatal("no location for the photo's smaller size")
	}
	if err := s.Cache().SaveMessages(ctx, msgs); err != nil {
		t.Fatal(err)
	}
	got, err := s.Cache().Around(ctx, 4, 0, 10)
	if err != nil || len(got) != 1 || got[0].Rich == nil {
		t.Fatalf("cache: %+v, %v", got, err)
	}
	want, _ := json.Marshal(msgs[0].Rich)
	have, _ := json.Marshal(got[0].Rich)
	if string(want) != string(have) {
		t.Fatalf("cache changed the page:\n%s\n%s", want, have)
	}
	before := model.Revision(msgs[0])
	msgs[0].Rich.Blocks[2].Language = "rust"
	if model.Revision(msgs[0]) == before {
		t.Fatal("an edit of the page keeps the revision")
	}
}

// What is nested deeper than Telegram Desktop reads is left out, and a page
// of too many blocks is cut: a hostile message cannot run away.
func TestRichMessageBounded(t *testing.T) {
	var block tg.PageBlockClass = &tg.PageBlockParagraph{Text: plain("deep")}
	for range 1000 {
		block = &tg.PageBlockBlockquoteBlocks{Blocks: []tg.PageBlockClass{block}}
	}
	var text tg.RichTextClass = plain("x")
	for range 1000 {
		text = &tg.TextBold{Text: text}
	}
	many := make([]tg.PageBlockClass, 10000)
	for i := range many {
		many[i] = &tg.PageBlockParagraph{Text: text}
	}
	page := convertRich(tg.RichMessage{Blocks: append([]tg.PageBlockClass{block}, many...)})
	depth := 0
	for b := page.Blocks[0]; len(b.Blocks) > 0; b = b.Blocks[0] {
		depth++
	}
	if depth >= richDepth {
		t.Fatalf("blocks nested %d deep", depth)
	}
	if n := len(page.Blocks); n > richBlocks {
		t.Fatalf("%d blocks", n)
	}
	if n := len(page.Blocks[1].Text.Entities); n >= richDepth {
		t.Fatalf("text nested %d deep", n)
	}
}

// The whole of a message sent cut short is asked of Telegram, and kept for
// when it cannot be.
func TestRichMessageLoadsWhole(t *testing.T) {
	s := testStore(t)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	full := richFixture(false)
	var asked *tg.MessagesGetRichMessageRequest
	var fail error
	s.history.api = composerAPI(func(in bin.Encoder) (bin.Encoder, error) {
		r, ok := in.(*tg.MessagesGetRichMessageRequest)
		if !ok {
			return nil, errors.New("unexpected request")
		}
		asked = r
		if fail != nil {
			return nil, fail
		}
		msg := &tg.Message{ID: r.ID, PeerID: &tg.PeerUser{UserID: 5}}
		msg.SetRichMessage(full)
		return &tg.MessagesMessages{Messages: []tg.MessageClass{msg}}, nil
	})
	ctx := context.Background()
	key := model.MessageKey{ChatID: 5, MessageID: 42}
	page, err := s.RichMessage(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if asked.ID != 42 {
		t.Fatalf("asked %+v", asked)
	}
	if user, ok := asked.Peer.(*tg.InputPeerUser); !ok || user.UserID != 5 || user.AccessHash != 55 {
		t.Fatalf("asked of %+v", asked.Peer)
	}
	if page.Part || len(page.Blocks) != len(full.Blocks) {
		t.Fatalf("page of %d blocks, part %v", len(page.Blocks), page.Part)
	}
	fail = errors.New("offline")
	kept, err := s.RichMessage(ctx, key)
	if err != nil || len(kept.Blocks) != len(page.Blocks) {
		t.Fatalf("kept page: %d blocks, %v", len(kept.Blocks), err)
	}
	if _, err := s.RichMessage(ctx, model.MessageKey{ChatID: 5, MessageID: 43}); err == nil {
		t.Fatal("a page never loaded came from nowhere")
	}
}

// The chat list shows a rich message's summary, or what it shows when it
// has no text.
func TestRichMessagePreview(t *testing.T) {
	msg := &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 4}}
	msg.SetRichMessage(tg.RichMessage{Blocks: []tg.PageBlockClass{&tg.PageBlockTitle{Text: plain("Hello")}, &tg.PageBlockParagraph{Text: plain("  world  ")}}})
	if text, _ := preview(msg); text != "Hello world" {
		t.Fatalf("preview %q", text)
	}
	msg.SetRichMessage(tg.RichMessage{Blocks: []tg.PageBlockClass{&tg.PageBlockPhoto{PhotoID: 1}}})
	if text, _ := preview(msg); text != "Фото" {
		t.Fatalf("preview %q", text)
	}
	m, _ := convertMessage("a", msg, nil)
	var chat model.Chat
	setPreview(&chat, m)
	if chat.LastMessage != "Фото" {
		t.Fatalf("last message %q", chat.LastMessage)
	}
	if strings.TrimSpace(m.Text) != "" {
		t.Fatalf("text %q of a page without text", m.Text)
	}
}

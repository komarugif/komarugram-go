// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"os"
	"testing"

	"gio-mw/widget/button"

	"gioui.org/f32"
	"gioui.org/io/pointer"
	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// TestRenderCommentsHead saves the header of the comments page, for looking
// at it: COMMENTS_PNG=/tmp/comments.png.
func TestRenderCommentsHead(t *testing.T) {
	path := os.Getenv("COMMENTS_PNG")
	if path == "" {
		t.Skip("set COMMENTS_PNG to a file")
	}
	head := chatHead{back: button.Text(), title: "9 комментариев", subtitle: "Новости Go"}
	l := localization.For("ru")
	renderFrames(t, image.Pt(500, 200), path, func(gtx layout.Context) {
		layoutChatPageHead(gtx, model.Chat{ID: 1, Title: "x"}, l, nil, nil, &head, nil, func(gtx layout.Context) layout.Dimensions {
			return layout.Dimensions{Size: gtx.Constraints.Max}
		}, nil)
	})
}

// TestCommentsBarOpens clicks the comments bar of a channel post.
func TestCommentsBarOpens(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
		for i := range messages {
			messages[i].Post, messages[i].SenderName = true, ""
		}
		messages[4].CommentsOpen, messages[4].Comments = true, 3
	})
	var opened []model.Message
	h.page.openComments = func(m model.Message) { opened = append(opened, m) }
	h.frames(2)
	if r := h.page.rows[5]; r == nil {
		t.Fatal("post 5 is not shown")
	}
	// The bar is the bottom of the post's bubble.
	at := h.messageAt(5)
	for y := at.Y; y < at.Y+80 && len(opened) == 0; y += 4 {
		h.press(pointer.ButtonPrimary, f32.Pt(100, y))
	}
	if len(opened) != 1 || opened[0].Key.MessageID != 5 {
		t.Fatalf("opened %+v", opened)
	}
	// Posts without a discussion have no bar.
	for y := h.messageAt(4).Y - 30; y < h.messageAt(4).Y+30; y += 4 {
		h.press(pointer.ButtonPrimary, f32.Pt(100, y))
	}
	if len(opened) != 1 {
		t.Fatalf("a post without comments opened them: %+v", opened)
	}
}

// TestThreadRootNotQuoted checks that in a thread a reply to its root
// quotes nothing, while a reply to another message there still does.
func TestThreadRootNotQuoted(t *testing.T) {
	h := newMenuHarness(t, func(_ *menuStore, messages []model.Message) {
		messages[2].ReplyToMessageID = 1
		messages[3].ReplyToMessageID = 2
	})
	h.store.benchmarkHistory.h.ThreadRoot = 1
	h.store.benchmarkHistory.h.Revision++
	h.frames(3)
	p := h.page
	height := func(id model.MessageID) int64 {
		for i, m := range p.messages {
			if m.Key.MessageID == id {
				return p.heights.Prefix(i+1) - p.heights.Prefix(i)
			}
		}
		t.Fatalf("message %d not shown", id)
		return 0
	}
	plain, toRoot, toComment := height(5), height(3), height(4)
	if toRoot != plain {
		t.Errorf("a reply to the root is %d high, a plain message %d: it quotes the root", toRoot, plain)
	}
	if toComment <= plain {
		t.Errorf("a reply to a comment is %d high, a plain message %d: it lost its quote", toComment, plain)
	}
}

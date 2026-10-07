// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"strings"

	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// anchorTarget is the anchor a link goes to, when it goes to one in the
// article it is in: a link to #name.
func anchorTarget(url string) (string, bool) {
	rest, ok := strings.CutPrefix(strings.TrimSpace(url), "#")
	if !ok {
		return "", false
	}
	return model.AnchorName(rest), true
}

// goToAnchor goes to the anchor name of r's article, as Telegram Desktop
// does: it opens the details that hide it, and the history scrolls to it
// in the next frame, when it is laid out. An anchor the message does not
// have is looked for in the whole article when Telegram sent it cut short.
func (p *chatPage) goToAnchor(r *messageRow, name string, l localization.Catalog) {
	if r.article == nil {
		return
	}
	if name != "" && r.articleState.openTo(r.article, name) {
		r.articleState.jump = name
		p.anchorJump, p.anchorWait = r.key.MessageID, 0
		p.invalidate()
		return
	}
	if r.article.part && p.openArticle != nil {
		m, ok := p.messageByID(r.key.MessageID)
		if !ok {
			m = model.Message{Key: r.key}
		}
		p.openArticle(m, name)
		return
	}
	p.toast.Show(l.T("rich.anchor_missing"))
}

// anchorFrames is how many frames an anchor a link goes to is waited for:
// the details opened for it lay it out in the next one.
const anchorFrames = 3

// scrollToAnchor scrolls the history so that the anchor the article of
// message id was asked to go to is at its top.
func (p *chatPage) scrollToAnchor(gtx layout.Context, id model.MessageID) {
	r := p.rows[id]
	top, ok := p.anchorTop(r)
	if !ok {
		return
	}
	// The article is under the bubble's padding and what is over it.
	p.restore(id, r.bodyTop+gtx.Dp(bubblePadTop)+r.articleAbove+top)
	p.list.Position.BeforeEnd = true
	p.invalidate()
}

// anchorTop is where the anchor the article of r was asked to go to is in
// it, once it is laid out; until then, for a few frames, the jump waits.
func (p *chatPage) anchorTop(r *messageRow) (int, bool) {
	if r == nil || r.articleState.jump == "" {
		return 0, false
	}
	top, ok := r.articleState.tops[r.articleState.jump]
	if !ok {
		if p.anchorWait++; p.anchorWait < anchorFrames {
			p.anchorJump = r.key.MessageID
			p.invalidate()
		} else {
			r.articleState.jump = ""
		}
		return 0, false
	}
	r.articleState.jump = ""
	return top, true
}

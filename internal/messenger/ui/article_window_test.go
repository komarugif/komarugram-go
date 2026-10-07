// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// wholeArticleStore gives the whole article once it is told to.
type wholeArticleStore struct {
	benchmarkHistory
	whole model.RichPage
	ready chan struct{}
}

func (s wholeArticleStore) RichMessage(ctx context.Context, _ model.MessageKey) (model.RichPage, error) {
	select {
	case <-s.ready:
		return s.whole, nil
	case <-ctx.Done():
		return model.RichPage{}, ctx.Err()
	}
}

// articleViewHarness lays out an article window's view without the window.
type articleViewHarness struct {
	t      *testing.T
	view   *articleWindow
	store  wholeArticleStore
	router input.Router
	now    time.Time
}

// newArticleViewHarness opens the part of page, whose whole is whole, at
// fragment.
func newArticleViewHarness(t *testing.T, part, whole model.RichPage, fragment string) *articleViewHarness {
	t.Helper()
	h := &articleViewHarness{t: t, store: wholeArticleStore{whole: whole, ready: make(chan struct{})}, now: time.Unix(1_790_000_000, 0)}
	h.view = newArticleView(h.store, localization.For("en"), richMessage(part), fragment, func() {})
	t.Cleanup(h.view.Close)
	h.frame()
	return h
}

func (h *articleViewHarness) frame() {
	gtx := layout.Context{Ops: new(op.Ops), Source: h.router.Source(), Now: h.now, Constraints: layout.Exact(image.Pt(500, 400)), Metric: unit.Metric{PxPerDp: 1, PxPerSp: 1}, Values: map[string]any{}}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	h.view.layout(gtx, false)
	h.router.Frame(gtx.Ops)
	h.now = h.now.Add(16 * time.Millisecond)
}

// load lets the store give the whole article, and lays out the frames that
// show it.
func (h *articleViewHarness) load() {
	close(h.store.ready)
	deadline := time.Now().Add(5 * time.Second)
	for !h.view.whole && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
		h.frame()
	}
	for range 4 {
		h.frame()
	}
}

// A window opened at an anchor the part does not have shows the part and
// waits; once the whole article comes, it opens the details that hide the
// anchor and scrolls to it.
func TestArticleWindowGoesToFragmentOfWhole(t *testing.T) {
	part, whole := anchorPage(true), anchorPage(false)
	part.Blocks = part.Blocks[:10]
	h := newArticleViewHarness(t, part, whole, "deep end")
	for range 4 {
		h.frame()
	}
	if got := h.view.page.toast.Text(); got != "" {
		t.Fatalf("the part tells %q", got)
	}
	if h.view.list.Position.Offset != 0 {
		t.Fatalf("the part scrolled to %d", h.view.list.Position.Offset)
	}
	h.load()
	r := h.view.page.rows[h.view.message.Key.MessageID]
	top, ok := r.articleState.tops["deep end"]
	if !ok {
		t.Fatal("the details over the anchor stay closed")
	}
	if got := h.view.list.Position.Offset; got != articleWindowMargin+top {
		t.Fatalf("the window is at %d, the anchor at %d", got, articleWindowMargin+top)
	}
}

// An anchor the whole article does not have either is told of.
func TestArticleWindowMissingFragment(t *testing.T) {
	h := newArticleViewHarness(t, anchorPage(true), anchorPage(false), "nowhere")
	h.load()
	if got := h.view.page.toast.Text(); got != localization.For("en").T("rich.anchor_missing") {
		t.Fatalf("toast %q", got)
	}
}

// The window steps back to where it was before an anchor it went to, and
// ahead again, by Alt with an arrow and by its buttons, shown while there
// is somewhere to step to.
func TestArticleWindowStepsBackAndAhead(t *testing.T) {
	page := anchorPage(false)
	h := newArticleViewHarness(t, page, page, "")
	v := h.view
	r := v.page.rows[v.message.Key.MessageID]
	v.page.goToAnchor(r, "deep end", localization.For("en"))
	for range 4 {
		h.frame()
	}
	there := v.list.Position.Offset
	if there == 0 || len(v.back) != 1 {
		t.Fatalf("the anchor is at %d, back %v", there, v.back)
	}
	h.router.Queue(key.Event{Name: key.NameLeftArrow, Modifiers: key.ModAlt, State: key.Press})
	h.frame()
	if got := v.list.Position.Offset; got != 0 || len(v.ahead) != 1 {
		t.Fatalf("back, the window is at %d, ahead %v", got, v.ahead)
	}
	// The button ahead is the second in the bar over the article.
	for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
		h.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Position: f32.Pt(4+48+24, 24), Buttons: pointer.ButtonPrimary})
		h.frame()
	}
	if got := v.list.Position.Offset; got != there || len(v.ahead) != 0 || len(v.back) != 1 {
		t.Fatalf("ahead, the window is at %d, not %d", got, there)
	}
}

// Ctrl with = and 0 zoom the window's article in by 10 % and back, kept
// where the window keeps it.
func TestArticleWindowZoom(t *testing.T) {
	page := anchorPage(false)
	h := newArticleViewHarness(t, page, page, "")
	zoom := 0
	h.view.tools.zoom = func() int { return zoom }
	h.view.tools.setZoom = func(z int) { zoom = z }
	h.frame()
	r := h.view.page.rows[h.view.message.Key.MessageID]
	before := r.text.size
	h.router.Queue(key.Event{Name: "=", Modifiers: key.ModShortcut, State: key.Press})
	h.frame()
	h.frame()
	// The window's width is the same: the article's text is bigger, and
	// longer.
	if zoom != 110 || r.text.size.Y <= before.Y*21/20 {
		t.Fatalf("zoomed to %d, the article is %v, was %v", zoom, r.text.size, before)
	}
	h.router.Queue(key.Event{Name: "0", Modifiers: key.ModShortcut, State: key.Press})
	h.frame()
	if zoom != 100 {
		t.Fatalf("Ctrl+0 zoomed to %d", zoom)
	}
}

// Ctrl+F searches the article: the matches of any case are counted, the
// first selected, and Enter goes to the next.
func TestArticleWindowSearch(t *testing.T) {
	page := anchorPage(false)
	h := newArticleViewHarness(t, page, page, "")
	h.router.Queue(key.Event{Name: "F", Modifiers: key.ModShortcut, State: key.Press})
	h.frame()
	s := &h.view.tools.search
	if !s.open {
		t.Fatal("Ctrl+F does not open the search")
	}
	s.field.SetText("абзац 1")
	h.frame()
	// "Абзац 1" and "Абзац 10" to "Абзац 19", and "Ещё абзац 1" and its tens.
	if len(s.matches) != 22 || s.current != 0 {
		t.Fatalf("%d matches, at %d", len(s.matches), s.current)
	}
	r := h.view.page.rows[h.view.message.Key.MessageID]
	if got := r.selectedText(); got != "Абзац 1" {
		t.Fatalf("selected %q", got)
	}
	h.router.Queue(key.Event{Name: key.NameReturn, State: key.Press})
	h.frame()
	if s.current != 1 || h.view.list.Position.Offset == 0 {
		t.Fatalf("Enter went to %d, the window at %d", s.current, h.view.list.Position.Offset)
	}
}

// Share forwards the message, from the window's own dialog.
func TestArticleWindowShare(t *testing.T) {
	page := anchorPage(false)
	h := newArticleViewHarness(t, page, page, "")
	h.view.page.source = forwardingStore{h.store}
	h.view.page.chats = func() []model.Chat { return []model.Chat{{ID: 5, Title: "Друг"}} }
	h.frame()
	// Share is second from the right of the bar, after the search.
	for _, kind := range []pointer.Kind{pointer.Press, pointer.Release} {
		h.router.Queue(pointer.Event{Kind: kind, Source: pointer.Mouse, Position: f32.Pt(500-4-44-22, 24), Buttons: pointer.ButtonPrimary})
		h.frame()
	}
	h.frame()
	if !h.view.page.forwarding.modal.Shown() || len(h.view.page.forwarding.ids) != 1 {
		t.Fatal("Share opens no forward dialog")
	}
}

// forwardingStore can forward messages, and forwards none.
type forwardingStore struct{ wholeArticleStore }

func (forwardingStore) ForwardMessages(context.Context, int64, []model.MessageID, int64) error {
	return nil
}

package ui

import (
	"encoding/binary"
	"image"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"unicode/utf16"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"
)

// entityOn marks the first sub of text as kind, as Telegram counts it: in
// UTF-16 units.
func entityOn(text, kind, sub string) model.Entity {
	i := strings.Index(text, sub)
	if i < 0 {
		panic(sub)
	}
	return model.Entity{Kind: kind, Offset: len(utf16.Encode([]rune(text[:i]))), Length: len(utf16.Encode([]rune(sub)))}
}

// profileTexts are messages whose texts wrap every way the profile has to
// tell: words, paragraphs and spaces, styles that change in a word, code,
// quotes, folded ones, a link without spaces, CJK and emoji.
func profileTexts() []model.Message {
	long := "Telegram keeps every message of the chat in its history, and the history scrolls through all of them, fast and slow. Сообщения переносятся по словам, а длинные слова — по графемам. "
	var msgs []model.Message
	add := func(text string, entities ...model.Entity) {
		// Past the harness's own messages, whose rows took their profiles.
		id := model.MessageID(1001 + len(msgs))
		msgs = append(msgs, model.Message{Key: model.MessageKey{ChatID: 1, MessageID: id}, Text: text, Entities: entities, SenderName: "Ann", ContentRevision: uint64(id)})
	}
	add("Short")
	add(strings.Repeat(long, 3))
	add("First paragraph " + long + "\n\nSecond, after a blank line.   \nThird with spaces at the end    \n" + long)
	words := strings.Repeat("unbelievable ", 30)
	add(words, entityOn(words, "bold", "believ"), entityOn(words, "italic", "able unbe"))
	mixed := "Inline code like fmt.Println(x) and a link https://example.com/a/very/long/path?with=query&and=more " + long
	add(mixed, entityOn(mixed, "code", "fmt.Println(x)"), entityOn(mixed, "url", "https://example.com/a/very/long/path?with=query&and=more"), entityOn(mixed, "bold", "Inline code"))
	code := "Before the code.\nfunc main() {\n\tfmt.Println(\"a line of code long enough to wrap in a narrow bubble, and then some more\")\n}\nAfter the code, " + long
	add(code, entityOn(code, "pre", "func main() {\n\tfmt.Println(\"a line of code long enough to wrap in a narrow bubble, and then some more\")\n}\n"))
	quote := "Quoted: " + long + long + "\nAnd the answer, " + long
	add(quote, entityOn(quote, "quote", long+long))
	folded := "A folded quote:\n" + strings.Repeat(long, 4)
	add(folded, model.Entity{Kind: "quote", Offset: len(utf16.Encode([]rune("A folded quote:\n"))), Length: len(utf16.Encode([]rune(strings.Repeat(long, 4)))), Collapsed: true})
	add("https://example.com/" + strings.Repeat("averyveryverylongpathwithoutanyspaces", 6))
	add(strings.Repeat("历史记录滚动浏览所有消息，快慢皆可。", 12))
	add(strings.Repeat("Emoji 😀🎉👍🏽 in a line, ", 15))
	add("a" + strings.Repeat(" ", 40) + "b" + strings.Repeat("  word", 40))
	return msgs
}

// A profile taken at one width tells how high the text is at any other,
// and how far its last line reaches, which the time goes after.
func TestProfileTellsTheTextAtEveryWidth(t *testing.T) {
	h := newMenuHarness(t, nil)
	metric := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	theme := defaults.NewTheme(layout.Context{Ops: new(op.Ops), Metric: metric, Values: map[string]any{}}, schemes.SchemeBaselineLight())
	l := localization.For("en")
	lay := func(m model.Message, width int) *messageRow {
		gtx := layout.Context{Ops: new(op.Ops), Now: h.now, Constraints: layout.Constraints{Max: image.Pt(width, 1<<20)}, Metric: metric, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, theme)
		h.page.row(gtx, m, false, 0, l, false)
		return h.page.rows[m.Key.MessageID]
	}
	for _, m := range profileTexts() {
		r := lay(m, 600)
		if r.capture.text == nil {
			t.Fatalf("message %d: no profile of its text", m.Key.MessageID)
		}
		// Through the cache's bytes, as the history reads it.
		taken := rowProfile{text: r.capture.text}
		prof, ok, err := decodeRowProfile(taken.encode())
		if !ok || err != nil {
			t.Fatalf("message %d: the profile reads back as %v, %v", m.Key.MessageID, ok, err)
		}
		for width := 150; width <= 700; width += 23 {
			r := lay(m, width)
			got, last, _ := prof.text.height(r.capture.textMax)
			if want := r.text.size.Y; got != want {
				t.Errorf("message %d at %d px (text %d px): the profile tells %d px, laid out %d px", m.Key.MessageID, width, r.capture.textMax, got, want)
				continue
			}
			// A word longer than a line is drawn to its end: the profile tells
			// it so.
			if right, _, ok := r.lastLine(); ok && min(right, r.capture.textMax) != last {
				t.Errorf("message %d at %d px: the last line reaches %d px, the profile tells %d", m.Key.MessageID, width, right, last)
			}
		}
	}
}

// profileStore keeps what the page saves of its rows, and tells it which
// messages it has profiles of.
type profileStore struct {
	*menuStore
	mu       sync.Mutex
	saved    map[model.MessageID]model.MessageLayout
	profiled map[model.MessageID]uint64
}

func (s *profileStore) SaveView(v model.Viewport, ls []model.MessageLayout) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, l := range ls {
		if len(l.Profile) > 0 {
			s.saved[l.Key.MessageID] = l
		}
	}
}

func (s *profileStore) Profiled(int64, model.RenderEnvironment, byte) map[model.MessageID]uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return maps.Clone(s.profiled)
}

// A row drawn hands its profile to the store with its height, once: the
// time, the reactions and the text as they were laid out. A row whose
// message the store has a profile of takes none.
func TestRowsHandTheirProfilesOver(t *testing.T) {
	var store *profileStore
	h := newMenuHarnessOn(t, func(_ *menuStore, messages []model.Message) {
		messages[18].Reactions = []model.Reaction{{Emoji: "👍", Count: 3}, {Emoji: "🔥", Count: 12}}
		// A short last line, which the time goes after.
		messages[17].Text = strings.Repeat("A message that wraps over a few lines of the bubble. ", 4) + "\nOk"
	}, func(s *menuStore) model.ConversationStore {
		store = &profileStore{menuStore: s, saved: map[model.MessageID]model.MessageLayout{}, profiled: map[model.MessageID]uint64{20: 1}}
		return store
	})
	h.page.list.Position.BeforeEnd = false
	h.frames(3)
	h.page.save(true)
	store.mu.Lock()
	saved := maps.Clone(store.saved)
	store.mu.Unlock()
	if _, ok := saved[20]; ok {
		t.Error("message 20, whose profile the store has, took one again")
	}
	for _, id := range []model.MessageID{18, 19} {
		l, ok := saved[id]
		if !ok {
			t.Fatalf("message %d handed no profile over: %v", id, slices.Collect(maps.Keys(saved)))
		}
		prof, ok, err := decodeRowProfile(l.Profile)
		if !ok || err != nil {
			t.Fatalf("message %d: the profile reads as %v, %v", id, ok, err)
		}
		if prof.width != l.Environment.WidthPx || prof.height != l.HeightPx || prof.footer.X == 0 || prof.text == nil {
			t.Fatalf("message %d: profile %+v for a row %d px high at %d", id, prof, l.HeightPx, l.Environment.WidthPx)
		}
		if id == 19 && (len(prof.chips) != 2 || prof.chipRow == 0) {
			t.Fatalf("the reactions' chips are %v, %d px high", prof.chips, prof.chipRow)
		}
		if id == 18 && !prof.onText {
			t.Error("the time of a text was not on its last line")
		}
	}
	// Taken once: the rows drawn again hand nothing more over.
	store.mu.Lock()
	clear(store.saved)
	store.mu.Unlock()
	h.frames(3)
	h.page.save(true)
	store.mu.Lock()
	again := len(store.saved)
	store.mu.Unlock()
	if again != 0 {
		t.Fatalf("drawn again, %d rows handed their profiles over again", again)
	}
	// A row whose height changed of itself, as media whose state came,
	// takes its profile anew.
	m := h.page.measures[18]
	m.HeightPx++
	h.page.measures[18] = m
	h.frames(3)
	h.page.save(true)
	store.mu.Lock()
	_, retaken := store.saved[18]
	store.mu.Unlock()
	if !retaken {
		t.Fatal("a row that changed of itself took no profile again")
	}
}

// A profile reads back from its bytes as it was, and one of another
// version reads as none.
func TestRowProfileBytes(t *testing.T) {
	h := newMenuHarness(t, nil)
	metric := unit.Metric{PxPerDp: 1, PxPerSp: 1}
	theme := defaults.NewTheme(layout.Context{Ops: new(op.Ops), Metric: metric, Values: map[string]any{}}, schemes.SchemeBaselineLight())
	for _, m := range profileTexts() {
		gtx := layout.Context{Ops: new(op.Ops), Now: h.now, Constraints: layout.Constraints{Max: image.Pt(600, 1<<20)}, Metric: metric, Values: map[string]any{}}
		wdk.InitMaterialThemeInContext(gtx, theme)
		h.page.row(gtx, m, false, 0, localization.For("en"), false)
		c := h.page.rows[m.Key.MessageID].capture
		taken := rowProfile{width: 600, height: 321, bubbleWidth: 430, text: c.text, footer: image.Pt(44, 16), onText: true, chips: []int{40, 52}, chipRow: 28,
			bubble: bubbleProfile{parts: []partProfile{{kind: partWidget, size: image.Pt(80, 20)}, {kind: partSpace, size: image.Pt(0, 3)}, {kind: partMedia, edge: true, fit: true, media: model.MessagePhoto, mw: 1280, mh: 720}, {kind: partText},
				{kind: partMedia, edge: true, album: []image.Point{{1280, 720}, {720, 1280}}}, {kind: partText, text: c.text}},
				plate: image.Pt(50, 18), textLast: true, reply: replyProfile{named: 70, line: 300, lineHeight: 18, pad: 21, least: 120, lines: 1, size: image.Pt(321, 44)}, slot: 42, avatar: 34, bar: 44, barNatural: 200, rest: 8}}
		data := taken.encode()
		back, ok, err := decodeRowProfile(data)
		if !ok || err != nil {
			t.Fatalf("message %d reads back as %v, %v", m.Key.MessageID, ok, err)
		}
		if !reflect.DeepEqual(back, taken) {
			t.Fatalf("message %d reads back as another profile", m.Key.MessageID)
		}
		for n := range data {
			if _, ok, err := decodeRowProfile(data[:n]); ok && err == nil {
				t.Fatalf("message %d: %d of %d bytes read as a profile", m.Key.MessageID, n, len(data))
			}
		}
	}
	other := binary.AppendUvarint(nil, rowProfileVersion+1)
	if _, ok, _ := decodeRowProfile(other); ok {
		t.Fatal("a profile of another version read as one")
	}
}

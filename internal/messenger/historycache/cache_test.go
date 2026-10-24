package historycache

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"komarugram/internal/messenger/model"
	"komarugram/internal/messenger/security"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/telegram/updates"
)

func TestPersistencePagingDeletionAndLayouts(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "history")
	c, e := Open(path, "a", nil)
	if e != nil {
		t.Fatal(e)
	}
	var messages []model.Message
	for i := 1; i <= 300; i++ {
		messages = append(messages, model.Message{Key: model.MessageKey{AccountID: "a", ChatID: 7, MessageID: model.MessageID(i)}, Text: "private text", ContentRevision: 2})
	}
	if e = c.SaveMessages(ctx, messages); e != nil {
		t.Fatal(e)
	}
	if e = c.Delete(ctx, 7, []int{150}); e != nil {
		t.Fatal(e)
	}
	if e = c.SaveMessages(ctx, messages[149:150]); e != nil {
		t.Fatal(e)
	}
	around, e := c.Around(ctx, 7, 150, 20)
	if e != nil || len(around) != 20 {
		t.Fatalf("around=%d, %v", len(around), e)
	}
	for _, m := range around {
		if m.Key.MessageID == 150 {
			t.Fatal("resurrected deletion")
		}
	}
	env := model.RenderEnvironment{WidthPx: 500, ScaleMilli: 1000, Locale: "ru", RendererRevision: 1}
	view := model.Viewport{AccountID: "a", ChatID: 7, AnchorMessageID: 149, AnchorOffsetPx: 31, Environment: env}
	ls := []model.MessageLayout{{Key: messages[148].Key, Environment: env, ContentRevision: 2, HeightPx: 79}}
	if e = c.SaveView(ctx, view, ls); e != nil {
		t.Fatal(e)
	}
	if e = c.Close(); e != nil {
		t.Fatal(e)
	}
	c, e = Open(path, "a", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	var restored model.Viewport
	if ok, e := c.Get(ctx, "viewport/7", &restored); e != nil || !ok || restored.AnchorMessageID != 149 || restored.AnchorOffsetPx != 31 {
		t.Fatalf("restore %+v %v", restored, e)
	}
	layouts, e := c.Layouts(ctx, 7, env)
	if e != nil || len(layouts) != 1 || layouts[0].HeightPx != 79 {
		t.Fatalf("layouts %+v %v", layouts, e)
	}
	env.WidthPx++
	layouts, e = c.Layouts(ctx, 7, env)
	if e != nil || len(layouts) != 0 {
		t.Fatal("stale environment measurement")
	}
	before, e := c.Page(ctx, 7, 100, -1, 10)
	if e != nil || len(before) != 10 || before[0].Key.MessageID != 90 || before[9].Key.MessageID != 99 {
		t.Fatalf("before: %+v %v", before, e)
	}
	after, e := c.Page(ctx, 7, 100, 1, 10)
	if e != nil || len(after) != 10 || after[0].Key.MessageID != 101 {
		t.Fatal("bad forward cache page", e)
	}
}
func TestAccountAndChannelIsolation(t *testing.T) {
	ctx := context.Background()
	c, e := Open(filepath.Join(t.TempDir(), "h"), "a", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	msg := model.Message{Key: model.MessageKey{AccountID: "b", ChatID: 1, MessageID: 1}}
	if c.SaveMessages(ctx, []model.Message{msg}) == nil {
		t.Fatal("wrong account accepted")
	}
	msg.Key.AccountID = "a"
	msg.Key.MessageID = 55
	if e = c.Delete(ctx, 0, []int{55}); e != nil {
		t.Fatal(e)
	}
	if e = c.SaveMessages(ctx, []model.Message{msg}); e != nil {
		t.Fatal(e)
	}
	got, e := c.Around(ctx, 1, 0, 20)
	if e != nil || len(got) != 0 {
		t.Fatal("late non-channel response resurrected deletion")
	}
	msg.Key.ChatID = -1000000000042
	if e = c.SaveMessages(ctx, []model.Message{msg}); e != nil {
		t.Fatal(e)
	}
	got, e = c.Around(ctx, msg.Key.ChatID, 0, 20)
	if e != nil || len(got) != 1 {
		t.Fatal("channel ID collision")
	}
}

type fakeTPM struct{ key []byte }

func (f *fakeTPM) Probe() error { return nil }
func (f *fakeTPM) Seal(key, auth []byte) ([]byte, []byte, error) {
	f.key = append([]byte(nil), key...)
	return append([]byte(nil), auth...), []byte("device"), nil
}
func (f *fakeTPM) Unseal(pub, priv, auth []byte) ([]byte, error) {
	if !bytes.Equal(pub, auth) {
		return nil, errors.New("denied")
	}
	return append([]byte(nil), f.key...), nil
}
func TestProtectionTransitionKeepsSyncAndAnchor(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	p, e := security.OpenPath(filepath.Join(dir, "security"), &fakeTPM{})
	if e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "history")
	c, e := Open(path, "a", p)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	if e = c.SetState(ctx, 1, updates.State{Pts: 22, Seq: 4}); e != nil {
		t.Fatal(e)
	}
	if e = c.Put(ctx, "viewport/7", model.Viewport{AnchorMessageID: 81}); e != nil {
		t.Fatal(e)
	}
	msg := model.Message{Key: model.MessageKey{AccountID: "a", ChatID: 7, MessageID: 81}, Text: "keep this message"}
	if e = c.SaveMessages(ctx, []model.Message{msg}); e != nil {
		t.Fatal(e)
	}
	if e = c.SaveMedia(ctx, "picture", []byte("media bytes")); e != nil {
		t.Fatal(e)
	}
	if e = p.Enable(ctx, "test"); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(path + ".plain"); !errors.Is(e, os.ErrNotExist) {
		t.Fatal("plaintext cache left behind")
	}
	if e = c.SetPts(ctx, 1, 23); e != nil {
		t.Fatal("lost update cursor", e)
	}
	state, ok, e := c.GetState(ctx, 1)
	if e != nil || !ok || state.Pts != 23 || state.Seq != 4 {
		t.Fatal(state, ok, e)
	}
	var v model.Viewport
	if ok, e = c.Get(ctx, "viewport/7", &v); e != nil || !ok || v.AnchorMessageID != 81 {
		t.Fatal("anchor lost", e)
	}
	checkContent := func() {
		messages, err := c.Around(ctx, 7, 81, 5)
		if err != nil || len(messages) != 1 || messages[0].Text != msg.Text {
			t.Fatalf("message lost: %+v, %v", messages, err)
		}
		media, err := c.Media(ctx, "picture")
		if err != nil || string(media) != "media bytes" {
			t.Fatalf("media lost: %q, %v", media, err)
		}
	}
	checkContent()
	raw, e := os.ReadFile(path + ".secure")
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(raw, []byte("SQLite format")) || bytes.Contains(raw, []byte("viewport")) {
		t.Fatal("unencrypted database")
	}
	p.SetUnmigration(func() error { return nil })
	if e = p.Disable(ctx, "test"); e != nil {
		t.Fatal(e)
	}
	checkContent()
	if e = p.Enable(ctx, "again"); e != nil {
		t.Fatal(e)
	}
	checkContent()
}
func TestReconcileOnlyAuthoritativeInterval(t *testing.T) {
	c, e := Open(filepath.Join(t.TempDir(), "h"), "a", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	ctx := context.Background()
	for _, id := range []int{5, 10, 15, 20, 25} {
		if e = c.SaveMessages(ctx, []model.Message{{Key: model.MessageKey{AccountID: "a", ChatID: 1, MessageID: model.MessageID(id)}}}); e != nil {
			t.Fatal(e)
		}
	}
	ids, _, e := c.Reconcile(ctx, 1, 10, 20, []int{10, 20})
	if e != nil || len(ids) != 1 || ids[0] != 15 {
		t.Fatal(ids, e)
	}
	got, e := c.Around(ctx, 1, 0, 10)
	if e != nil || len(got) != 4 {
		t.Fatal("removed outside known server interval", e)
	}
}

func TestPhotosPageAroundAnchorWithIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h")
	c, e := Open(path, "a", nil)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	var msgs []model.Message
	for i := 1; i <= 40; i++ {
		m := model.Message{Key: model.MessageKey{AccountID: "a", ChatID: 9, MessageID: model.MessageID(i)}, Text: "text"}
		if i%4 == 0 {
			m.Kind, m.Media = model.MessagePhoto, &model.MessageMedia{ID: fmt.Sprint("p", i)}
		}
		if i == 6 {
			m.Kind, m.Media = model.MessageVideo, &model.MessageMedia{ID: "v"}
		}
		if i == 10 {
			m.Kind, m.Media = model.MessageGIF, &model.MessageMedia{ID: "g"}
		}
		msgs = append(msgs, m)
	}
	msgs = append(msgs, model.Message{Key: model.MessageKey{AccountID: "a", ChatID: 10, MessageID: 8}, Kind: model.MessagePhoto})
	if e = c.SaveMessages(ctx, msgs); e != nil {
		t.Fatal(e)
	}
	if e = c.Delete(ctx, 9, []int{16}); e != nil {
		t.Fatal(e)
	}
	// A cache created before the index gets it, over its rows, when opened.
	if _, e = c.db.Exec(`DROP INDEX photo_videos`); e != nil {
		t.Fatal(e)
	}
	c.Close()
	if c, e = Open(path, "a", nil); e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	ids := func(ms []model.Message) (out []int) {
		for _, m := range ms {
			out = append(out, int(m.Key.MessageID))
		}
		return
	}
	if all, _ := c.Photos(ctx, 9, 0, 1, 100); fmt.Sprint(ids(all)) != "[4 6 8 12 20 24 28 32 36 40]" {
		t.Fatalf("photos and videos: %v", ids(all))
	}
	older, e := c.Photos(ctx, 9, 24, -1, 3)
	// 16 is deleted, 10 is a GIF and chat 10 is another chat.
	if e != nil || fmt.Sprint(ids(older)) != "[8 12 20]" {
		t.Fatalf("older: %v %v", ids(older), e)
	}
	newer, e := c.Photos(ctx, 9, 24, 1, 10)
	if e != nil || fmt.Sprint(ids(newer)) != "[28 32 36 40]" {
		t.Fatalf("newer: %v %v", ids(newer), e)
	}
	var plan string
	rows, e := c.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM messages WHERE chat=? AND `+photoWhere+` AND deleted=0 AND id>? ORDER BY id LIMIT 5`, 9, 0)
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		rows.Scan(&id, &parent, &unused, &detail)
		plan += detail + "\n"
	}
	rows.Close()
	if !strings.Contains(plan, "photo_videos") {
		t.Fatalf("gallery query does not use the photo index:\n%s", plan)
	}
}

// The GIFs of a chat are a gallery of their own, which the photos' leaves
// out, with an index of its own built over the rows of a cache opened.
func TestGIFsPageAroundAnchorWithIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "h")
	c, e := Open(path, "a", nil)
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	var msgs []model.Message
	for i := 1; i <= 20; i++ {
		m := model.Message{Key: model.MessageKey{AccountID: "a", ChatID: 9, MessageID: model.MessageID(i)}, Text: "text"}
		switch {
		case i%3 == 0:
			m.Kind, m.Media = model.MessageGIF, &model.MessageMedia{ID: fmt.Sprint("g", i)}
		case i%4 == 0:
			m.Kind, m.Media = model.MessagePhoto, &model.MessageMedia{ID: fmt.Sprint("p", i)}
		}
		msgs = append(msgs, m)
	}
	msgs = append(msgs, model.Message{Key: model.MessageKey{AccountID: "a", ChatID: 10, MessageID: 6}, Kind: model.MessageGIF})
	if e = c.SaveMessages(ctx, msgs); e != nil {
		t.Fatal(e)
	}
	if e = c.Delete(ctx, 9, []int{9}); e != nil {
		t.Fatal(e)
	}
	if _, e = c.db.Exec(`DROP INDEX gifs`); e != nil {
		t.Fatal(e)
	}
	c.Close()
	if c, e = Open(path, "a", nil); e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	ids := func(ms []model.Message) (out []int) {
		for _, m := range ms {
			out = append(out, int(m.Key.MessageID))
		}
		return
	}
	// 9 is deleted, chat 10 is another chat, and 4, 8, 16 and 20 are photos.
	if all, _ := c.GIFs(ctx, 9, 0, 1, 100); fmt.Sprint(ids(all)) != "[3 6 12 15 18]" {
		t.Fatalf("GIFs: %v", ids(all))
	}
	if older, e := c.GIFs(ctx, 9, 15, -1, 2); e != nil || fmt.Sprint(ids(older)) != "[6 12]" {
		t.Fatalf("older: %v %v", ids(older), e)
	}
	if photos, _ := c.Photos(ctx, 9, 0, 1, 100); fmt.Sprint(ids(photos)) != "[4 8 16 20]" {
		t.Fatalf("photos: %v", ids(photos))
	}
	var plan string
	rows, e := c.db.Query(`EXPLAIN QUERY PLAN SELECT id FROM messages WHERE chat=? AND `+gifWhere+` AND deleted=0 AND id>? ORDER BY id LIMIT 5`, 9, 0)
	if e != nil {
		t.Fatal(e)
	}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		rows.Scan(&id, &parent, &unused, &detail)
		plan += detail + "\n"
	}
	rows.Close()
	if !strings.Contains(plan, "gifs") {
		t.Fatalf("GIF query does not use the GIF index:\n%s", plan)
	}
}

// Media read are used: the eviction goes by use, and an avatar read every
// day stays though it was written long before what came since. Marked used
// only when written, it went first, to be downloaded again.
func TestMediaReadIsKept(t *testing.T) {
	ctx := context.Background()
	c, e := Open(filepath.Join(t.TempDir(), "history"), "a", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.mediaBudget = 1 << 20
	blob := make([]byte, smallMedia-1)
	for _, key := range []string{"avatar", "m/0", "m/1", "m/2", "m/3", "m/4", "m/5", "m/6", "m/7", "m/8", "m/9"} {
		if e := c.SaveMedia(ctx, key, blob); e != nil {
			t.Fatal(e)
		}
	}
	// Written days ago, the avatar first.
	c.mu.Lock()
	_, e = c.db.Exec(`UPDATE media SET used=?`, time.Now().Add(-24*time.Hour).UnixNano())
	if e == nil {
		_, e = c.db.Exec(`UPDATE media SET used=? WHERE key=?`, time.Now().Add(-48*time.Hour).UnixNano(), "avatar")
	}
	c.mu.Unlock()
	if e != nil {
		t.Fatal(e)
	}
	if b, e := c.Media(ctx, "avatar"); e != nil || len(b) == 0 {
		t.Fatalf("the avatar is not there (%v)", e)
	}
	// What comes now evicts what was used longest ago.
	for i := 10; i < 20; i++ {
		if e := c.SaveMedia(ctx, fmt.Sprintf("m/%d", i), blob); e != nil {
			t.Fatal(e)
		}
	}
	if b, e := c.Media(ctx, "avatar"); e != nil || len(b) == 0 {
		t.Fatalf("the avatar read is gone (%v)", e)
	}
	if b, _ := c.Media(ctx, "m/0"); len(b) != 0 {
		t.Fatal("nothing was evicted: the budget did not fill")
	}
}

// Media stay within the budget, the newest kept; an eviction frees an eighth
// of it, so that the writes after it do not each evict again. The size kept
// follows replaced entries.
func TestMediaBudget(t *testing.T) {
	ctx := context.Background()
	c, e := Open(filepath.Join(t.TempDir(), "history"), "a", nil)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.mediaBudget = 1 << 20
	size := func() int64 {
		var n int64
		c.mu.Lock()
		defer c.mu.Unlock()
		if e := c.db.QueryRow(`SELECT coalesce(sum(length(data)),0) FROM media`).Scan(&n); e != nil {
			t.Fatal(e)
		}
		return n
	}
	// Small media: the larger are files.
	blob := make([]byte, smallMedia-1)
	evicted := false
	for i := range 30 {
		before := size()
		if e := c.SaveMedia(ctx, fmt.Sprintf("m/%d", i), blob); e != nil {
			t.Fatal(e)
		}
		after := size()
		if after > c.mediaBudget {
			t.Fatalf("save %d: %d bytes over a budget of %d", i, after, c.mediaBudget)
		}
		if after < before+int64(len(blob)) && !evicted {
			evicted = true
			if after > c.mediaBudget-c.mediaBudget/8 {
				t.Fatalf("an eviction left %d bytes of %d: no room for the next writes", after, c.mediaBudget)
			}
		}
		c.mu.Lock()
		kept := c.mediaBytes
		c.mu.Unlock()
		if kept != after {
			t.Fatalf("save %d: kept size %d, table %d", i, kept, after)
		}
	}
	if !evicted {
		t.Fatal("nothing evicted")
	}
	if b, _ := c.Media(ctx, "m/29"); len(b) != len(blob) {
		t.Fatal("the newest evicted")
	}
	if b, _ := c.Media(ctx, "m/0"); b != nil {
		t.Fatal("the oldest kept")
	}
	if e := c.SaveMedia(ctx, "m/29", make([]byte, 10)); e != nil {
		t.Fatal(e)
	}
	c.mu.Lock()
	kept := c.mediaBytes
	c.mu.Unlock()
	if kept != size() {
		t.Fatalf("after a replacement: kept size %d, table %d", kept, size())
	}
}

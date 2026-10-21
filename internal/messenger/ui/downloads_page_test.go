// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gioui.org/f32"
	"gioui.org/io/input"
	"gioui.org/io/pointer"
	"gioui.org/io/semantic"
	"gioui.org/layout"
	"gioui.org/op"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

// fakeDownloads is a store's downloads, as a test gives them.
type fakeDownloads struct {
	mu      sync.Mutex
	list    []model.DownloadedFile
	deleted []int64
}

func (f *fakeDownloads) Download(context.Context, model.Message, string, func(int64, int64)) (string, error) {
	return "", nil
}

func (f *fakeDownloads) Downloads(context.Context) ([]model.DownloadedFile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]model.DownloadedFile(nil), f.list...), nil
}

func (f *fakeDownloads) DeleteDownloads(_ context.Context, ids []int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleted = append(f.deleted, ids...)
	return nil
}

func sampleDownloads() *fakeDownloads {
	now := time.Now()
	return &fakeDownloads{list: []model.DownloadedFile{
		{ID: 1, Key: model.MessageKey{ChatID: 1, MessageID: 10}, Name: "Отчёт за октябрь.pdf", Path: "/home/u/Downloads/komarugram/Отчёт за октябрь.pdf", Size: 2_400_000, Added: now.Add(-10 * time.Minute)},
		{ID: 2, Key: model.MessageKey{ChatID: 1, MessageID: 11}, Name: "video_2026-10-20_19-14-15.mp4", Path: "/home/u/Downloads/komarugram/video_2026-10-20_19-14-15.mp4", Size: 148_000_000, Added: now.Add(-26 * time.Hour)},
		{ID: 3, Key: model.MessageKey{ChatID: 2, MessageID: 5}, Name: "photo_2026-10-01_08-00-00.jpg", Path: "/home/u/Downloads/komarugram/photo.jpg", Size: 310_000, Added: now.Add(-20 * 24 * time.Hour), State: model.DownloadChanged},
		{ID: 4, Key: model.MessageKey{ChatID: 2, MessageID: 6}, Name: "build.zip", Path: "/home/u/Downloads/komarugram/build.zip", Size: 51_000_000, Added: now.Add(-30 * 24 * time.Hour), State: model.DownloadMissing},
	}}
}

// loadedDownloads is a page with its list read.
func loadedDownloads(t *testing.T, source model.Downloader) *downloadsPage {
	t.Helper()
	p := newDownloadsPage(source, func() {})
	p.open()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, loaded, _ := p.shown()
		if loaded {
			return p
		}
		if time.Now().After(deadline) {
			t.Fatal("the downloads did not load")
		}
		time.Sleep(time.Millisecond)
	}
}

// Sorted by size, the largest file comes first; by date, the newest.
func TestDownloadsSorting(t *testing.T) {
	p := loadedDownloads(t, sampleDownloads())
	list, _, _ := p.shown()
	if list[0].ID != 1 || list[3].ID != 4 {
		t.Fatal("by date:", list[0].ID, list[3].ID)
	}
	p.bySize = true
	list, _, _ = p.shown()
	if list[0].ID != 2 || list[1].ID != 4 || list[3].ID != 3 {
		t.Fatal("by size:", list[0].ID, list[1].ID, list[3].ID)
	}
}

// TestRenderDownloads saves the Downloads section, for looking at it:
// DOWNLOADS_PNG=/tmp/downloads.png, with -sorted, -confirm and -empty
// beside it.
func TestRenderDownloads(t *testing.T) {
	path := os.Getenv("DOWNLOADS_PNG")
	if path == "" {
		t.Skip("set DOWNLOADS_PNG to a file")
	}
	base := strings.TrimSuffix(path, ".png")
	l := localization.For("ru")
	size := image.Pt(760, 640)
	p := loadedDownloads(t, sampleDownloads())
	files := newAttachmentFiles()
	defer files.Close()
	files.downloads = map[model.MessageKey]*fileDownload{
		{ChatID: 3, MessageID: 1}: {message: model.Message{Media: &model.MessageMedia{FileName: "Лекция 4.mkv", Size: 700_000_000}}, done: 260_000_000, total: 700_000_000, started: time.Now(), cancel: func() {}},
	}
	p.files = func() *attachmentFiles { return files }
	draw := func(gtx layout.Context) {
		p.Update(gtx, l)
		p.Layout(gtx, l, false)
	}
	renderFrames(t, size, path, draw)
	files.downloads = nil
	p.bySize = true
	p.selected = map[int64]bool{2: true, 4: true}
	for _, id := range []int64{1, 2, 3, 4} {
		p.row(id)
	}
	renderFrames(t, size, base+"-sorted.png", draw)
	p.pending = []int64{2, 4}
	p.confirm.Open()
	renderFrames(t, size, base+"-confirm.png", draw)
	empty := loadedDownloads(t, &fakeDownloads{})
	renderFrames(t, size, base+"-empty.png", func(gtx layout.Context) {
		empty.Update(gtx, l)
		empty.Layout(gtx, l, false)
	})
}

// clickLabel clicks the n-th button labeled label, from the top.
func clickLabel(t *testing.T, r *input.Router, label string, n int) {
	t.Helper()
	// A node is in the list and among its parent's children: each once.
	seen := map[image.Rectangle]bool{}
	var found []image.Rectangle
	var find func([]input.SemanticNode)
	find = func(nodes []input.SemanticNode) {
		for _, node := range nodes {
			if node.Desc.Label == label && node.Desc.Class == semantic.Button && !seen[node.Desc.Bounds] {
				seen[node.Desc.Bounds] = true
				found = append(found, node.Desc.Bounds)
			}
			find(node.Children)
		}
	}
	find(r.AppendSemantics(nil))
	sort.Slice(found, func(i, j int) bool { return found[i].Min.Y < found[j].Min.Y })
	if n >= len(found) {
		t.Fatalf("%d elements labeled %q, not %d", len(found), label, n+1)
	}
	at := found[n].Min.Add(found[n].Size().Div(2))
	pos := f32.Pt(float32(at.X), float32(at.Y))
	r.Queue(pointer.Event{Kind: pointer.Press, Source: pointer.Mouse, Buttons: pointer.ButtonPrimary, Position: pos},
		pointer.Event{Kind: pointer.Release, Source: pointer.Mouse, Position: pos})
}

// Files picked by their marks are deleted from the disk once the dialog is
// answered, and only then; a file's button goes to its message.
func TestDownloadsDelete(t *testing.T) {
	source := sampleDownloads()
	p := loadedDownloads(t, source)
	var opened []model.MessageKey
	p.openMessage = func(d model.DownloadedFile) { opened = append(opened, d.Key) }
	l := localization.For("en")
	var router input.Router
	frame := func() {
		ops := new(op.Ops)
		gtx := sharedContext(ops, image.Pt(760, 900))
		gtx.Source = router.Source()
		p.Update(gtx, l)
		p.Layout(gtx, l, false)
		router.Frame(ops)
	}
	frame()
	frame()
	// The newest first: the first and the third, picked.
	clickLabel(t, &router, l.T("downloads.select"), 0)
	frame()
	clickLabel(t, &router, l.T("downloads.select"), 2)
	frame()
	if len(p.selected) != 2 || !p.selected[1] || !p.selected[3] {
		t.Fatal("picked:", p.selected)
	}
	clickLabel(t, &router, l.T("downloads.delete"), 0)
	frame()
	frame()
	if !p.confirm.Shown() || len(source.deleted) != 0 {
		t.Fatal("deleted before the dialog was answered")
	}
	// The dialog's button, under the header's.
	clickLabel(t, &router, l.T("downloads.delete"), 1)
	frame()
	deadline := time.Now().Add(5 * time.Second)
	for {
		source.mu.Lock()
		n := len(source.deleted)
		source.mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not deleted")
		}
		frame()
		time.Sleep(time.Millisecond)
	}
	if source.deleted[0] != 1 || source.deleted[1] != 3 {
		t.Fatal("deleted:", source.deleted)
	}
	// Once the dialog has gone.
	for i := 0; p.confirm.Shown(); i++ {
		if i > 100 {
			t.Fatal("the dialog stays")
		}
		frame()
	}
	frame()
	clickLabel(t, &router, l.T("downloads.view_in_chat"), 1)
	frame()
	if len(opened) != 1 || opened[0] != (model.MessageKey{ChatID: 1, MessageID: 11}) {
		t.Fatal("opened:", opened)
	}
	// The header's folder, made if it is not there.
	folder := filepath.Join(t.TempDir(), "komarugram")
	p.dir = func() string { return folder }
	clickLabel(t, &router, l.T("downloads.show_in_folder"), 0)
	frame()
	for deadline := time.Now().Add(5 * time.Second); browserOpened.Load() != folder; {
		if time.Now().After(deadline) {
			t.Fatal("the folder was not shown:", browserOpened.Load())
		}
		time.Sleep(time.Millisecond)
	}
	if st, err := os.Stat(folder); err != nil || !st.IsDir() {
		t.Fatal("the folder was not made:", err)
	}
}

// The message of a file in a forum opens in its topic, at the message,
// from the Downloads section; one of a chat closes the topic open and
// opens the chat at it.
func TestDownloadOpensItsMessage(t *testing.T) {
	h := newAppForum(t)
	a := h.app
	var topic model.Topic
	for _, tp := range h.store.Topics(h.forum.ID).Topics {
		if !tp.General {
			topic = tp
			break
		}
	}
	a.section = section{kind: sectionDownloads}
	a.takeDownloadMessage(downloadMessage{chat: h.forum, topic: topic.ID, message: 5})
	thread := a.thread
	if a.section.kind != sectionAll || a.selected != h.forum.ID || thread == nil || !thread.topic || thread.name != topic.Title {
		t.Fatalf("section %v, chat %d, opened %+v; want the topic %q", a.section.kind, a.selected, thread, topic.Title)
	}
	if v, ok := h.store.Viewport(thread.chat.ID); !ok || v.AnchorMessageID != 5 {
		t.Fatalf("the topic opens at %+v", v)
	}
	chat := a.store.Chats()[1]
	a.takeDownloadMessage(downloadMessage{chat: chat, message: 3})
	if a.thread != nil || a.selected != chat.ID {
		t.Fatalf("chat %d, thread %+v", a.selected, a.thread)
	}
}

// downloadLookupStore is the demo's, where no chat is in the list, a channel is
// found by its ID, and a message is found after a while, in a topic.
type downloadLookupStore struct {
	*mockstore.Store
	asked   atomic.Int64
	lookups atomic.Int32
}

func (s *downloadLookupStore) Chats() []model.Chat { return nil }
func (s *downloadLookupStore) ChannelByID(_ context.Context, id int64) (model.Chat, error) {
	s.asked.Store(id)
	return model.Chat{ID: -1000000000000 - id, Forum: true}, nil
}
func (s *downloadLookupStore) LookupMessage(chat int64, id model.MessageID) (model.Message, model.LookupState) {
	if s.lookups.Add(1) < 3 {
		return model.Message{}, model.LookupLoading
	}
	return model.Message{ForumTopic: true, ReplyToTopID: 42}, model.LookupFound
}

// A chat not in the list is asked for by its ID, and the topic of a file
// downloaded before topics were kept is found from its message.
func TestDownloadFindsItsChatAndTopic(t *testing.T) {
	store := &downloadLookupStore{Store: mockstore.New(time.Now(), 0)}
	ctx := context.Background()
	chat, err := findChat(ctx, store, -1001234567890)
	if err != nil || store.asked.Load() != 1234567890 || chat.ID != -1001234567890 {
		t.Fatal(chat, err, store.asked.Load())
	}
	if topic := lookupTopic(ctx, store, model.MessageKey{ChatID: chat.ID, MessageID: 7}); topic != 42 {
		t.Fatal("topic", topic)
	}
	if _, err := findChat(ctx, store, -5); !errors.Is(err, model.ErrLinkNotFound) {
		t.Fatal("a basic group not in the list:", err)
	}
}

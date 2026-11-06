// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/bin"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/tg"

	"komarugram/internal/messenger/model"
)

// heldServer is filesServer with a hand on the uploads: the parts of the file
// it holds, the first to come, wait for the release or the end of their
// context, as a slow connection's would; the others go through.
type heldServer struct {
	filesServer
	mu sync.Mutex
	// held is the file held, entered tells it came, and release lets it go.
	held    int64
	entered chan struct{}
	release chan struct{}
	// sends, when set, holds the request that sends until it is closed.
	sends   chan struct{}
	sending chan struct{}
	files   map[int64]int
	stopped bool
	// pause is how long each part takes, one at a time.
	pause time.Duration
}

func newHeldServer() *heldServer {
	return &heldServer{entered: make(chan struct{}, 64), release: make(chan struct{}), sending: make(chan struct{}, 8), files: map[int64]int{}}
}

func (h *heldServer) invoke(ctx context.Context, in bin.Encoder, out bin.Decoder) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	var result bin.Encoder
	var err error
	switch r := in.(type) {
	case *tg.UploadSaveFilePartRequest:
		h.mu.Lock()
		time.Sleep(h.pause)
		if h.held == 0 {
			h.held = r.FileID
		}
		held := h.held == r.FileID
		h.files[r.FileID]++
		h.mu.Unlock()
		if held {
			h.entered <- struct{}{}
			select {
			case <-h.release:
			case <-ctx.Done():
				h.mu.Lock()
				h.stopped = true
				h.mu.Unlock()
				return ctx.Err()
			}
		}
		result = &tg.BoolTrue{}
	case *tg.MessagesSendMediaRequest, *tg.MessagesSendMultiMediaRequest:
		if h.sends != nil {
			h.sending <- struct{}{}
			<-h.sends
		}
		h.mu.Lock()
		result, err = h.filesServer.handle(in)
		h.mu.Unlock()
	default:
		h.mu.Lock()
		result, err = h.filesServer.handle(in)
		h.mu.Unlock()
	}
	if err != nil {
		return err
	}
	b := new(bin.Buffer)
	if err := result.Encode(b); err != nil {
		return err
	}
	return out.Decode(b)
}

func heldStore(t *testing.T) (*Store, *heldServer) {
	s := testStore(t)
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	s.history.histories[5] = &model.History{}
	server := newHeldServer()
	s.history.api = tg.NewClient(telegram.InvokeFunc(server.invoke))
	// A test that fails midway leaves nothing held for the store's end.
	t.Cleanup(func() {
		select {
		case <-server.release:
		default:
			close(server.release)
		}
	})
	return s, server
}

// bigFile writes a file of size bytes, enough parts for a share to be seen.
func bigFile(t *testing.T, name string, size int) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, bytes.Repeat([]byte("x"), size), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// pendingKeys are the messages on their way, in their order.
func pendingKeys(s *Store) []model.MessageKey {
	s.history.mu.Lock()
	defer s.history.mu.Unlock()
	keys := make([]model.MessageKey, 0, len(s.history.pending.list))
	for _, p := range s.history.pending.list {
		keys = append(keys, p.msg.Key)
	}
	return keys
}

func queueFiles(t *testing.T, s *Store, text string, group bool, paths ...string) []model.MessageKey {
	t.Helper()
	err := s.QueueMessage(context.Background(), 5, model.OutgoingMessage{RandomID: 1000, Text: text, Files: &model.OutgoingFiles{Paths: paths, Group: group}})
	if err != nil {
		t.Fatal(err)
	}
	return pendingKeys(s)
}

func waitEntered(t *testing.T, server *heldServer) {
	t.Helper()
	select {
	case <-server.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("nothing came to upload")
	}
}

// A file on its way tells how far it went, from before its upload starts to
// Telegram having it.
func TestUploadTellsHowFarItWent(t *testing.T) {
	s, server := heldStore(t)
	// The uploader's parts of 512 KB after it starts: several, and one held.
	size := 3 << 20
	path := bigFile(t, "big.bin", size)
	s.history.mu.Lock()
	api := s.history.api
	s.history.api = nil
	s.history.mu.Unlock()
	keys := queueFiles(t, s, "", false, path)
	if status, ok := s.UploadStatus(keys[0]); !ok || status != (model.UploadStatus{}) {
		t.Fatalf("before its upload: %+v, %v", status, ok)
	}
	if _, ok := s.UploadStatus(model.MessageKey{ChatID: 5, MessageID: 1}); ok {
		t.Fatal("something that is no file on its way has a status")
	}
	s.history.mu.Lock()
	s.history.api = api
	s.history.mu.Unlock()
	// Let the first part through, for a chunk to tell of.
	waitEntered(t, server)
	server.release <- struct{}{}
	deadline := time.Now().Add(10 * time.Second)
	var status model.UploadStatus
	for status.Done == 0 && time.Now().Before(deadline) {
		status, _ = s.UploadStatus(keys[0])
		time.Sleep(time.Millisecond)
	}
	if status.Done <= 0 || status.Done >= int64(size) || status.Total != int64(size) {
		t.Fatalf("midway: %+v", status)
	}
	close(server.release)
	waitOutgoing(t, s, 0)
	if _, ok := s.UploadStatus(keys[0]); ok {
		t.Fatal("still told of after it went")
	}
}

// A file of an album cancelled before its turn goes from the album, which goes
// without it, the others keeping their identities.
func TestCancelFileBeforeItsTurnInAlbum(t *testing.T) {
	s, server := heldStore(t)
	dir := t.TempDir()
	keys := queueFiles(t, s, "Holiday", true, picture(t, dir, "a.png", 30, 20), picture(t, dir, "b.png", 30, 20), picture(t, dir, "c.png", 30, 20))
	waitEntered(t, server)
	s.CancelUpload(keys[1])
	if got := pendingKeys(s); len(got) != 2 || got[0] != keys[0] || got[1] != keys[2] {
		t.Fatalf("on their way: %+v", got)
	}
	if _, ok := s.UploadStatus(keys[1]); ok {
		t.Fatal("the cancelled file is still told of")
	}
	close(server.release)
	waitOutgoing(t, s, 0)
	if len(server.albums) != 1 || len(server.uploaded) != 2 || len(server.singles) != 0 {
		t.Fatalf("%d albums, %d uploaded, %d singles", len(server.albums), len(server.uploaded), len(server.singles))
	}
	items := server.albums[0].MultiMedia
	if len(items) != 2 || items[0].RandomID != 1000 || items[1].RandomID != 1002 {
		t.Fatalf("items %+v", items)
	}
	if items[0].Message != "Holiday" || items[1].Message != "" {
		t.Fatalf("captions %q, %q", items[0].Message, items[1].Message)
	}
}

// Cancelling the file being uploaded ends its upload and no other: the rest
// upload and go, and its caption goes on the first of them.
func TestCancelFileUploadingLeavesTheOthers(t *testing.T) {
	s, server := heldStore(t)
	dir := t.TempDir()
	keys := queueFiles(t, s, "Holiday", true, picture(t, dir, "a.png", 30, 20), picture(t, dir, "b.png", 30, 20), picture(t, dir, "c.png", 30, 20))
	waitEntered(t, server)
	s.outbox.mu.Lock()
	e := s.outbox.entries[0]
	s.outbox.mu.Unlock()
	s.CancelUpload(keys[0])
	waitOutgoing(t, s, 0)
	if e.Attempts != 0 {
		t.Fatalf("the others lost %d attempts to it", e.Attempts)
	}
	server.mu.Lock()
	defer server.mu.Unlock()
	if !server.stopped {
		t.Fatal("its upload went on")
	}
	if len(server.albums) != 1 || len(server.uploaded) != 2 {
		t.Fatalf("%d albums, %d uploaded", len(server.albums), len(server.uploaded))
	}
	items := server.albums[0].MultiMedia
	if len(items) != 2 || items[0].RandomID != 1001 || items[1].RandomID != 1002 || items[0].Message != "Holiday" || items[1].Message != "" {
		t.Fatalf("items %+v", items)
	}
}

// An album of files keeps its caption on the last that is left, and the
// message shows it there while it waits.
func TestCancelFileMovesCaptionToTheLastOfFiles(t *testing.T) {
	s, server := heldStore(t)
	keys := queueFiles(t, s, "Notes", true, bigFile(t, "a.txt", 10), bigFile(t, "b.txt", 10), bigFile(t, "c.txt", 10))
	waitEntered(t, server)
	s.CancelUpload(keys[2])
	shown := s.History(5).Messages
	if len(shown) != 2 || shown[1].Key != keys[1] || shown[1].Text != "Notes" {
		t.Fatalf("shown: %+v", shown)
	}
	close(server.release)
	waitOutgoing(t, s, 0)
	items := server.albums[0].MultiMedia
	if len(items) != 2 || items[0].Message != "" || items[1].Message != "Notes" {
		t.Fatalf("items %+v", items)
	}
}

// What is left of an album of one goes as a message by itself: no album holds
// one file.
func TestCancelFileLeavingOneSendsItAlone(t *testing.T) {
	s, server := heldStore(t)
	dir := t.TempDir()
	keys := queueFiles(t, s, "Holiday", true, picture(t, dir, "a.png", 30, 20), picture(t, dir, "b.png", 30, 20))
	waitEntered(t, server)
	s.CancelUpload(keys[1])
	close(server.release)
	waitOutgoing(t, s, 0)
	if len(server.albums) != 0 || len(server.singles) != 1 {
		t.Fatalf("%d albums, %d singles", len(server.albums), len(server.singles))
	}
	if r := server.singles[0]; r.RandomID != 1000 || r.Message != "Holiday" {
		t.Fatalf("the message: %+v", r)
	}
	if _, ok := server.singles[0].Media.(*tg.InputMediaPhoto); !ok {
		t.Fatalf("sent %T, not what the server has", server.singles[0].Media)
	}
}

// With every file cancelled nothing goes: the message is over, its staged files
// gone, and who waits for it told.
func TestCancelEveryFileEndsTheMessage(t *testing.T) {
	s, server := heldStore(t)
	dir := t.TempDir()
	done := make(chan error, 1)
	go func() {
		done <- s.Send(context.Background(), 5, model.OutgoingMessage{RandomID: 1000, Files: &model.OutgoingFiles{Group: true, Paths: []string{picture(t, dir, "a.png", 30, 20), picture(t, dir, "b.png", 30, 20)}}})
	}()
	waitEntered(t, server)
	for _, key := range pendingKeys(s) {
		s.CancelUpload(key)
	}
	select {
	case err := <-done:
		if !errors.Is(err, errOutgoingCancelled) {
			t.Fatalf("told %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("whoever waits was not told")
	}
	waitOutgoing(t, s, 0)
	if len(server.albums)+len(server.singles) != 0 {
		t.Fatal("something went")
	}
	if len(pendingKeys(s)) != 0 {
		t.Fatal("still shown")
	}
	rows, err := s.Cache().Outgoing(context.Background())
	if err != nil || len(rows) != 0 {
		t.Fatalf("%d rows kept: %v", len(rows), err)
	}
}

// A message waiting for the connection is cancelled at once, with no worker
// to do it.
func TestCancelFileOfMessageNotOnItsWay(t *testing.T) {
	s, server := heldStore(t)
	s.history.mu.Lock()
	s.history.api = nil
	s.history.mu.Unlock()
	keys := queueFiles(t, s, "", false, bigFile(t, "a.txt", 10))
	s.CancelUpload(keys[0])
	waitOutgoing(t, s, 0)
	if len(pendingKeys(s)) != 0 || len(server.singles) != 0 {
		t.Fatal("it stayed")
	}
}

// Once the request that sends a file was made it may reach Telegram whatever
// happens here, so that a cancel changes nothing.
func TestCancelFileOnceSentDoesNothing(t *testing.T) {
	s, server := heldStore(t)
	server.sends = make(chan struct{})
	close(server.release)
	keys := queueFiles(t, s, "", false, bigFile(t, "a.txt", 10))
	select {
	case <-server.sending:
	case <-time.After(10 * time.Second):
		t.Fatal("it was not sent")
	}
	s.CancelUpload(keys[0])
	if len(pendingKeys(s)) != 1 {
		t.Fatal("the message left though it goes")
	}
	close(server.sends)
	waitOutgoing(t, s, 0)
	if len(server.singles) != 1 {
		t.Fatalf("%d sent", len(server.singles))
	}
}

// A cancel survives closing the client: the file does not come back.
func TestCancelledFileStaysCancelledAfterRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	database := filepath.Join(dir, "history")
	s := New(nil)
	if err := s.Configure(ctx, "a", database, nil); err != nil {
		t.Fatal(err)
	}
	s.history.peers[5] = peerRecord{Kind: "user", ID: 5, Hash: 55}
	s.history.histories[5] = &model.History{}
	keys := queueFiles(t, s, "Notes", true, bigFile(t, "a.txt", 10), bigFile(t, "b.txt", 10))
	s.CancelUpload(keys[1])
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s = New(nil)
	if err := s.Configure(ctx, "a", database, nil); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.history.histories[5] = &model.History{}
	shown := s.History(5).Messages
	if len(shown) != 1 || shown[0].Key != keys[0] || shown[0].Text != "Notes" {
		t.Fatalf("after the restart: %+v", shown)
	}
	if _, ok := s.UploadStatus(keys[1]); ok {
		t.Fatal("the cancelled file came back")
	}
	if _, ok := s.UploadStatus(keys[0]); !ok {
		t.Fatal("the file left is not told of")
	}
	server := new(filesServer)
	s.history.api = composerAPI(server.handle)
	s.startOutgoing()
	waitOutgoing(t, s, 0)
	if len(server.singles) != 1 || server.singles[0].RandomID != 1000 || server.singles[0].Message != "Notes" {
		t.Fatalf("sent %+v", server.singles)
	}
}

// An upload goes on for as long as its file moves, however long that is; one
// that stops is an attempt out of time.
func TestUploadOutlastsPatienceWhileItMoves(t *testing.T) {
	patience := outgoingPatience
	outgoingPatience = 150 * time.Millisecond
	t.Cleanup(func() { outgoingPatience = patience })
	s, server := heldStore(t)
	server.pause = 60 * time.Millisecond
	close(server.release)
	// Parts of 512 KB, one by one: far longer than the patience in all.
	queueFiles(t, s, "", false, bigFile(t, "big.bin", 4<<20))
	s.outbox.mu.Lock()
	e := s.outbox.entries[0]
	s.outbox.mu.Unlock()
	waitOutgoing(t, s, 0)
	if e.Attempts != 0 || len(server.singles) != 1 {
		t.Fatalf("%d attempts lost, %d sent", e.Attempts, len(server.singles))
	}
}

func TestUploadThatStopsRunsOutOfTime(t *testing.T) {
	patience := outgoingPatience
	outgoingPatience = 50 * time.Millisecond
	t.Cleanup(func() { outgoingPatience = patience })
	s, server := heldStore(t)
	queueFiles(t, s, "", false, bigFile(t, "a.txt", 10))
	waitEntered(t, server)
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		s.outbox.mu.Lock()
		attempts := s.outbox.entries[0].Attempts
		s.outbox.mu.Unlock()
		if attempts > 0 {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("it waits for ever")
}

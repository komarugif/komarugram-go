// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/audio"
	"komarugram/pkg/voice"
)

// silentPlayback stands for the system's output in tests: nothing is heard,
// and the source stays where it was put.
type silentPlayback struct {
	mu      sync.Mutex
	src     audio.Source
	playing bool
	closed  bool
	// ended is set by end: the sound played through.
	ended  bool
	volume float64
}

func (s *silentPlayback) Pause()  { s.mu.Lock(); s.playing = false; s.mu.Unlock() }
func (s *silentPlayback) Resume() { s.mu.Lock(); s.playing = true; s.mu.Unlock() }
func (s *silentPlayback) Playing() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.playing
}
func (s *silentPlayback) Ended() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ended
}

// end is the sound playing to its end.
func (s *silentPlayback) end()            { s.mu.Lock(); s.playing, s.ended = false, true; s.mu.Unlock() }
func (s *silentPlayback) Position() int64 { return s.src.Position() }
func (s *silentPlayback) SeekSample(pos int64) error {
	return s.src.SeekSample(pos)
}
func (s *silentPlayback) SetVolume(volume float64) {
	s.mu.Lock()
	s.volume = volume
	s.mu.Unlock()
}
func (s *silentPlayback) Close() { s.mu.Lock(); s.closed = true; s.mu.Unlock() }
func (s *silentPlayback) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// audioHarnessPage is a chat page of the demo, whose voice messages play
// into silentPlaybacks.
func audioHarnessPage(t *testing.T) (*chatPage, func(i int) *silentPlayback, func(id string) model.Message) {
	t.Helper()
	store := mockstore.New(time.Now(), 0)
	p := newChatPage(store, func() {})
	t.Cleanup(p.Close)
	var played []*silentPlayback
	var mu sync.Mutex
	p.audio.play = func(src audio.Source) (audioPlayback, error) {
		s := &silentPlayback{src: src, playing: true}
		mu.Lock()
		played = append(played, s)
		mu.Unlock()
		return s, nil
	}
	playedAt := func(i int) *silentPlayback {
		mu.Lock()
		defer mu.Unlock()
		if i >= len(played) {
			return nil
		}
		return played[i]
	}
	find := func(id string) model.Message {
		for _, m := range store.History(2).Messages {
			if m.Media != nil && m.Media.ID == id {
				return m
			}
		}
		t.Fatalf("no %s in the demo", id)
		return model.Message{}
	}
	return p, playedAt, find
}

func waitAudio(t *testing.T, what string, done func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); !done(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal(what)
		}
	}
}

// A voice message plays in the client: a click plays it, another pauses
// it, a click on its waveform moves it there.
func TestVoicePlaysPausesAndSeeks(t *testing.T) {
	p, played, find := audioHarnessPage(t)
	m := find("demo/voice")
	if !internalAudio(m) {
		t.Fatal("an OGG voice message does not play in the client")
	}
	p.audio.toggle(p, m, -1)
	waitAudio(t, "it did not start", func() bool { return p.audio.state(m).playing })
	if s := p.audio.state(m); !s.active || s.loading || s.progress != 0 {
		t.Fatalf("state %+v", s)
	}
	p.audio.toggle(p, m, -1)
	if p.audio.state(m).playing {
		t.Fatal("a second click did not pause it")
	}
	p.audio.toggle(p, m, 0.5)
	s := p.audio.state(m)
	if !s.playing || s.progress < 0.49 || s.progress > 0.51 {
		t.Fatalf("after a click in the middle: %+v", s)
	}
	if s.elapsed < 3600*time.Millisecond || s.elapsed > 3700*time.Millisecond {
		t.Fatalf("elapsed %v in the middle of 7.3 s", s.elapsed)
	}
	// Another message takes its place.
	other := find("demo/voice-bare")
	p.audio.toggle(p, other, -1)
	waitAudio(t, "the other did not start", func() bool { return p.audio.state(other).playing })
	if p.audio.state(m).active || !played(0).isClosed() {
		t.Fatal("the first message still plays")
	}
	// The bar's button ends it.
	p.audio.stop()
	if p.audio.state(other).active || !played(1).isClosed() {
		t.Fatal("stop left it playing")
	}
}

// A voice message sent without a waveform gets one, worked out as it
// plays; one sent with it keeps Telegram's.
func TestVoiceWaveformWorkedOut(t *testing.T) {
	p, _, find := audioHarnessPage(t)
	bare, sent := find("demo/voice-bare"), find("demo/voice")
	if p.audio.waveform(bare) != nil {
		t.Fatal("a waveform before playing")
	}
	p.audio.toggle(p, bare, -1)
	waitAudio(t, "no waveform was worked out", func() bool { return p.audio.waveform(bare) != nil })
	// The demo's two voice messages are one file: the waveform worked out
	// is the one Telegram would send.
	got, want := voice.Bars(p.audio.waveform(bare)), voice.Bars(sent.Media.Waveform)
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("bar %d is %d, want %d", i, got[i], want[i])
		}
	}
}

// Opus plays in libopus's sandbox, MP3 and the other dr_libs formats in
// theirs, M4A in fdk-aac's, voice messages and music alike; anything else
// opens in the external player.
func TestAudioFormats(t *testing.T) {
	for _, kind := range []model.MessageKind{model.MessageVoice, model.MessageMusic} {
		for mime, want := range map[string]string{"audio/ogg": "opus", "audio/mpeg": "dr", "audio/flac": "dr", "audio/x-wav": "dr", "audio/mp4": "aac", "audio/x-m4a": "aac", "video/mp4": "", "audio/aac": "", "audio/x-ms-wma": ""} {
			m := model.Message{Kind: kind, Media: &model.MessageMedia{MIMEType: mime}}
			if got := audioFormat(m); got != want {
				t.Errorf("kind %d, %q: %q, want %q", kind, mime, got, want)
			}
		}
	}
	// A voice message sent without a type is Telegram's own, Opus; music
	// without one is not known.
	if audioFormat(model.Message{Kind: model.MessageVoice, Media: &model.MessageMedia{}}) != "opus" || audioFormat(model.Message{Kind: model.MessageMusic, Media: &model.MessageMedia{}}) != "" {
		t.Error("a file without a type")
	}
	if audioFormat(model.Message{Kind: model.MessageFile, Media: &model.MessageMedia{MIMEType: "audio/mpeg"}}) != "" {
		t.Error("a file plays as audio")
	}
	// A video message plays in the client when it is an MP4, as Telegram's
	// are.
	for mime, want := range map[string]string{"video/mp4": "round", "": "round", "video/webm": ""} {
		if got := audioFormat(model.Message{Kind: model.MessageRound, Media: &model.MessageMedia{MIMEType: mime}}); got != want {
			t.Errorf("video message %q: %q, want %q", mime, got, want)
		}
	}
	if audioFormat(model.Message{Kind: model.MessageVideo, Media: &model.MessageMedia{MIMEType: "video/mp4"}}) != "" {
		t.Error("a video plays in the client")
	}
}

// An MP3 voice message, as some bots send, plays in the client and moves.
func TestVoiceMP3Plays(t *testing.T) {
	p, _, find := audioHarnessPage(t)
	m := find("demo/voice-mp3")
	p.audio.toggle(p, m, 0.5)
	waitAudio(t, "it did not start", func() bool { return p.audio.state(m).playing })
	if s := p.audio.state(m); s.progress < 0.49 || s.progress > 0.51 {
		t.Fatalf("after a click in the middle: %+v", s)
	}
	waitAudio(t, "no waveform was worked out", func() bool { return p.audio.waveform(m) != nil })
}

// A voice message shown without a waveform gets one as soon as it is
// shown, as in Telegram Desktop, and only once.
func TestVoiceWaveformWhenShown(t *testing.T) {
	p, played, find := audioHarnessPage(t)
	for _, id := range []string{"demo/voice-bare", "demo/voice-mp3"} {
		m := find(id)
		p.audio.showWaveform(p, m)
		waitAudio(t, id+": no waveform when shown", func() bool { return p.audio.waveform(m) != nil })
		if played(0) != nil {
			t.Fatalf("%s played to get its waveform", id)
		}
		waitAudio(t, "the worker did not end", func() bool {
			p.audio.mu.Lock()
			defer p.audio.mu.Unlock()
			return !p.audio.busy
		})
	}
	p.audio.mu.Lock()
	shown := len(p.audio.shown)
	p.audio.mu.Unlock()
	if shown != 2 {
		t.Fatalf("%d shown", shown)
	}
}

// An M4A voice message plays through fdk-aac, the module KOMARUGRAM_AACDEC
// names: it is not in this repository.
func TestVoiceM4APlays(t *testing.T) {
	if os.Getenv("KOMARUGRAM_AACDEC") == "" {
		t.Skip("set KOMARUGRAM_AACDEC to an aacdec.wasm")
	}
	p, _, find := audioHarnessPage(t)
	m := find("demo/voice-m4a")
	p.audio.toggle(p, m, 0.5)
	waitAudio(t, "it did not start", func() bool { return p.audio.state(m).playing })
	if s := p.audio.state(m); s.progress < 0.49 || s.progress > 0.51 {
		t.Fatalf("after a click in the middle: %+v", s)
	}
	waitAudio(t, "no waveform was worked out", func() bool { return p.audio.waveform(m) != nil })
}

// Music plays in the client too, and moves.
func TestMusicPlays(t *testing.T) {
	p, _, find := audioHarnessPage(t)
	m := find("demo/music")
	if m.Kind != model.MessageMusic || !internalAudio(m) {
		t.Fatal("demo music does not play in the client")
	}
	p.audio.toggle(p, m, 0.5)
	waitAudio(t, "it did not start", func() bool { return p.audio.state(m).playing })
	if s := p.audio.state(m); s.progress < 0.49 || s.progress > 0.51 {
		t.Fatalf("after a click in the middle: %+v", s)
	}
	// Music gets no waveform.
	if p.audio.waveform(m) != nil {
		t.Fatal("music got a waveform")
	}
}

// streamingStore gives files as a download does: a range at a time, any
// range, as MediaStream fetches them. The first and the last part are
// there; the rest is held back until let go.
type streamingStore struct {
	model.ConversationStore
	data    []byte
	ready   int64
	mu      sync.Mutex
	release chan struct{}
}

func (s *streamingStore) MediaStream(ctx context.Context, m model.Message) (io.ReaderAt, int64, func(), error) {
	return s, int64(len(s.data)), func() {}, nil
}

func (s *streamingStore) ReadAt(p []byte, off int64) (int, error) {
	if off+int64(len(p)) > s.ready && off < int64(len(s.data))-s.ready {
		select {
		case <-s.release:
		case <-time.After(10 * time.Second):
			return 0, errors.New("never let go")
		}
	}
	n := copy(p, s.data[min(int64(len(s.data)), off):])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// A file that downloads plays as it does: what came first plays before the
// rest is there.
func TestMusicPlaysAsItDownloads(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	var music model.Message
	for _, m := range store.History(2).Messages {
		if m.Media != nil && m.Media.ID == "demo/music" {
			music = m
		}
	}
	data, err := store.Media(context.Background(), music)
	if err != nil {
		t.Fatal(err)
	}
	// Twenty copies: an MP3 twice as long as the first part of it.
	data = bytes.Repeat(data, 20)
	music.Media.Size, music.Media.Duration = int64(len(data)), 100*time.Second
	s := &streamingStore{ConversationStore: store, data: data, ready: 128 << 10, release: make(chan struct{})}
	p := newChatPage(s, func() {})
	t.Cleanup(p.Close)
	p.audio.play = func(src audio.Source) (audioPlayback, error) {
		return &silentPlayback{src: src, playing: true}, nil
	}
	p.audio.toggle(p, music, -1)
	waitAudio(t, "it did not start before the file was there", func() bool { return p.audio.state(music).playing })
	close(s.release)
}

// A file no decoder here takes goes to the external player, with no
// error told.
func TestUnknownFormatGoesExternal(t *testing.T) {
	store := mockstore.New(time.Now(), 0)
	p := newChatPage(store, func() {})
	t.Cleanup(p.Close)
	// An OGG file of Vorbis, or anything that is not Opus: the demo's MP3
	// said to be OGG.
	var m model.Message
	for _, x := range store.History(2).Messages {
		if x.Media != nil && x.Media.ID == "demo/music" {
			m = x
		}
	}
	m.Media.MIMEType = "audio/ogg"
	p.audio.toggle(p, m, -1)
	var external *model.Message
	waitAudio(t, "it did not go to the external player", func() bool {
		external, _ = p.audio.takeExternal()
		return external != nil
	})
	if external.Key != m.Key || p.audio.state(m).active {
		t.Fatalf("external %+v, state %+v", external.Key, p.audio.state(m))
	}
	p.errorMu.Lock()
	defer p.errorMu.Unlock()
	if p.mediaError != nil {
		t.Fatalf("an error was told: %v", p.mediaError)
	}
}

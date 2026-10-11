// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"gioui.org/io/key"

	"komarugram/internal/messenger/model"
	"komarugram/pkg/voice"
)

// fakeRecorder has recorded a second of a loud tone.
type fakeRecorder struct {
	mu                 sync.Mutex
	stopped, cancelled bool
	failed             error
}

func (r *fakeRecorder) Level() float32          { return 0.5 }
func (r *fakeRecorder) Duration() time.Duration { return time.Second }
func (r *fakeRecorder) Full() bool              { return false }
func (r *fakeRecorder) Failed() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failed
}
func (r *fakeRecorder) Stop() ([]int16, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stopped = true
	pcm := make([]int16, 48000)
	for i := range pcm {
		pcm[i] = int16(i%100*300 - 15000)
	}
	return pcm, nil
}
func (r *fakeRecorder) Cancel() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancelled = true
}

func voiceHarness(t *testing.T) (*menuHarness, *[]*fakeRecorder) {
	h := newMenuHarness(t, nil)
	var recorders []*fakeRecorder
	h.page.composer.voice = voiceTools{
		record: func(context.Context, string) (voiceRecorder, error) {
			r := &fakeRecorder{}
			recorders = append(recorders, r)
			return r, nil
		},
		encode: func(_ context.Context, _ string, _ []int16, path string) error {
			return os.WriteFile(path, []byte("OggS"), 0o600)
		},
		choose: func(context.Context, string) fileChoice {
			t.Error("a file was asked for with an FFmpeg")
			return fileChoice{}
		},
	}
	return h, &recorders
}

// sentMessages waits for the composer to send something.
func sentMessages(t *testing.T, h *menuHarness) []model.OutgoingMessage {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); ; {
		h.frame()
		h.store.mu.Lock()
		sent := append([]model.OutgoingMessage(nil), h.store.sent...)
		h.store.mu.Unlock()
		if len(sent) > 0 {
			return sent
		}
		if time.Now().After(deadline) {
			t.Fatal("nothing sent")
		}
	}
}

// oggOpus is an OGG file of Opus that plays for two seconds, as far as its
// headers tell.
func oggOpus() []byte {
	page := func(packet []byte, granule uint64) []byte {
		b := append([]byte("OggS\x00\x00"), binary.LittleEndian.AppendUint64(nil, granule)...)
		b = append(b, make([]byte, 12)...)
		b = append(b, 1, byte(len(packet)))
		return append(b, packet...)
	}
	head := binary.LittleEndian.AppendUint16([]byte("OpusHead\x01\x01"), 312)
	return append(page(head, 0), page([]byte("sound"), 2*48000+312)...)
}

// Without an FFmpeg, the microphone asks for an audio file and sends it as
// a voice message, with the duration its headers tell; the file is the
// user's, and stays.
func TestComposerSendsChosenVoiceFile(t *testing.T) {
	h, _ := voiceHarness(t)
	c := h.page.composer
	c.ffmpeg = func() string { return "/opt/ffmpeg/ffmpeg" }
	var asked []string
	c.voice.record = func(_ context.Context, ffmpeg string) (voiceRecorder, error) {
		asked = append(asked, ffmpeg)
		return nil, voice.ErrNoFFmpeg
	}
	path := filepath.Join(t.TempDir(), "note.ogg")
	if err := os.WriteFile(path, oggOpus(), 0o600); err != nil {
		t.Fatal(err)
	}
	c.voice.choose = func(context.Context, string) fileChoice { return fileChoice{path: path} }
	c.micClick.click.Click()
	h.frames(2)
	m := sentMessages(t, h)[0]
	if len(asked) != 1 || asked[0] != "/opt/ffmpeg/ffmpeg" {
		t.Fatalf("recorded with %q", asked)
	}
	if m.Path != path || m.Voice == nil || m.Voice.Duration != 2*time.Second || m.FFmpeg != "/opt/ffmpeg/ffmpeg" {
		t.Fatalf("sent %+v", m)
	}
	if c.draft(1).err != nil {
		t.Fatalf("error %v", c.draft(1).err)
	}
	h.frames(5)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the chosen file went: %v", err)
	}
}

// A file that is not a voice message's format is not sent, and the
// composer says why; a cancelled chooser does nothing.
func TestComposerRejectsVoiceFile(t *testing.T) {
	h, _ := voiceHarness(t)
	c := h.page.composer
	c.voice.record = func(context.Context, string) (voiceRecorder, error) { return nil, voice.ErrNoFFmpeg }
	c.voice.choose = func(context.Context, string) fileChoice { return fileChoice{} }
	c.micClick.click.Click()
	h.frames(4)
	if c.draft(1).err != nil || c.choosing {
		t.Fatalf("a cancelled chooser left %v, choosing %t", c.draft(1).err, c.choosing)
	}
	path := filepath.Join(t.TempDir(), "song.mp3")
	if err := os.WriteFile(path, []byte("not a sound"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.voice.choose = func(context.Context, string) fileChoice { return fileChoice{path: path} }
	c.micClick.click.Click()
	for deadline := time.Now().Add(5 * time.Second); c.draft(1).err == nil; {
		if time.Now().After(deadline) {
			t.Fatal("no error for a file that is not a voice message")
		}
		h.frame()
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	if len(h.store.sent) != 0 {
		t.Fatalf("sent %+v", h.store.sent)
	}
}

// The microphone records while the text is empty; Send sends the
// recording as a voice message, whose file goes once it is sent.
func TestComposerRecordsVoice(t *testing.T) {
	h, recorders := voiceHarness(t)
	c := h.page.composer
	if !c.canRecord(c.draft(1)) {
		t.Fatal("no microphone with an empty composer")
	}
	c.micClick.click.Click()
	h.frames(3)
	if c.recording == nil || len(*recorders) != 1 {
		t.Fatal("the microphone did not record")
	}
	c.send.click.Click()
	h.frames(2)
	m := sentMessages(t, h)[0]
	if m.Voice == nil || m.Voice.Duration != time.Second || len(m.Voice.Waveform) != 63 || m.Path == "" || !(*recorders)[0].stopped {
		t.Fatalf("sent %+v", m)
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		h.frame()
		if _, err := os.Stat(m.Path); errors.Is(err, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the recording's file was kept after it was sent")
		}
	}
	// Text in the composer takes the microphone's place.
	c.draft(1).editor.SetText("hi")
	if c.canRecord(c.draft(1)) {
		t.Fatal("microphone offered beside text")
	}
}

// Escape drops the recording; a microphone that fails tells so.
func TestComposerCancelsVoice(t *testing.T) {
	h, recorders := voiceHarness(t)
	c := h.page.composer
	// The history has the keyboard, as after a click in it; it takes
	// Escape to clear its selection.
	h.router.Source().Execute(key.FocusCmd{Tag: &h.page.keyboard})
	h.frame()
	c.micClick.click.Click()
	h.frames(3)
	h.router.Queue(key.Event{Name: key.NameEscape, State: key.Press})
	h.frames(3)
	if c.recording != nil {
		t.Fatal("Escape left the recording")
	}
	if !h.router.Source().Focused(&c.draft(1).editor) {
		t.Fatal("the field did not get the focus back")
	}
	for deadline := time.Now().Add(5 * time.Second); ; {
		(*recorders)[0].mu.Lock()
		cancelled := (*recorders)[0].cancelled
		(*recorders)[0].mu.Unlock()
		if cancelled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the recorder was not cancelled")
		}
		time.Sleep(5 * time.Millisecond)
	}
	c.micClick.click.Click()
	h.frames(3)
	r := (*recorders)[1]
	r.mu.Lock()
	r.failed = errors.New("no microphone")
	r.mu.Unlock()
	h.frames(2)
	if c.recording != nil || c.draft(1).err == nil {
		t.Fatal("a failed recording went on")
	}
	h.store.mu.Lock()
	defer h.store.mu.Unlock()
	if len(h.store.sent) != 0 {
		t.Fatalf("sent %+v", h.store.sent)
	}
}

// A chooser prints the paths it took one to a line, with the line ends of
// its system and a last one; the names keep their spaces.
func TestSplitChosenPaths(t *testing.T) {
	for _, c := range []struct {
		out  string
		want []string
	}{
		{"", nil},
		{"\n", nil},
		{"/home/a b/one.png\n/home/two.jpg\n", []string{"/home/a b/one.png", "/home/two.jpg"}},
		{"C:\\Users\\я\\one.png\r\nC:\\Users\\я\\two.png\r\n", []string{"C:\\Users\\я\\one.png", "C:\\Users\\я\\two.png"}},
		{"/single", []string{"/single"}},
		// PowerShell 2.0 on Windows 7, told to print UTF-8.
		{"\ufeffC:\\kg\\ffmpeg.exe\r\n", []string{"C:\\kg\\ffmpeg.exe"}},
	} {
		if got := splitPaths(c.out); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: %q, want %q", c.out, got, c.want)
		}
	}
}

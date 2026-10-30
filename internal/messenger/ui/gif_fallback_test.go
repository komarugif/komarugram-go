// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"os"
	"testing"
	"time"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/player"
	"komarugram/pkg/program"
)

// A GIF the sandbox cannot play asks where else to play it, offering what
// there is: FFmpeg, the external player, or both; with neither, the toast
// says what to install. Nothing on the system is looked for here, only
// the paths given, as with -no-integrations.
func TestGIFFallbackOffersWhatThereIs(t *testing.T) {
	program.SetSearching(false)
	t.Cleanup(func() { program.SetSearching(true) })
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	l := localization.For("en")
	gif := model.Message{Key: model.MessageKey{ChatID: 1, MessageID: 7}, Kind: model.MessageGIF, Media: &model.MessageMedia{ID: "gif/7", MIMEType: "video/mp4"}}
	for _, c := range []struct {
		name           string
		ffmpeg, player bool
	}{{"both", true, true}, {"ffmpeg", true, false}, {"player", false, true}, {"none", false, false}} {
		p := newChatPage(mockstore.New(time.Now(), 0), func() {})
		ffmpeg := ""
		if c.ffmpeg {
			ffmpeg = exe
		}
		p.media.ConfigureDecoders(false, true, ffmpeg)
		p.playerPaths = func() map[player.Kind]string {
			if c.player {
				return map[player.Kind]string{player.VLC: exe}
			}
			return nil
		}
		p.gifUnplayable(gif, l)
		d := &p.gifFallback
		if c.name == "none" {
			if d.modal.Shown() || p.toast.text != l.T("gif_fallback.none") {
				t.Errorf("none: a dialog %v, the toast %q", d.modal.Shown(), p.toast.text)
			}
			p.Close()
			continue
		}
		if !d.modal.Shown() || d.ffmpeg != c.ffmpeg || d.player != c.player || d.msg.Key != gif.Key {
			t.Errorf("%s: shown %v, FFmpeg %v, player %v", c.name, d.modal.Shown(), d.ffmpeg, d.player)
		}
		p.Close()
	}
}

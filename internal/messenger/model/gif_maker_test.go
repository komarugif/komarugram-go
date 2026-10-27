// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"errors"
	"testing"
)

// The bot's messages, as video2gif words them, tell where it is; its
// failures are errors, and what tells neither is passed over.
func TestParseGIFBot(t *testing.T) {
	for _, c := range []struct {
		text  string
		ok    bool
		err   error
		stage GIFStage
		share float64
		queue int
	}{
		{text: "You are in the queue. Your position: 3", ok: true, stage: GIFQueued, share: -1, queue: 3},
		{text: "Downloading file...", ok: true, stage: GIFDownloading, share: -1},
		{text: "Extracting data from video...", ok: true, stage: GIFProbing, share: -1},
		{text: "Processing...\nResolution: 1920x1080 -> 1280x720\nProgress: 42.50%\nFrames 425/1000\nFile size: 1024kB\nSpeed: 2.1x", ok: true, stage: GIFEncoding, share: .425},
		// Frames the bot could not count make its share Infinity, or NaN.
		{text: "Processing...\nResolution: 640x360 -> 640x360\nProgress: Infinity%\nFrames 30/0\nFile size: 12kB\nSpeed: 1x", ok: true, stage: GIFEncoding, share: -1},
		{text: "Processing...\nProgress: NaN%\nFrames 25/100", ok: true, stage: GIFEncoding, share: .25},
		// Frames miscounted make more done than all.
		{text: "Processing...\nProgress: 120.00%\nFrames 120/100", ok: true, stage: GIFEncoding, share: -1},
		{text: "Sending a gif...", ok: true, stage: GIFSending, share: -1},
		{text: "File size too large", ok: true, err: ErrGIFTooLarge},
		{text: "You can send no more than 2 videos at a time!", ok: true, err: ErrGIFBotBusy},
		{text: "An unknown error has occurred. If the problem persists, please contact @kotik_admin", ok: true, err: ErrGIFBotFailed},
		{text: "An error occurred", ok: true, err: ErrGIFBotFailed},
		{text: "Cancelled.", ok: true, err: ErrGIFBotCancelled},
		{text: "Nothing to cancel."},
		{text: "Hey! Send a video to this bot and get it as a GIF."},
		{text: "Please note: This GIF may not display correctly due to it exceeding Telegram's recommended GIF size."},
	} {
		p, ok, err := ParseGIFBot(c.text)
		if ok != c.ok || !errors.Is(err, c.err) || err != nil && c.err == nil {
			t.Errorf("%q: ok %v, err %v", c.text, ok, err)
			continue
		}
		if err == nil && ok && (p.Stage != c.stage || p.Share != c.share || p.Queue != c.queue) {
			t.Errorf("%q: %+v", c.text, p)
		}
	}
}

// A video is offered as a GIF unless its chat protects it; a GIF or a
// round video is not.
func TestCanMakeGIF(t *testing.T) {
	m := Message{Kind: MessageVideo, Media: &MessageMedia{ID: "v"}}
	if !CanMakeGIF(m) {
		t.Fatal("a video is not offered")
	}
	protected := m
	protected.NoForwards = true
	round := m
	round.Kind = MessageRound
	for _, no := range []Message{protected, round, {Kind: MessageVideo}} {
		if CanMakeGIF(no) {
			t.Errorf("offered %+v", no)
		}
	}
}

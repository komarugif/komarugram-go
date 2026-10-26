// SPDX-License-Identifier: Unlicense OR MIT

package model

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// A video is saved as a GIF the way Telegram plays one: no sound, at most
// HD, within GIFMaxBytes. It is converted by FFmpeg, or, without one, by
// GIFBot, a bot of KomaruGram's maintainer, which takes the video
// forwarded to it and sends it back as a GIF.

// GIFBot is the username of the bot that converts a video to a GIF;
// GIFBotCancel is its command that stops the conversions of the account.
const (
	GIFBot       = "komaruconvbot"
	GIFBotCancel = "/cancel"
)

// GIFMaxBytes is the most a GIF saved may weigh: Telegram plays a larger
// one as a video.
const GIFMaxBytes = 10 << 20

// GIFBotMaxBytes is the largest video GIFBot takes.
const GIFBotMaxBytes = 20 << 20

var (
	// ErrGIFTooLarge is a GIF converted that still weighs more than
	// GIFMaxBytes, or a video larger than the bot takes.
	ErrGIFTooLarge = errors.New("GIF exceeds 10 MiB")
	// ErrGIFBotSilent is the bot saying nothing for GIFBotPatience.
	ErrGIFBotSilent = errors.New("GIF bot does not answer")
	// ErrGIFBotBusy is the bot converting as many of the account's videos
	// as it takes at a time.
	ErrGIFBotBusy = errors.New("GIF bot is busy with other videos")
	// ErrGIFBotFailed is the bot telling it could not convert the video.
	ErrGIFBotFailed = errors.New("GIF bot failed to convert the video")
	// ErrGIFBotCancelled is the bot telling the conversion was cancelled,
	// by GIFBotCancel sent from elsewhere.
	ErrGIFBotCancelled = errors.New("GIF bot conversion cancelled")
)

// GIFBotPatience is how long the bot may say nothing before it is given
// up on: it edits its message at every step, and while it encodes.
const GIFBotPatience = 2 * time.Minute

// GIFStage is what the conversion of a video to a GIF does now.
type GIFStage uint8

const (
	// GIFStarting is before anything is known: the video forwarded, or
	// FFmpeg reading it.
	GIFStarting GIFStage = iota
	// GIFQueued is the bot converting others' videos first.
	GIFQueued
	// GIFDownloading is the bot downloading the video.
	GIFDownloading
	// GIFProbing is the bot reading what the video is.
	GIFProbing
	// GIFEncoding is the video being encoded, by FFmpeg or by the bot.
	GIFEncoding
	// GIFSending is the GIF on its way: from the bot, or uploaded.
	GIFSending
)

// GIFProgress is how far the conversion of a video to a GIF has come.
type GIFProgress struct {
	Stage GIFStage
	// Share is how much of the encoding is done, from 0 to 1; below 0 when
	// it is not known.
	Share float64
	// Queue is the place in the bot's queue, for GIFQueued.
	Queue int
	// Pass is the encoding, from 1: a GIF too large is encoded again.
	Pass int
}

// GIFMaker is a store that saves a video as a GIF among the saved ones,
// converted here or by GIFBot.
type GIFMaker interface {
	SavedGIFStore
	// SaveGIFFile uploads the file at path, a video without sound of
	// size and duration made of the video of source, as a GIF, and puts it
	// first among the saved ones; nothing is sent.
	SaveGIFFile(ctx context.Context, source Message, path string, width, height int, duration time.Duration) error
	// ConvertGIFByBot forwards the video of m to GIFBot, tells how far it
	// has come as the bot edits its message, and returns the message of
	// the GIF the bot sends.
	ConvertGIFByBot(ctx context.Context, m Message, progress func(GIFProgress)) (Message, error)
	// MadeGIF is the GIF made before of the video of source, saved again
	// rather than made again: Telegram keeps a saved GIF once, by its
	// document, and moves it first when it is saved again.
	MadeGIF(ctx context.Context, source Message) (Message, bool)
}

// GIFBotCancelAnswer reports whether text is GIFBot's answer to
// GIFBotCancel: what it cancelled, or that nothing was.
func GIFBotCancelAnswer(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.HasPrefix(lower, "cancelled") || strings.HasPrefix(lower, "nothing to cancel")
}

// CanMakeGIF reports whether the video of m may be saved as a GIF: not a
// round one, nor one its chat protects.
func CanMakeGIF(m Message) bool {
	return m.Kind == MessageVideo && m.Media != nil && !m.NoForwards
}

var (
	gifBotQueue    = regexp.MustCompile(`(?i)in the queue\. your position: (\d+)`)
	gifBotProgress = regexp.MustCompile(`(?i)progress: ([0-9.]+)%`)
	gifBotFrames   = regexp.MustCompile(`(?i)frames (\d+)/(\d+)`)
)

// ParseGIFBot reads a message of GIFBot's: how far it has come, or what it
// failed with; ok is false for one that tells neither, such as its
// greeting or its note on a large GIF.
func ParseGIFBot(text string) (progress GIFProgress, ok bool, err error) {
	progress.Share = -1
	lower := strings.ToLower(strings.TrimSpace(text))
	switch {
	case gifBotQueue.MatchString(text):
		n, _ := strconv.Atoi(gifBotQueue.FindStringSubmatch(text)[1])
		progress.Stage, progress.Queue = GIFQueued, n
	case strings.HasPrefix(lower, "downloading file"):
		progress.Stage = GIFDownloading
	case strings.HasPrefix(lower, "extracting data"):
		progress.Stage = GIFProbing
	case strings.HasPrefix(lower, "processing"):
		progress.Stage, progress.Pass = GIFEncoding, 1
		// The share of the frames, which the bot's progress tells as well
		// unless it could not count them.
		if m := gifBotProgress.FindStringSubmatch(text); m != nil {
			// More than all, of frames miscounted, is no share.
			if f, e := strconv.ParseFloat(m[1], 64); e == nil && f >= 0 && f <= 100 {
				progress.Share = f / 100
			}
		}
		if m := gifBotFrames.FindStringSubmatch(text); progress.Share < 0 && m != nil {
			done, _ := strconv.Atoi(m[1])
			total, _ := strconv.Atoi(m[2])
			if total > 0 && done <= total {
				progress.Share = float64(done) / float64(total)
			}
		}
	case strings.HasPrefix(lower, "sending a gif"):
		progress.Stage = GIFSending
	case strings.HasPrefix(lower, "file size too large"):
		return progress, true, ErrGIFTooLarge
	case strings.HasPrefix(lower, "you can send no more than"):
		return progress, true, ErrGIFBotBusy
	case strings.HasPrefix(lower, "an unknown error"), strings.HasPrefix(lower, "an error occurred"):
		return progress, true, ErrGIFBotFailed
	case strings.HasPrefix(lower, "cancelled"):
		return progress, true, ErrGIFBotCancelled
	default:
		return progress, false, nil
	}
	return progress, true, nil
}

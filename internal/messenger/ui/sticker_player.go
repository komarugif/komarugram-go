// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"

	"gio-mw/widget/radio"

	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/pkg/video"
)

// decoderSettings are the internal players: one FFmpeg, and for video
// stickers (WebM) and for GIFs and animated avatars (MP4) the choice between
// it and a WASM sandbox; and for voice messages and music, the choice
// between the WASM sandboxes and the external player.
type decoderSettings struct {
	program                     *programSetting
	stickers, animations, audio *decoderChoice
}

// decoderChoice is the player of one kind of media.
type decoderChoice struct {
	height heightTransition
	// title and hint are localization keys; hint may be empty.
	title, hint string
	program     *programSetting
	// options are the values to choose from, "ffmpeg" and "wasm" unless set,
	// and fallback the one that holds when none was chosen and FFmpeg's
	// presence does not decide.
	options  []string
	fallback string
	radios   *radio.Radios[string]
	chosen   func() string
	choose   func(string)
}

func newDecoderSettings() *decoderSettings {
	program := &programSetting{
		title:  "FFmpeg",
		custom: func() string { return "" }, save: func(string) {},
		check: video.CheckFFmpeg,
		found: func(ctx context.Context) (string, string, error) {
			path := video.ResolveFFmpeg("")
			if path == "" {
				return "", "", nil
			}
			about, err := video.CheckFFmpeg(ctx, path)
			return path, about, err
		},
	}
	return &decoderSettings{
		program:    program,
		stickers:   newDecoderChoice(program, "sticker_player.title", ""),
		animations: newDecoderChoice(program, "sticker_player.mp4_title", "sticker_player.mp4_hint"),
		audio:      newDecoderChoice(program, "sticker_player.audio_title", "sticker_player.audio_hint", "external", "wasm"),
	}
}

// newDecoderChoice is a choice among options, FFmpeg and the WASM sandbox
// when none are given.
func newDecoderChoice(program *programSetting, title, hint string, options ...string) *decoderChoice {
	c := &decoderChoice{title: title, hint: hint, program: program, options: options}
	if len(options) == 0 {
		c.options = []string{"ffmpeg", "wasm"}
	} else {
		c.fallback = options[0]
	}
	c.radios = radio.NewRadios(c.options, c.options[len(c.options)-1], func(value string) {
		if c.choose != nil {
			c.choose(value)
		}
	})
	return c
}

func (c *decoderChoice) current() string {
	if c.chosen != nil && c.chosen() != "" {
		return c.chosen()
	}
	if c.fallback != "" {
		return c.fallback
	}
	if video.ResolveFFmpeg(c.program.custom()) != "" {
		return "ffmpeg"
	}
	return "wasm"
}

func (s *decoderSettings) Update(gtx layout.Context) {
	s.program.Update(gtx)
	for _, c := range []*decoderChoice{s.stickers, s.animations, s.audio} {
		c.radios.SetValue(c.current())
		c.radios.Update(gtx)
	}
}

func (c *decoderChoice) Layout(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	labels := make(map[string]string, len(c.options))
	for _, option := range c.options {
		switch option {
		case "ffmpeg":
			labels[option] = "FFmpeg"
		default:
			if option == "external" {
				labels[option] = playerText(l, "sticker_player.external")
			} else {
				labels[option] = l.T("sticker_player." + option)
			}
		}
	}
	hint := ""
	if c.hint != "" {
		hint = l.T(c.hint)
	}
	return settingsChoiceCard(gtx, &c.height, l.T(c.title), hint, func(gtx layout.Context) layout.Dimensions {
		return c.radios.Layout(gtx, radio.LeadingKind, labels)
	})
}

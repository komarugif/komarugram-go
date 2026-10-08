// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"os"

	"gio-mw/token"
	"gio-mw/widget/button"

	"gioui.org/layout"

	"komarugram/internal/messenger/emojipacks"
	"komarugram/internal/messenger/fonts"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/preferences"
)

// offeredEmojiPack is the pack offered where the system has no emoji: the
// one Telegram Desktop draws with, downloaded from its repository, not
// through an account.
const offeredEmojiPack = "apple"

// emojiOffer offers the Apple emoji pack where no font of the system has
// emoji, as on Haiku, which shows squares instead. It asks once: answered
// either way, it does not come back; the packs stay in the settings.
type emojiOffer struct {
	modal           modal
	download, later *button.Button
	// state is how far the offer went.
	state emojiOfferState
	// missing is told, once, whether the system lacks emoji.
	missing chan bool
	pack    emojipacks.Pack
	// systemHasEmoji looks at the system's fonts; tests replace it.
	systemHasEmoji func() bool
}

type emojiOfferState uint8

const (
	emojiOfferIdle     emojiOfferState = iota
	emojiOfferChecking                 // looking at the system's fonts
	emojiOfferCatalog                  // the system lacks emoji: finding the pack
	emojiOfferShown
	emojiOfferDone
)

func newEmojiOffer() *emojiOffer {
	return &emojiOffer{download: button.Filled(), later: button.Text(), missing: make(chan bool, 1), systemHasEmoji: fonts.SystemHasEmoji}
}

// wanted reports whether files leave emoji to the system's fonts, and the
// offer was not answered.
func (o *emojiOffer) wanted(files preferences.Fonts) bool {
	return !files.EmojiOffered && files.EmojiPack == "" && files.Emoji == "" &&
		!fonts.FromEnv(fonts.Emoji) && os.Getenv(fonts.SetEnv) == ""
}

// Update moves the offer on; toast tells what came of an answer.
func (o *emojiOffer) Update(gtx layout.Context, s *emojiSettings, toast func(string), l localization.Catalog, invalidate func()) {
	if o.state == emojiOfferDone || !s.available() {
		return
	}
	files := s.files()
	if !o.wanted(files) {
		// Answered, or chosen, in another window.
		o.state = emojiOfferDone
		o.modal.Close()
		return
	}
	switch o.state {
	case emojiOfferIdle:
		o.state = emojiOfferChecking
		go func() {
			o.missing <- !o.systemHasEmoji()
			invalidate()
		}()
	case emojiOfferChecking:
		select {
		case missing := <-o.missing:
			if !missing {
				o.state = emojiOfferDone
				return
			}
			o.state = emojiOfferCatalog
			s.askCatalog()
		default:
			return
		}
	}
	if o.state == emojiOfferCatalog {
		if s.reading {
			return
		}
		for _, p := range s.catalog {
			if p.ID == offeredEmojiPack && s.offered(p) {
				o.pack = p
			}
		}
		if o.pack.ID == "" {
			// No catalog, or no such pack in it: nothing to offer.
			o.state = emojiOfferDone
			return
		}
		o.state = emojiOfferShown
		o.modal.Open()
	}
	if o.download.Clicked(gtx) {
		o.answer(s, true, toast, l)
	}
	if o.later.Clicked(gtx) {
		o.answer(s, false, toast, l)
	}
}

// answer ends the offer, downloading the pack and choosing it once it is
// downloaded if download is set.
func (o *emojiOffer) answer(s *emojiSettings, download bool, toast func(string), l localization.Catalog) {
	o.state = emojiOfferDone
	o.modal.Close()
	files := s.files()
	files.EmojiOffered = true
	s.setFiles(files)
	if !download {
		return
	}
	// A pack installed before, as the catalog has it, is only chosen.
	if have, ok := s.store.Pack(o.pack.ID); ok && have.Revision() == o.pack.Revision() {
		files.EmojiPack = o.pack.ID
		s.setFiles(files)
		return
	}
	s.useWhenDone = o.pack.ID
	s.done = func(id string, err error) {
		if err != nil {
			toast(l.T("emojipacks.failed") + ": " + mediaErrorText(err))
		}
	}
	s.startDownload(o.pack)
	toast(l.T("emojipacks.offer_started"))
}

func (o *emojiOffer) Layout(gtx layout.Context, s *emojiSettings, l localization.Catalog) {
	if !o.modal.Shown() {
		return
	}
	sc := scheme(gtx)
	body := l.Format("emojipacks.offer_body", map[string]string{"size": sizeText(l, o.pack.DownloadSize())})
	shown := o.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(440))
		return o.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("emojipacks.offer_title"), token.TypestyleTitleMedium, sc.Surface.OnColor, 2)
				}),
				vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, body, token.TypestyleBodyMedium, sc.Surface.OnColor, 0)
				}),
				vspace(16),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Spacing: layout.SpaceStart, Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return o.later.Layout(gtx, l.T("emojipacks.offer_later"))
						}),
						layout.Rigid(layout.Spacer{Width: 8}.Layout),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return o.download.Layout(gtx, l.T("emojipacks.download"))
						}),
					)
				}),
			)
		}, defaultCardPadding)
	})
	if !shown && o.state == emojiOfferShown {
		// Escape or a click beside the dialog answers "not now".
		o.answer(s, false, nil, l)
	}
}

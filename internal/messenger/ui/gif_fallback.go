// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"gio-mw/token"

	"gioui.org/layout"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
	"komarugram/pkg/player"
)

// gifFallback asks where to play a GIF the sandbox cannot, one not in
// H.264 (mp4.ErrUnsupported), when GIFs play in the sandbox: with FFmpeg,
// in the chat but out of the sandbox, or in the external player. It
// offers what there is, and is asked, never chosen for the user: the
// sandbox is chosen to keep strangers' files out of FFmpeg.
type gifFallback struct {
	modal  modal
	msg    model.Message
	ffmpeg bool
	player bool
	// viaFFmpeg, viaPlayer and cancel are the dialog's buttons.
	viaFFmpeg, viaPlayer, cancel surface
}

// gifUnplayable asks where to play m, a GIF the sandbox cannot play, or
// tells that nothing here can.
func (p *chatPage) gifUnplayable(m model.Message, l localization.Catalog) {
	d := &p.gifFallback
	d.msg = m
	d.ffmpeg = p.media.FFmpegFound()
	d.player = len(dedicatedPlayers(player.Installed(p.customPlayers()))) > 0
	if !d.ffmpeg && !d.player {
		p.toast.Show(l.T("gif_fallback.none"))
		return
	}
	d.modal.Open()
	p.invalidate()
}

func (p *chatPage) gifFallbackDialog(gtx layout.Context, l localization.Catalog) {
	d := &p.gifFallback
	if !d.modal.Shown() {
		return
	}
	if !d.modal.closing {
		switch {
		case d.ffmpeg && d.viaFFmpeg.Clicked(gtx):
			d.modal.Close()
			p.media.PlayGIFWithFFmpeg(d.msg)
		case d.player && d.viaPlayer.Clicked(gtx):
			d.modal.Close()
			p.play(gtx, d.msg, p.reportMedia, l)
		case d.cancel.Clicked(gtx):
			d.modal.Close()
		}
	}
	text := l.T("gif_fallback.text")
	switch {
	case d.ffmpeg && d.player:
		text += " " + l.T("gif_fallback.both")
	case d.ffmpeg:
		text += " " + l.T("gif_fallback.ffmpeg_only")
	default:
		text += " " + l.T("gif_fallback.player_only")
	}
	shown := d.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(440))
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return d.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			sc := scheme(gtx)
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("gif_fallback.title"), token.TypestyleTitleLarge, sc.Surface.OnColor, 0)
				}),
				vspace(8),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, text, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
				}),
				vspace(16),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return textButton(gtx, &d.cancel, l.T("gif_fallback.cancel"))
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if !d.player {
								return layout.Dimensions{}
							}
							return textButton(gtx, &d.viaPlayer, l.T("gif_fallback.player"))
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if !d.ffmpeg {
								return layout.Dimensions{}
							}
							return textButton(gtx, &d.viaFFmpeg, l.T("gif_fallback.ffmpeg"))
						}),
					)
				}),
			)
		}, defaultCardPadding)
	})
	if !shown {
		d.msg = model.Message{}
	}
}

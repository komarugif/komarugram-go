// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"path/filepath"
	"strings"
	"time"

	"gio-mw/token"

	"gioui.org/io/event"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// Saving an album: the menu opened on one of its parts asks whether to
// save that part or the whole album; opened beside them, it saves the
// whole album. Its photos go into the user's pictures, the rest into the
// downloads, as each alone would.

// albumPart is the part of an album pressed with the right button last.
type albumPart struct {
	album, id model.MessageID
	at        time.Duration
}

// partPresses notes the right presses on member, a part of album, laid out
// in child.
func (p *chatPage) partPresses(gtx layout.Context, album model.Message, child *messageRow, member model.Message) {
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &child.partPress, Kinds: pointer.Press})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok && e.Buttons == pointer.ButtonSecondary {
			p.messageMenu.part = albumPart{album: album.Key.MessageID, id: member.Key.MessageID, at: e.Time}
		}
	}
}

// partPressArea takes the presses on a part of an album, of size, laid out
// in child, over it, passing them on.
func partPressArea(gtx layout.Context, child *messageRow, size image.Point) {
	defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, &child.partPress)
}

// menuPart is the part of album m the menu was opened on, if it was opened
// on one.
func (p *chatPage) menuPart(m model.Message) (model.Message, bool) {
	menu := &p.messageMenu
	if len(m.Attachments) < 2 || menu.part.album != m.Key.MessageID || menu.part.at != menu.pressed {
		return model.Message{}, false
	}
	for _, part := range m.Attachments {
		if part.Key.MessageID == menu.part.id {
			return part, p.saveWayOf(part) != saveNone
		}
	}
	return model.Message{}, false
}

// canSave reports whether the menu of m offers to save its file, or the
// files of its album.
func (p *chatPage) canSave(m model.Message) bool {
	if len(m.Attachments) < 2 {
		return p.saveWayOf(m) != saveNone
	}
	for _, part := range m.Attachments {
		if p.albumWayOf(part) != saveNone {
			return true
		}
	}
	return false
}

// albumWayOf is how part is saved with its album: as alone, but into the
// downloads where it would be saved where the user chooses, which the
// system's dialog would ask for each part.
func (p *chatPage) albumWayOf(part model.Message) saveWay {
	switch way := p.saveWayOf(part); way {
	case saveAs:
		if _, ok := p.source.(model.Downloader); ok {
			return saveToDownloads
		}
		return saveNone
	default:
		return way
	}
}

// saveAlbum saves the files of parts in the background, and tells how
// many and where.
func (p *chatPage) saveAlbum(parts []model.Message, l localization.Catalog) {
	ways := make([]saveWay, len(parts))
	for i, part := range parts {
		ways[i] = p.albumWayOf(part)
	}
	source := p.source
	go func() {
		defer crash.Recover("save album", func(e *crash.Panic) { p.reportMedia(e) })
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		var dirs []string
		saved := 0
		var failed error
		for i, part := range parts {
			var path string
			var err error
			switch ways[i] {
			case saveNone:
				continue
			case saveToPictures:
				var data []byte
				if data, err = source.Media(ctx, part); err == nil {
					path, err = savePhoto(data, part)
				}
			case saveToDownloads:
				path, err = source.(model.Downloader).Download(ctx, part, appDownloadsDir(), nil)
			}
			if err != nil {
				if failed == nil {
					failed = err
				}
				continue
			}
			saved++
			if dir := filepath.Dir(path); !containsString(dirs, dir) {
				dirs = append(dirs, dir)
			}
		}
		if failed != nil {
			p.reportMedia(failed)
		}
		if saved > 0 {
			p.reportNotice(l.Count("menu.album_saved", saved, map[string]string{"path": strings.Join(dirs, ", ")}))
		}
	}()
}

func containsString(list []string, s string) bool {
	for _, one := range list {
		if one == s {
			return true
		}
	}
	return false
}

// albumSaveDialog asks whether to save the part of an album the menu was
// opened on, or the whole album.
type albumSaveDialog struct {
	modal            modal
	album, part      model.Message
	one, all, cancel surface
}

func (d *albumSaveDialog) open(album, part model.Message) {
	*d = albumSaveDialog{album: album, part: part}
	d.modal.Open()
}

func (d *albumSaveDialog) layout(gtx layout.Context, p *chatPage, l localization.Catalog) {
	if !d.modal.Shown() {
		return
	}
	if !d.modal.closing {
		switch {
		case d.one.Clicked(gtx):
			d.modal.Close()
			p.saveMessage(d.part, p.saveWayOf(d.part), l)
		case d.all.Clicked(gtx):
			d.modal.Close()
			p.saveAlbum(d.album.Attachments, l)
		case d.cancel.Clicked(gtx):
			d.modal.Close()
		}
	}
	shown := d.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(400))
		gtx.Constraints.Min.X = gtx.Constraints.Max.X
		return d.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			sc := scheme(gtx)
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("album_save.title"), token.TypestyleTitleLarge, sc.Surface.OnColor, 1)
				}),
				vspace(8),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					text := l.Count("album_save.text", len(d.album.Attachments), nil)
					return label(gtx, text, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
				}),
				vspace(16),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints.Min.X = gtx.Constraints.Max.X
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							return textButton(gtx, &d.cancel, l.T("album_save.cancel"))
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &d.one, l.T("album_save.one")) }),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &d.all, l.T("album_save.all")) }),
					)
				}),
			)
		}, defaultCardPadding)
	})
	if !shown {
		*d = albumSaveDialog{}
	}
}

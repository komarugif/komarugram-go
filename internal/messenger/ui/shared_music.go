// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"slices"
	"strconv"
	"time"

	"gioui.org/layout"
	"gioui.org/op"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// musicResult is what a change of the account's playlist brought: done is
// told when it went, err when it did not.
type musicResult struct {
	err  error
	done string
}

// changeMusic sends a change of the account's playlist, already shown, to
// the store in the background: drainMusic takes what it brought.
func (p *chatInfo) changeMusic(change func(context.Context, model.SavedMusicStore) error, done string) {
	store, ok := p.source.(model.SavedMusicStore)
	if !ok {
		return
	}
	results, invalidate := p.musicResults, p.invalidate
	send := func(r musicResult) {
		select {
		case results <- r:
		default:
		}
		invalidate()
	}
	go func() {
		defer crash.Recover("playlist", func(e *crash.Panic) { send(musicResult{err: e}) })
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		send(musicResult{err: change(ctx, store), done: done})
	}()
}

// drainMusic takes what the changes of the playlist brought: a failure
// tells so and shows the playlist as Telegram has it; the profile's song,
// the first of it, follows what went.
func (p *chatInfo) drainMusic() {
	for {
		select {
		case r := <-p.musicResults:
			if r.err != nil {
				p.modal.Toast(mediaErrorText(r.err))
				if p.section == model.SharedSavedMusic {
					p.selectSection(model.SharedSavedMusic)
				}
				continue
			}
			if r.done != "" {
				p.modal.Toast(r.done)
			}
			p.loadProfile()
		default:
			return
		}
	}
}

// removeSong takes song off the account's playlist, as Telegram Desktop's
// Delete in it does: at once, and told.
func (p *chatInfo) removeSong(song model.Message, l localization.Catalog) {
	i := slices.IndexFunc(p.messages, func(m model.Message) bool { return m.Key == song.Key })
	if i < 0 {
		return
	}
	p.messages = slices.Delete(p.messages, i, i+1)
	if n, ok := p.counts[model.SharedSavedMusic]; ok && n > 0 {
		p.counts[model.SharedSavedMusic] = n - 1
	}
	p.changeMusic(func(ctx context.Context, store model.SavedMusicStore) error {
		return store.RemoveMusic(ctx, song)
	}, l.T("menu.music_removed"))
}

// moveSong puts the song at place from of the account's playlist at place
// to, as Telegram Desktop's reorder does: after the song before it there.
func (p *chatInfo) moveSong(from, to int) {
	if from == to || from < 0 || to < 0 || from >= len(p.messages) || to >= len(p.messages) {
		return
	}
	song := p.messages[from]
	p.messages = slices.Insert(slices.Delete(p.messages, from, from+1), to, song)
	var after *model.Message
	if to > 0 {
		prev := p.messages[to-1]
		after = &prev
	}
	p.changeMusic(func(ctx context.Context, store model.SavedMusicStore) error {
		return store.MoveMusic(ctx, song, after)
	}, "")
}

// songHandleWidth is how much of the end of a song of the account's
// playlist takes the press that drags it: the handle in its bubble, and
// the bubble's edge.
const songHandleWidth = 64

// ownPlaylist tells whether the section shown is the account's own
// playlist, whose songs it reorders.
func (p *chatInfo) ownPlaylist() bool {
	_, ok := p.source.(model.SavedMusicStore)
	return ok && p.own != nil && p.section == model.SharedSavedMusic && !p.themePage
}

// songID tells a song of the playlist apart for the drag.
func songID(m model.Message) string {
	return strconv.Itoa(int(m.Key.MessageID))
}

// layoutOwnPlaylist draws the account's playlist: its songs in one block,
// each with a handle at its right that drags it to another place, as the
// settings' menus reorder their items; then what loads more, or tells.
func (p *chatInfo) layoutOwnPlaylist(gtx layout.Context, l localization.Catalog, animate bool) layout.Dimensions {
	ids := make([]string, len(p.messages))
	for i, m := range p.messages {
		ids[i] = songID(m)
	}
	taken := p.songs.dragging
	if order, ok := p.songs.update(gtx, ids, max(1, p.songHeight)); ok {
		p.moveSong(slices.Index(ids, taken), slices.Index(order, taken))
		for i, m := range p.messages {
			ids[i] = songID(m)
		}
	}
	shown := p.songs.shown(ids)
	byID := make(map[string]model.Message, len(p.messages))
	for _, m := range p.messages {
		byID[songID(m)] = m
	}
	// The handle is in the bubble; the bubble taken is of another color.
	r := p.renderer
	r.songHandle = func(gtx layout.Context, _ model.Message) layout.Dimensions {
		size := image.Pt(gtx.Dp(32), gtx.Dp(32))
		return exact(gtx, size, func(gtx layout.Context) layout.Dimensions {
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				return exact(gtx, image.Pt(gtx.Dp(20), gtx.Dp(20)), func(gtx layout.Context) layout.Dimensions {
					return iconMenuDrag(gtx, scheme(gtx).SurfaceVariant.OnColor)
				})
			})
		})
	}
	defer func() { r.songHandle, r.lifted = nil, model.MessageKey{} }()
	dims := p.list.Layout(gtx, 2, func(gtx layout.Context, i int) layout.Dimensions {
		if i == 1 {
			return p.inColumn(gtx, func(gtx layout.Context) layout.Dimensions {
				return p.layoutMediaRow(gtx, l, animate, false, 1, 0, 0)
			})
		}
		return p.inColumn(gtx, func(gtx layout.Context) layout.Dimensions {
			return p.layoutSongs(gtx, l, animate, shown, byID)
		})
	})
	if p.more && !p.loading && p.problem == nil && p.list.Position.First+p.list.Position.Count >= 2 {
		p.load(false)
	}
	return dims
}

// layoutSongs draws the songs of shown, by their IDs, one under another
// where the drag puts them, all as high as the highest.
func (p *chatInfo) layoutSongs(gtx layout.Context, l localization.Catalog, animate bool, shown []string, byID map[string]model.Message) layout.Dimensions {
	width := gtx.Constraints.Max.X
	type drawn struct {
		call op.CallOp
		size image.Point
	}
	rows := make(map[string]drawn, len(shown))
	height := 0
	for _, id := range shown {
		m := byID[id]
		row := gtx
		row.Constraints = layout.Constraints{Max: image.Pt(width, gtx.Constraints.Max.Y)}
		p.renderer.lifted = model.MessageKey{}
		if p.songs.dragging == id {
			p.renderer.lifted = m.Key
		}
		macro := op.Record(gtx.Ops)
		dims := p.renderer.row(row, m, false, 0, l, animate)
		p.cellInput(row, m, dims.Size)
		rows[id] = drawn{macro.Stop(), dims.Size}
		height = max(height, dims.Size.Y)
	}
	p.songHeight = height
	p.songs.top = 0
	for _, id := range p.songs.drawOrder(shown) {
		r, ok := rows[id]
		if !ok {
			continue
		}
		y := p.songs.rowY(gtx, id, slices.Index(shown, id), height)
		offset(gtx, image.Pt(0, y+(height-r.size.Y)/2), func(gtx layout.Context) layout.Dimensions {
			r.call.Add(gtx.Ops)
			return layout.Dimensions{Size: r.size}
		})
		// The handle the bubble draws is at its end; the press is taken
		// here, in the rows' own place, which the drag counts from.
		p.songs.handle(gtx, image.Rect(width-gtx.Dp(songHandleWidth), y, width, y+height))
	}
	return layout.Dimensions{Size: image.Pt(width, len(shown)*height)}
}

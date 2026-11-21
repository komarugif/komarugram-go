// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"time"

	"gio-mw/token"
	"gio-mw/widget/scroll"
	"gioui.org/layout"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// chatFolderChoice belongs to the row which opened it, including when that
// chat is different from the conversation currently open.
type chatFolderChoice struct {
	modal   modal
	action  chatRowAction
	chat    model.Chat
	folders []model.Folder
	list    scroll.List
	rows    map[int64]*surface
	cancel  surface
}

func (p *chatFolderChoice) open(chat model.Chat, action chatRowAction, folders []model.Folder) {
	p.chat, p.action = chat, action
	p.list = scroll.List{List: layout.List{Axis: layout.Vertical}}
	p.rows = make(map[int64]*surface)
	p.refresh(folders)
	if len(p.folders) > 0 {
		p.modal.Open()
	}
}

func (p *chatFolderChoice) refresh(folders []model.Folder) {
	p.folders = p.folders[:0]
	for _, f := range folders {
		if f.ID == 0 {
			continue
		}
		if p.action == chatRowAddFolder && !f.Contains(p.chat) || p.action == chatRowRemoveFolder && f.CanRemoveChat(p.chat) {
			p.folders = append(p.folders, f)
		}
	}
}

func (p *chatFolderChoice) update(gtx layout.Context, m *chatRowMenu, chats []model.Chat, t *toast, l localization.Catalog) {
	if p.modal.closing {
		return
	}
	c, ok := chatByID(chats, p.chat.ID)
	if !ok || p.cancel.Clicked(gtx) {
		p.modal.Close()
		return
	}
	p.chat = c
	// Resolve clicks against the IDs and definitions that were actually shown.
	for _, f := range p.folders {
		if row := p.rows[f.ID]; row != nil && row.Clicked(gtx) {
			change := model.FolderAddChat
			if p.action == chatRowRemoveFolder {
				change = model.FolderRemoveChat
			}
			m.changeFolder(f, c, change, t, l)
			p.modal.Close()
			return
		}
	}
	p.refresh(m.folderStore.Folders())
	if len(p.folders) == 0 {
		p.modal.Close()
	}
}

func (p *chatFolderChoice) layout(gtx layout.Context, l localization.Catalog) {
	if !p.modal.Shown() {
		return
	}
	shown := p.modal.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		width := min(gtx.Constraints.Max.X, gtx.Dp(400))
		height := min(gtx.Constraints.Max.Y, gtx.Dp(144)+len(p.folders)*gtx.Dp(44), gtx.Dp(544))
		gtx.Constraints = layout.Exact(image.Pt(width, height))
		return p.modal.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = gtx.Constraints.Max
			sc := scheme(gtx)
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, chatRowLabel(p.action, l), token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.ChatTitle(p.chat), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 1)
				}),
				vspace(8), layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return p.list.Layout(gtx, len(p.folders), func(gtx layout.Context, i int) layout.Dimensions {
						f := p.folders[i]
						row := p.rows[f.ID]
						if row == nil {
							row = new(surface)
							p.rows[f.ID] = row
						}
						gtx.Constraints = layout.Exact(image.Pt(gtx.Constraints.Max.X, gtx.Dp(44)))
						return menuRow(gtx, row, folderIcon(f), f.Title, false)
					})
				}), vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &p.cancel, l.T("account.cancel")) })
				}))
		}, defaultCardPadding)
	})
	if !shown {
		p.clear()
	}
}

func (p *chatFolderChoice) clear() {
	p.modal.Hide()
	p.chat = model.Chat{}
	p.folders = nil
	p.rows = nil
}

func (m *chatRowMenu) close() {
	m.open = false
	m.folderChoice.clear()
}

func (m *chatRowMenu) changeFolder(base model.Folder, c model.Chat, change model.FolderChatAction, t *toast, l localization.Catalog) {
	if m.folderSave != nil {
		return
	}
	draft, err := base.WithChat(c, change)
	if err != nil {
		t.Show(l.T(folderChangeError(err)))
		return
	}
	done := make(chan error, 1)
	m.folderSave = done
	m.folderNotice = ""
	key := ""
	switch change {
	case model.FolderAddChat:
		key = "chat_row.folder_added"
	case model.FolderRemoveChat:
		key = "chat_row.folder_removed"
	}
	if key != "" {
		m.folderNotice = l.Format(key, map[string]string{"chat": l.ChatTitle(c), "folder": base.Title})
	}
	store := m.folderStore
	go func() {
		defer crash.Recover("chat folder action", func(e *crash.Panic) {
			done <- e
			if m.invalidate != nil {
				m.invalidate()
			}
		})
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		done <- store.SaveFolder(ctx, &base, draft)
		if m.invalidate != nil {
			m.invalidate()
		}
	}()
}

func folderChangeError(err error) string {
	switch {
	case errors.Is(err, model.ErrFolderCache):
		return "folders.cache_failed"
	case errors.Is(err, model.ErrFolderPeerLimit):
		return "folders.peer_limit"
	case errors.Is(err, model.ErrFolderDefault):
		return "folders.default"
	case errors.Is(err, model.ErrFolderConflict):
		return "folders.action_changed"
	case errors.Is(err, model.ErrFolderTypes):
		return "folders.types_required"
	default:
		return "folders.action_failed"
	}
}

func (m *chatRowMenu) folderResult(t *toast, l localization.Catalog) {
	select {
	case err := <-m.folderSave:
		m.folderSave = nil
		if err != nil {
			t.Show(l.T(folderChangeError(err)))
		} else if m.folderNotice != "" {
			t.Show(m.folderNotice)
		}
		m.folderNotice = ""
	default:
	}
}

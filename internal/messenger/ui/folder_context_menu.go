// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"slices"
	"time"

	"gio-mw/token"
	"gioui.org/io/event"
	"gioui.org/io/key"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

type folderAction uint8

const (
	folderRead folderAction = iota + 1
	folderRemove
)

// Both navigation surfaces attach the same folder ID to their right-click
// target. The window supplies the position, independent of list scrolling.
type folderContextMenu struct {
	store                model.FolderActions
	hits                 map[int64]*byte
	area, dismiss, panel byte
	menu                 contextMenu
	open, focus          bool
	at                   image.Point
	folder               model.Folder
	items                []infoMenuItem
	edit, read, remove   surface
	confirm              modal
	cancel, accept       surface
	confirmAction        folderAction
	// Immutable snapshot taken when the user chose Read.
	unread  []model.FolderReadChat
	pending chan error
	stop    context.CancelFunc
}

func (m *folderContextMenu) hit(gtx layout.Context, id int64, size image.Point) {
	if m == nil {
		return
	}
	if m.hits == nil {
		m.hits = make(map[int64]*byte)
	}
	tag := m.hits[id]
	if tag == nil {
		tag = new(byte)
		m.hits[id] = tag
	}
	defer pointer.PassOp{}.Push(gtx.Ops).Pop()
	defer clip.Rect{Max: size}.Push(gtx.Ops).Pop()
	event.Op(gtx.Ops, tag)
}

func (m *folderContextMenu) update(gtx layout.Context, a *App, folders []model.Folder) {
	if m.store == nil {
		return
	}
	l := a.catalog()
	select {
	case err := <-m.pending:
		m.stop = nil
		m.pending = nil
		if err != nil {
			msg := "folders.action_failed"
			if errors.Is(err, model.ErrFolderConflict) {
				msg = "folders.action_changed"
			} else if errors.Is(err, model.ErrFolderCache) {
				msg = "folders.cache_failed"
			}
			a.toast.Show(l.T(msg))
		}
	default:
	}
	if m.confirm.Shown() {
		if !m.confirm.closing {
			if m.cancel.Clicked(gtx) {
				m.confirm.Close()
			} else if m.accept.Clicked(gtx) {
				m.confirm.Close()
				if slices.ContainsFunc(folders, func(f model.Folder) bool { return f.SameRules(m.folder) && f.HasMyInvites == m.folder.HasMyInvites }) {
					m.run(a, m.confirmAction, m.unread)
				} else {
					a.toast.Show(l.T("folders.action_changed"))
				}
			}
		}
		return
	}
	// Record only secondary presses: a later primary press cannot retarget the menu.
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &m.area, Kinds: pointer.Press})
		if !ok {
			break
		}
		if e, ok := ev.(pointer.Event); ok && e.Buttons == pointer.ButtonSecondary {
			m.at = e.Position.Round()
		}
	}
	for _, f := range folders {
		tag := m.hits[f.ID]
		if tag == nil {
			continue
		}
		for {
			ev, ok := gtx.Event(pointer.Filter{Target: tag, Kinds: pointer.Press})
			if !ok {
				break
			}
			e, ok := ev.(pointer.Event)
			if ok && e.Source == pointer.Mouse && e.Buttons == pointer.ButtonSecondary && m.pending == nil {
				m.show(f, l)
				gtx.Execute(op.InvalidateCmd{})
			}
		}
	}
	if !m.open {
		return
	}
	if !slices.ContainsFunc(folders, func(f model.Folder) bool { return f.ID == m.folder.ID }) {
		m.open = false
		return
	}
	for {
		ev, ok := gtx.Event(pointer.Filter{Target: &m.dismiss, Kinds: pointer.Press}, key.FocusFilter{Target: &m.dismiss}, key.Filter{Focus: &m.dismiss, Name: key.NameEscape})
		if !ok {
			break
		}
		switch e := ev.(type) {
		case pointer.Event:
			m.open = false
		case key.Event:
			if e.State == key.Press {
				m.open = false
			}
		}
	}
	if !m.open {
		return
	}
	switch {
	case m.edit.Clicked(gtx):
		m.open = false
		a.editNavigationFolder(gtx, m.folder.ID)
	case m.remove.Clicked(gtx) && m.folder.ID != 0:
		m.open = false
		m.confirmAction, m.unread = folderRemove, nil
		m.confirm.Open()
	case m.read.Clicked(gtx):
		m.open = false
		targets := m.store.UnreadInFolder(m.folder.ID)
		if len(targets) == 0 {
			return
		}
		total := 0
		for _, c := range targets {
			total += c.Unread
		}
		if total > 1000 {
			m.confirmAction, m.unread = folderRead, targets
			m.confirm.Open()
		} else {
			m.run(a, folderRead, targets)
		}
	}
}

func (m *folderContextMenu) show(f model.Folder, l localization.Catalog) {
	m.folder, m.open, m.focus = f, true, true
	m.menu = contextMenu{}
	edit := l.T("folders.context_edit")
	if f.ID == 0 {
		edit = l.T("folders.title")
	}
	m.items = []infoMenuItem{{item: &m.edit, icon: iconEdit, text: edit}}
	if len(m.store.UnreadInFolder(f.ID)) > 0 {
		m.items = append(m.items, infoMenuItem{item: &m.read, icon: iconRead, text: l.T("folders.context_read")})
	}
	if f.ID != 0 {
		m.items = append(m.items, infoMenuItem{item: &m.remove, icon: iconDelete, text: l.T("menu.delete")})
	}
}

func (a *App) editNavigationFolder(gtx layout.Context, id int64) {
	p := a.settings
	if a.section.kind == sectionSettings && p.section == settingsFolders && p.folders.draft {
		if p.folders.base != nil && p.folders.base.ID == id {
			return
		}
		p.folders.leaveDraft(gtx, a.catalog(), func(next layout.Context) { a.editNavigationFolder(next, id) })
		return
	}
	folders := a.store.Folders()
	i := slices.IndexFunc(folders, func(f model.Folder) bool { return f.ID == id })
	if i < 0 {
		return
	}
	a.openSection(gtx, section{kind: sectionSettings})
	p.show(settingsFolders)
	if id != 0 {
		f := folders[i]
		p.folders.begin(gtx, p, &f, f)
	}
}

func (m *folderContextMenu) run(a *App, action folderAction, unread []model.FolderReadChat) {
	if m.pending != nil {
		return
	}
	done := make(chan error, 1)
	m.pending = done
	folder := m.folder
	ctx, cancel := context.WithCancel(context.Background())
	m.stop = cancel
	go func() {
		defer cancel()
		defer crash.Recover("folder action", func(e *crash.Panic) { done <- e; a.window.Invalidate() })

		var err error
		switch action {
		case folderRead:
			err = m.store.ReadFolderChats(ctx, unread)
		case folderRemove:
			deleteCtx, stop := context.WithTimeout(ctx, time.Minute)
			defer stop()
			err = m.store.DeleteFolder(deleteCtx, folder)
		}
		done <- err
		a.window.Invalidate()
	}()
}

func (m *folderContextMenu) layout(gtx layout.Context, bd *blurBackdrop, l localization.Catalog) {
	if m.store == nil {
		return
	}
	func() {
		defer pointer.PassOp{}.Push(gtx.Ops).Pop()
		defer clip.Rect{Max: gtx.Constraints.Max}.Push(gtx.Ops).Pop()
		event.Op(gtx.Ops, &m.area)
	}()
	if len(m.items) > 0 {
		if m.focus {
			gtx.Execute(key.FocusCmd{Tag: &m.dismiss})
			m.focus = false
		}
		layoutActionMenu(gtx, &m.menu, m.open, m.at, &m.dismiss, &m.panel, m.items, nil, 0, bd)
		if !m.open && m.menu.bounds.Empty() {
			m.items = nil
		}
	}
	if !m.confirm.Shown() {
		m.unread = nil
		if !m.open && m.items == nil {
			m.folder = model.Folder{}
		}
		return
	}
	title := m.folder.Title
	if m.folder.ID == 0 {
		title = l.T("nav.all")
	}
	msg, action := "folders.remove_sure", "menu.delete"
	if m.folder.HasMyInvites {
		msg = "folders.remove_invites_sure"
	}
	if m.confirmAction == folderRead {
		msg, action = "folders.read_sure", "folders.context_read"
	}
	m.confirm.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(400))
		return m.confirm.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return label(gtx, l.Format(msg, map[string]string{"folder": title}), token.TypestyleBodyLarge, scheme(gtx).Surface.OnColor, 0)
			}), vspace(16), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return folderDialogActions(gtx, &m.cancel, l.T("account.cancel"), &m.accept, l.T(action))
			}))
		}, defaultCardPadding)
	})
}

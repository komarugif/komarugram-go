// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"image"
	"slices"
	"strconv"
	"time"

	"gio-mw/token"
	"gio-mw/widget/checkbox"
	"gioui.org/gesture"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

var folderTypeKeys = []string{"folders.contacts", "folders.noncontacts", "folders.groups", "folders.channels", "folders.bots"}

type folderSettings struct {
	store   model.FolderManager
	dialogs func() []model.Chat
	avatar  avatarLayout

	// Folder list and drag state.
	reorder            reorderRows
	shown, savingOrder []int64
	movable            []string
	byKey              map[string]model.Folder
	fixedAll           bool
	edits              map[int64]*folderSettingsRow

	// Editor and its dialogs.
	create, save                           surface
	name                                   *textField
	types                                  *checkbox.Checkboxes[string]
	autoName                               string
	customName                             bool
	base                                   *model.Folder
	initial, working                       model.Folder
	excludeTypes                           *checkbox.Checkboxes[string]
	peers                                  folderPeerPicker
	includePeers, excludePeers, chooseIcon surface
	iconDialog, saveDialog                 modal
	iconButtons                            map[string]*surface
	stay, discard                          surface
	afterLeave                             func(layout.Context)
	draft, invalidName, invalidTypes       bool
	pending                                chan error

	recommended       surface
	templates         []model.Folder
	templateButtons   []surface
	suggestions       chan folderSuggestions
	loadedSuggestions bool
}

func newFolderSettings(store model.FolderManager) *folderSettings {
	return &folderSettings{
		store:        store,
		name:         newTextField(0, ""),
		edits:        make(map[int64]*folderSettingsRow),
		iconButtons:  make(map[string]*surface),
		excludeTypes: checkbox.NewCheckboxes(folderExcludeKeys, nil, nil),
		types:        checkbox.NewCheckboxes(folderTypeKeys, nil, nil),
	}
}

func (v *folderSettings) run(invalidate func(), action func(context.Context) error) {
	done := make(chan error, 1)
	v.pending = done
	go func() {
		defer crash.Recover("folder settings", func(e *crash.Panic) { done <- e; invalidate() })
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		done <- action(ctx)
		invalidate()
	}()
}

func (v *folderSettings) update(gtx layout.Context, p *settingsPage, l localization.Catalog) {
	if v.pending != nil {
		select {
		case err := <-v.pending:
			v.pending = nil
			v.savingOrder = nil
			if err == nil {
				v.finishDraft(gtx)
			} else {
				key := "folders.failed"
				switch {
				case errors.Is(err, model.ErrFolderCache):
					v.finishDraft(gtx)
					key = "folders.cache_failed"
				case errors.Is(err, model.ErrFolderConflict):
					if v.base == nil {
						v.working.ID = model.NewFolderID(v.store.Folders())
						v.initial.ID = v.working.ID
						key = "folders.conflict"
					} else {
						key = "folders.edit_conflict"
					}
				case errors.Is(err, model.ErrFolderPeerLimit):
					key = "folders.peer_limit"
				case errors.Is(err, model.ErrFolderDefault):
					key = "folders.default"
				case errors.Is(err, model.ErrFolderLimit):
					key = "folders.limit"
				case errors.Is(err, model.ErrFolderOrder):
					key = "folders.changed"
				}
				v.afterLeave = nil
				p.toast.Show(l.T(key))
			}
		default:
			return
		}
	}
	if v.draft {
		v.updateDraft(gtx, p, l)
		return
	}
	v.updateSuggestions(gtx, p, l)
	if v.create.Clicked(gtx) {
		v.begin(gtx, p, nil, model.Folder{})
	}
	for _, f := range v.store.Folders() {
		if edit := v.edits[f.ID]; edit != nil && edit.Clicked(gtx) && f.ID != 0 {
			v.begin(gtx, p, &f, f)
			break
		}
	}
	if v.draft {
		v.updateDraft(gtx, p, l)
		return
	}
	if order, ok := v.reorder.update(gtx, v.movable, gtx.Dp(menuSettingsRowHeight)); ok && !slices.Equal(order, v.movable) {
		previous := slices.Clone(v.shown)
		var next []int64
		if v.fixedAll {
			next = append(next, 0)
		}
		for _, key := range order {
			next = append(next, v.byKey[key].ID)
		}
		v.savingOrder = next
		v.run(p.invalidate, func(ctx context.Context) error { return v.store.ReorderFolders(ctx, previous, next) })
	}
}

func (v *folderSettings) updateDraft(gtx layout.Context, p *settingsPage, l localization.Catalog) {
	if v.saveDialog.Shown() {
		if v.saveDialog.closing {
			return
		}
		switch {
		case v.stay.Clicked(gtx):
			v.afterLeave = nil
			v.saveDialog.Close()
		case v.discard.Clicked(gtx):
			v.finishDraft(gtx)
		case v.save.Clicked(gtx):
			v.saveDialog.Hide()
			if v.peers.modal.Shown() {
				if !v.peers.modal.closing {
					v.peers.applyTo(&v.working)
				}
				v.peers.modal.Hide()
				v.peers.release()
			}
			v.iconDialog.Hide()
			f := v.definition()
			v.invalidName = !model.ValidFolderTitle(f.Title)
			v.invalidTypes = f.FolderTypes == (model.FolderTypes{}) && len(f.Included)+len(f.Pinned) == 0
			if v.invalidName || v.invalidTypes {
				v.afterLeave = nil
				p.list.Position = layout.Position{}
				return
			}
			base := v.base
			v.run(p.invalidate, func(ctx context.Context) error { return v.store.SaveFolder(ctx, base, f) })
		}
		return
	}
	if v.peers.modal.Shown() || v.iconDialog.Shown() {
		return
	}
	v.updateFields(gtx, l)
	if v.includePeers.Clicked(gtx) {
		v.openPeers(true, l)
		return
	}
	if v.excludePeers.Clicked(gtx) && !v.working.Shared {
		v.openPeers(false, l)
		return
	}
	if v.chooseIcon.Clicked(gtx) {
		v.iconDialog.Open()
		return
	}

}

func (v *folderSettings) updateFields(gtx layout.Context, l localization.Catalog) {
	v.name.Submitted(gtx)
	v.excludeTypes.Update(gtx)
	if v.name.Text() != v.autoName {
		v.customName = true
	}
	v.types.Update(gtx)
	selected := v.types.GetValues()
	if !v.customName {
		suggestion := ""
		if len(selected) == 1 {
			suggestion = model.FitFolderTitle(l.T(selected[0]))
		}
		if suggestion != v.autoName {
			v.autoName = suggestion
			v.name.editor.SetText(suggestion)
		}
	}
	if len(selected)+len(v.working.Pinned)+len(v.working.Included) > 0 {
		v.invalidTypes = false
	}
	if v.invalidName && model.ValidFolderTitle(v.name.Text()) {
		v.invalidName = false
	}
}

func (v *folderSettings) layout(gtx layout.Context, l localization.Catalog, premium bool) layout.Dimensions {
	if v.pending != nil {
		gtx = gtx.Disabled()
	}
	if v.draft {
		return v.layoutDraft(gtx, l)
	}
	return card(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.layoutList(gtx, l, premium) }),
			vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return tonalButton(gtx, &v.create, l.T("folders.create"), 0)
				})
			}), layout.Rigid(func(gtx layout.Context) layout.Dimensions { return v.layoutSuggestions(gtx, l) }))
	}, defaultCardPadding)
}

func (v *folderSettings) layoutList(gtx layout.Context, l localization.Catalog, premium bool) layout.Dimensions {
	folders := v.store.Folders()
	ids := model.FolderIDs(folders)
	fixedAll := !premium
	if !slices.Equal(ids, v.shown) || fixedAll != v.fixedAll {
		v.reorder.cancel()
		v.savingOrder = nil
	}
	v.fixedAll = fixedAll
	v.shown = ids
	v.movable = v.movable[:0]
	if len(folders) <= 1 {
		return label(gtx, l.T("folders.empty"), token.TypestyleBodyMedium, scheme(gtx).SurfaceVariant.OnColor, 3)
	}
	// Keep the dropped order visible while Telegram acknowledges it.
	if v.savingOrder != nil {
		ids = v.savingOrder
	}
	if v.byKey == nil {
		v.byKey = make(map[string]model.Folder, len(folders))
	} else {
		clear(v.byKey)
	}
	for _, f := range folders {
		v.byKey[strconv.FormatInt(f.ID, 10)] = f
	}
	for _, id := range ids {
		if id != 0 || premium {
			v.movable = append(v.movable, strconv.FormatInt(id, 10))
		}
	}
	width, height := gtx.Constraints.Max.X, gtx.Dp(menuSettingsRowHeight)
	v.reorder.top = 0
	if v.fixedAll {
		v.layoutRow(gtx, 0, width, height, v.byKey["0"], false, l)
		v.reorder.top = height
	}
	shown := v.reorder.shown(v.movable)
	for _, key := range v.reorder.drawOrder(shown) {
		y := v.reorder.top + v.reorder.rowY(gtx, key, slices.Index(shown, key), height)
		if v.reorder.dragging == key {
			offset(gtx, image.Pt(0, y), func(gtx layout.Context) layout.Dimensions {
				fillRounded(gtx, scheme(gtx).SecondaryContainer.Color, image.Pt(width, height), gtx.Dp(8))
				return layout.Dimensions{}
			})
		}
		v.layoutRow(gtx, y, width, height, v.byKey[key], len(shown) > 1, l)
	}
	return layout.Dimensions{Size: image.Pt(width, len(folders)*height)}
}

type folderSettingsRow struct {
	surface
	handleHover gesture.Hover
}

func (v *folderSettings) layoutRow(gtx layout.Context, y, width, height int, f model.Folder, movable bool, l localization.Catalog) {
	sc := scheme(gtx)
	handleWidth := gtx.Dp(40)
	title, icon := f.Title, folderIcon(f)
	if f.ID == 0 {
		title, icon = l.T("nav.all"), iconAllChats
	}
	rect := image.Rect(0, y, width, y+height)
	row := v.edits[f.ID]
	if row == nil {
		row = new(folderSettingsRow)
		v.edits[f.ID] = row
	}
	hovered := row.handleHover.Update(gtx.Source)
	inRect(gtx, rect, func(gtx layout.Context) layout.Dimensions {
		style := surfaceStyle{content: sc.Surface.OnColor, radius: gtx.Dp(8), button: title}
		if movable && hovered {
			style.background = sc.Surface.OnColor.SetOpacity(token.OpacityLevel2)
		}
		content := func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min.Y = 0
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return exact(gtx, image.Pt(handleWidth, height), func(gtx layout.Context) layout.Dimensions {
						return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							if !movable {
								return layout.Dimensions{}
							}
							return exact(gtx, image.Pt(gtx.Dp(20), gtx.Dp(20)), func(gtx layout.Context) layout.Dimensions { return iconMenuDrag(gtx, sc.SurfaceVariant.OnColor) })
						})
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return exact(gtx, image.Pt(gtx.Dp(24), gtx.Dp(24)), func(gtx layout.Context) layout.Dimensions { return icon(gtx, sc.Primary.Color) })
				}),
				layout.Rigid(layout.Spacer{Width: 12}.Layout),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return label(gtx, title, token.TypestyleTitleMedium, sc.Surface.OnColor, 1)
				}))
		}
		if f.ID == 0 && !movable {
			return content(gtx)
		}
		return row.Layout(gtx, rect.Size(), style, content)
	})
	if movable && v.pending == nil {
		// Keep the drag hit area above the full-width row, in list coordinates.
		handle := image.Rect(0, y, handleWidth, y+height)
		v.reorder.handle(gtx, handle)
		defer clip.Rect(handle).Push(gtx.Ops).Pop()
		defer pointer.PassOp{}.Push(gtx.Ops).Pop()
		row.handleHover.Add(gtx.Ops)
	}
}

func (v *folderSettings) layoutDraft(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	identity := func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return v.name.Layout(gtx, l.T("folders.name"), v.invalidName)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if !v.invalidName {
					return layout.Dimensions{}
				}
				return label(gtx, l.T("folders.name_limit"), token.TypestyleBodySmall, sc.Error.Color, 2)
			}),
			vspace(12),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				icon := v.working.Emoticon
				if icon == "" {
					icon = l.T("folders.auto_icon")
				}
				return textButton(gtx, &v.chooseIcon, l.T("folders.icon")+" · "+icon)
			}),
		)
	}
	sectionCard := func(body layout.Widget) layout.FlexChild {
		return layout.Rigid(func(gtx layout.Context) layout.Dimensions { return card(gtx, body, defaultCardPadding) })
	}
	cards := []layout.FlexChild{sectionCard(identity), vspace(12), sectionCard(func(gtx layout.Context) layout.Dimensions { return v.ruleSection(gtx, l, true) })}
	if !v.working.Shared {
		cards = append(cards, vspace(12), sectionCard(func(gtx layout.Context) layout.Dimensions { return v.ruleSection(gtx, l, false) }))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, cards...)
}

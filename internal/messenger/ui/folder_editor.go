// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"slices"
	"strconv"
	"strings"
	"time"

	"gio-mw/token"
	"gioui.org/layout"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

var folderExcludeKeys = []string{"folders.muted", "folders.read", "folders.archived"}

func (v *folderSettings) begin(gtx layout.Context, p *settingsPage, base *model.Folder, f model.Folder) {
	v.reorder.cancel()
	if base == nil {
		f.ID = model.NewFolderID(v.store.Folders())
	}
	v.base, v.initial, v.working = base, f, f
	v.draft, v.invalidName, v.invalidTypes = true, false, false
	v.autoName, v.customName = "", base != nil || f.Title != ""
	v.name.Clear()
	v.name.editor.SetText(f.Title)
	typeValues := []bool{f.Contacts, f.NonContacts, f.Groups, f.Channels, f.Bots}
	var selected []string
	for i, on := range typeValues {
		if on {
			selected = append(selected, folderTypeKeys[i])
		}
	}
	v.types.SetValues(selected)
	selected = nil
	for i, on := range []bool{f.ExcludeMuted, f.ExcludeRead, f.ExcludeArchived} {
		if on {
			selected = append(selected, folderExcludeKeys[i])
		}
	}
	v.excludeTypes.SetValues(selected)
	p.list.Position = layout.Position{}
	v.name.Focus(gtx)
}

func (v *folderSettings) definition() model.Folder {
	f := v.working
	f.Title = strings.TrimSpace(v.name.Text())
	types := v.types.GetValues()
	f.FolderTypes = model.FolderTypes{Contacts: slices.Contains(types, "folders.contacts"), NonContacts: slices.Contains(types, "folders.noncontacts"), Groups: slices.Contains(types, "folders.groups"), Channels: slices.Contains(types, "folders.channels"), Bots: slices.Contains(types, "folders.bots")}
	excludes := v.excludeTypes.GetValues()
	f.ExcludeMuted = slices.Contains(excludes, "folders.muted")
	f.ExcludeRead = slices.Contains(excludes, "folders.read")
	f.ExcludeArchived = slices.Contains(excludes, "folders.archived")
	return f
}

// Run the requested navigation with the current frame, after the draft is resolved.
func (v *folderSettings) finishDraft(gtx layout.Context) {
	next := v.afterLeave
	v.afterLeave = nil
	v.draft = false
	v.base = nil
	v.working, v.initial = model.Folder{}, model.Folder{}
	v.saveDialog.Hide()
	v.iconDialog.Hide()
	v.peers.modal.Hide()
	v.peers.release()
	if next != nil {
		next(gtx)
	}
}

func (v *folderSettings) leaveDraft(gtx layout.Context, l localization.Catalog, next func(layout.Context)) {
	if v.pending != nil || v.saveDialog.Shown() {
		return
	}
	v.afterLeave = next
	if !v.peers.modal.Shown() && !v.iconDialog.Shown() {
		v.updateFields(gtx, l)
	}
	// A new folder opened from a filled-in template can be saved immediately.
	f := v.definition()
	if v.peers.modal.Shown() && !v.peers.modal.closing {
		v.peers.applyTo(&f)
	}
	if !v.initial.SameRules(f) || v.base == nil && v.initial.Title != "" {
		v.saveDialog.Open()
	} else {
		v.finishDraft(gtx)
	}
}

func (v *folderSettings) ruleSection(gtx layout.Context, l localization.Catalog, include bool) layout.Dimensions {
	keys, boxes, heading, action, button, count := folderExcludeKeys, v.excludeTypes, "folders.exclude", "folders.select_exclude", &v.excludePeers, len(v.working.Excluded)
	if include {
		keys, boxes, heading, action, button, count = folderTypeKeys, v.types, "folders.include", "folders.select_include", &v.includePeers, len(v.working.Pinned)+len(v.working.Included)
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T(heading), token.TypestyleTitleMedium, scheme(gtx).Surface.OnColor, 1)
		}),
		vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if v.working.Shared {
				return layout.Dimensions{}
			}
			labels := make(map[string]string, len(keys))
			for _, key := range keys {
				labels[key] = l.T(key)
			}
			return boxes.Layout(gtx, labels)
		}),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return textButton(gtx, button, l.T(action)+" ("+strconv.Itoa(count)+")")
		}), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			if include && v.invalidTypes {
				return label(gtx, l.T("folders.types_required"), token.TypestyleBodySmall, scheme(gtx).Error.Color, 2)
			}
			return layout.Dimensions{}
		}))
}

type folderSuggestions struct {
	folders []model.Folder
	err     error
}

func (v *folderSettings) updateSuggestions(gtx layout.Context, p *settingsPage, l localization.Catalog) {
	select {
	case result := <-v.suggestions:
		v.suggestions = nil
		if result.err != nil {
			p.toast.Show(l.T("folders.suggestions_failed"))
		} else {
			v.loadedSuggestions = true
			v.templates = result.folders
			v.templateButtons = make([]surface, len(v.templates))
		}
	default:
	}
	if v.recommended.Clicked(gtx) && v.suggestions == nil && !v.loadedSuggestions {
		done := make(chan folderSuggestions, 1)
		v.suggestions = done
		store := v.store
		go func() {
			defer crash.Recover("suggested folders", func(e *crash.Panic) { done <- folderSuggestions{err: e}; p.invalidate() })
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			folders, err := store.SuggestedFolders(ctx)
			done <- folderSuggestions{folders, err}
			p.invalidate()
		}()
	}
	for i := range v.templates {
		if v.templateButtons[i].Clicked(gtx) {
			v.begin(gtx, p, nil, v.templates[i])
			return
		}
	}
}

func (v *folderSettings) layoutSuggestions(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	if !v.loadedSuggestions {
		if v.suggestions != nil {
			gtx = gtx.Disabled()
		}
		return textButton(gtx, &v.recommended, l.T("folders.recommended"))
	}
	if len(v.templates) == 0 {
		return label(gtx, l.T("folders.no_suggestions"), token.TypestyleBodySmall, scheme(gtx).SurfaceVariant.OnColor, 2)
	}
	children := []layout.FlexChild{vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
		return label(gtx, l.T("folders.recommended"), token.TypestyleTitleSmall, scheme(gtx).Surface.OnColor, 1)
	})}
	for i, f := range v.templates {
		children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, &v.templateButtons[i], f.Title) }))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
}

// The empty emoticon restores Telegram's automatic icon.
var folderIcons = []string{"🐱", "📕", "💰", "🎮", "💡", "👌", "🎵", "🎨", "✈️", "⚽️", "⭐", "🎓", "🛫", "👤", "👥", "💬", "✅", "🤖", "👑", "🌹", "🏠", "❤", "🎭", "🍸", "📈", "💼", "🔔", "📢", "📁", "📋"}

func (v *folderSettings) overlay(gtx layout.Context, l localization.Catalog) {
	if v.saveDialog.Shown() {
		v.saveDialog.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(380))
			return v.saveDialog.Card(gtx, func(gtx layout.Context) layout.Dimensions {
				return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return label(gtx, l.T("folders.ask_save"), token.TypestyleTitleMedium, scheme(gtx).Surface.OnColor, 2)
					}), vspace(16),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return folderDialogActions(gtx, &v.discard, l.T("folders.no_save"), &v.save, l.T("composer.save"))
					}),
					layout.Rigid(func(gtx layout.Context) layout.Dimensions {
						return layout.E.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return textButton(gtx, &v.stay, l.T("folders.keep_editing"))
						})
					}))
			}, defaultCardPadding)
		})
		if v.saveDialog.closing || !v.saveDialog.Shown() {
			v.afterLeave = nil
		}
		return
	}
	if v.peers.modal.Shown() {
		v.layoutPeers(gtx, l)
		return
	}
	if !v.iconDialog.Shown() {
		return
	}
	for icon, b := range v.iconButtons {
		if b.Clicked(gtx) {
			v.working.Emoticon = icon
			v.iconDialog.Close()
		}
	}
	v.iconDialog.Layout(gtx, false, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(360))
		return v.iconDialog.Card(gtx, func(gtx layout.Context) layout.Dimensions {
			control := func(icon string) *surface {
				b := v.iconButtons[icon]
				if b == nil {
					b = new(surface)
					v.iconButtons[icon] = b
				}
				return b
			}
			children := []layout.FlexChild{
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, l.T("folders.icon"), token.TypestyleTitleMedium, scheme(gtx).Surface.OnColor, 1)
				}), vspace(8),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if v.working.Emoticon == "" {
						return tonalButton(gtx, control(""), l.T("folders.auto_icon"), 0)
					}
					return textButton(gtx, control(""), l.T("folders.auto_icon"))
				}), vspace(6)}
			cols := max(1, gtx.Constraints.Max.X/gtx.Dp(48))
			cell := image.Pt(gtx.Constraints.Max.X/cols, gtx.Dp(44))
			for group := range slices.Chunk(folderIcons, cols) {
				children = append(children, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					var row []layout.FlexChild
					for _, icon := range group {
						row = append(row, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							sc := scheme(gtx)
							style := surfaceStyle{radius: gtx.Dp(8), content: sc.Surface.OnColor, button: icon}
							if icon == v.working.Emoticon {
								style.background = sc.SecondaryContainer.Color
							}
							gtx.Constraints = layout.Exact(cell)
							return control(icon).Layout(gtx, cell, style, func(gtx layout.Context) layout.Dimensions {
								return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
									return label(gtx, icon, token.TypestyleTitleMedium, style.content, 1)
								})
							})
						}))
					}
					return layout.Flex{}.Layout(gtx, row...)
				}))
			}
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, children...)
		}, defaultCardPadding)
	})
}

func folderDialogActions(gtx layout.Context, cancel *surface, cancelText string, confirm *surface, confirmText string) layout.Dimensions {
	return layout.Flex{Spacing: layout.SpaceStart, Alignment: layout.Middle}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return textButton(gtx, cancel, cancelText) }),
		layout.Rigid(layout.Spacer{Width: 8}.Layout),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions { return tonalButton(gtx, confirm, confirmText, 0) }))
}

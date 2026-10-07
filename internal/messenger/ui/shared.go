// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"io"
	"strconv"
	"strings"
	"time"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/chatmedia"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"

	"gio-mw/token"
	"gio-mw/wdk"
	"gio-mw/widget/scroll"

	"gioui.org/io/clipboard"
	"gioui.org/io/pointer"
	"gioui.org/layout"
	"gioui.org/op/clip"
	"gioui.org/widget"
	"golang.org/x/exp/shiny/materialdesign/icons"
)

type sharedResult struct {
	cursor     string
	collection bool
	page       model.SharedPage
	counts     map[model.SharedKind]int
	err        error
}
type chatInfo struct {
	cardHeights       [4]heightTransition
	loader            loadingIndicator
	giftGeneration    uint64
	drawAvatar        avatarLayout
	openAvatar        func(chat int64)
	avatar            widget.Clickable
	gift              *giftDialog
	giftRows          map[model.MessageKey]*giftRow
	linkRows          map[model.MessageKey]*sharedLinkRow
	collection        model.SharedCollectionSource
	kinds             []model.SharedKind
	cursor            string
	source            model.SharedMediaSource
	renderer          *chatPage
	invalidate        func()
	chat              model.Chat
	visible           bool
	section           model.SharedKind
	list              scroll.List
	counts            map[model.SharedKind]int
	messages          []model.Message
	next              model.MessageID
	more, loading     bool
	problem           error
	cancel            context.CancelFunc
	result            chan sharedResult
	close, back       surface
	retry, moreButton surface
	modal             modal
	sections          map[model.SharedKind]*settingsItem
	themesButton      settingsItem
	// username copies the chat's @username, and usernameLink its link, as
	// AyuGram's Copy Username and Copy Username as Link.
	usernames              model.UsernameSource
	username, usernameLink settingsItem
	// told is the failure to load told in the toast last.
	told error
	// details tells the registration of a user and the data center of a
	// chat's photo, as materialgram's profile; each row copies its text.
	details                  model.ChatDetailer
	registration, dataCenter settingsItem
	themePage                bool
	themes                   *chatThemeController
}

func newChatInfo(source model.ConversationStore, images *imageOps, invalidate func()) *chatInfo {
	p := &chatInfo{invalidate: invalidate, sections: map[model.SharedKind]*settingsItem{}}
	p.source, _ = source.(model.SharedMediaSource)
	p.usernames, _ = source.(model.UsernameSource)
	p.details, _ = source.(model.ChatDetailer)
	p.collection, _ = source.(model.SharedCollectionSource)
	p.renderer = newChatPage(source, invalidate)
	p.renderer.media.Close()
	p.renderer.media = chatmedia.NewShared(source, invalidate)
	p.renderer.images = images
	p.renderer.rows = map[model.MessageID]*messageRow{}
	p.linkRows = map[model.MessageKey]*sharedLinkRow{}
	p.giftRows = map[model.MessageKey]*giftRow{}
	p.gift = nil
	for _, k := range append(append([]model.SharedKind{}, model.SharedKinds...), model.SharedStories, model.SharedGifts, model.SharedGroups) {
		p.sections[k] = new(settingsItem)
	}
	// Escape goes back from a section before it closes the dialog.
	p.modal.back = func() bool {
		if p.section == "" && !p.themePage {
			return false
		}
		p.goBack()
		return true
	}
	return p
}
func (p *chatInfo) Open(c model.Chat) {
	p.stop()
	p.visible = true
	p.modal.Open()
	p.chat = c
	p.kinds = append([]model.SharedKind{}, model.SharedKinds...)
	if p.collection != nil {
		if c.Kind != model.KindGroup {
			p.kinds = append([]model.SharedKind{model.SharedStories, model.SharedGifts}, p.kinds...)
		}
		if c.Kind == model.KindUser || c.Kind == model.KindBot {
			p.kinds = append(p.kinds, model.SharedGroups)
		}
	}
	p.section = ""
	p.themePage = false
	p.messages = nil
	p.counts = nil
	p.list = scroll.List{List: layout.List{Axis: layout.Vertical}}
	p.renderer.chat = c.ID
	p.renderer.kind = c.Kind
	p.renderer.rows = map[model.MessageID]*messageRow{}
	p.linkRows = map[model.MessageKey]*sharedLinkRow{}
	p.giftRows = map[model.MessageKey]*giftRow{}
	p.gift = nil
	p.load(true)
}
func (p *chatInfo) stop() {
	if p.cancel != nil {
		p.cancel()
		p.cancel = nil
	}
	p.loading = false
	p.result = nil
}

// Close closes the dialog at once and releases what it shows.
func (p *chatInfo) Close() {
	p.stop()
	p.visible = false
	p.modal.Hide()
	p.renderer.media.Clear()
	p.renderer.rows = map[model.MessageID]*messageRow{}
	p.linkRows = map[model.MessageKey]*sharedLinkRow{}
	p.giftRows = map[model.MessageKey]*giftRow{}
	p.gift = nil
	p.messages = nil
	if p.themes != nil {
		p.themes.DiscardPreview()
	}
}

// Release drops the decoded thumbnails of a hidden window. The open section
// keeps its messages and loads them again when shown.
func (p *chatInfo) Release() {
	p.renderer.media.Release()
	for _, r := range p.giftRows {
		r.background = nil
		r.patternSource = nil
		r.patternTint = nil
	}
}
func (p *chatInfo) Destroy() { p.Close(); p.renderer.chat = 0; p.renderer.Close() }
func (p *chatInfo) load(counts bool) {
	if p.source == nil || p.loading {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	p.cancel = cancel
	ch := make(chan sharedResult, 1)
	p.result = ch
	p.loading = true
	p.problem = nil
	chat, kind, next := p.chat.ID, p.section, p.next
	cursor := p.cursor
	go func() {
		defer cancel()
		send := func(r sharedResult) {
			select {
			case ch <- r:
				p.invalidate()
			case <-ctx.Done():
			}
		}
		defer crash.Recover("shared media", func(e *crash.Panic) { send(sharedResult{err: e}) })
		var r sharedResult
		if counts {
			r.counts, r.err = p.source.SharedCounts(ctx, chat)
		} else if kind == model.SharedStories || kind == model.SharedGifts || kind == model.SharedGroups {
			var page model.SharedCollectionPage
			page, r.err = p.collection.SharedCollection(ctx, chat, kind, cursor, 60)
			r.collection = true
			r.cursor = page.Next
			r.page = model.SharedPage{Messages: page.Messages, Total: page.Total, More: page.Next != ""}
		} else {
			r.page, r.err = p.source.SharedMedia(ctx, chat, kind, next, 60)
		}
		// Also deliver deadline errors, allowing the user to retry.
		ch <- r
		p.invalidate()
	}()
}
func (p *chatInfo) drain() {
	if p.result == nil {
		return
	}
	select {
	case r := <-p.result:
		p.loading = false
		p.problem = r.err
		p.result = nil
		if r.err != nil {
			return
		}
		if p.section == "" {
			p.counts = r.counts
			return
		}
		seen := map[model.MessageKey]bool{}
		for _, m := range p.messages {
			seen[m.Key] = true
		}
		for _, m := range r.page.Messages {
			if !seen[m.Key] {
				p.messages = append(p.messages, m)
				seen[m.Key] = true
			}
		}
		if r.collection {
			p.more = r.page.More && r.cursor != p.cursor
			p.cursor = r.cursor
		} else {
			p.more = r.page.More && r.page.Next != p.next
		}
		if p.counts == nil {
			p.counts = map[model.SharedKind]int{}
		}
		p.counts[p.section] = r.page.Total
		p.next = r.page.Next
	default:
	}
}
func (p *chatInfo) selectSection(k model.SharedKind) {
	p.stop()
	p.section = k
	p.messages = nil
	p.next = 0
	p.cursor = ""
	p.more = false
	p.problem = nil
	p.renderer.rows = map[model.MessageID]*messageRow{}
	p.linkRows = map[model.MessageKey]*sharedLinkRow{}
	p.giftRows = map[model.MessageKey]*giftRow{}
	p.gift = nil
	p.renderer.media.Clear()
	p.list = scroll.List{List: layout.List{Axis: layout.Vertical}}
	p.load(false)
}
func (p *chatInfo) Layout(gtx layout.Context, l localization.Catalog, animate bool) {
	if !p.visible {
		return
	}
	p.drain()
	if p.problem != nil && p.problem != p.told {
		p.modal.Toast(mediaErrorText(p.problem))
	}
	p.told = p.problem
	if p.themes != nil {
		if err := p.themes.newProblem(); err != nil {
			p.modal.Toast(mediaErrorText(err))
		}
	}
	// What the media shown here tell goes in this dialog's toast: the
	// renderer's own is not drawn.
	p.renderer.errorMu.Lock()
	if err := p.renderer.mediaError; err != nil {
		p.renderer.mediaError = nil
		p.modal.Toast(mediaErrorText(err))
	}
	p.renderer.errorMu.Unlock()
	if text := p.renderer.toast.Text(); text != "" {
		p.renderer.toast.Hide()
		p.modal.Toast(text)
	}
	// The dialogs over this one take Escape first.
	if p.renderer.link != "" {
		p.renderer.linkModal.Update(gtx, false)
	}
	if p.gift != nil {
		p.gift.modal.Update(gtx, false)
	}
	if p.close.Clicked(gtx) {
		p.modal.Close()
	}
	if p.back.Clicked(gtx) {
		p.goBack()
	}
	for k, c := range p.sections {
		if c.click.Clicked(gtx) {
			p.selectSection(k)
		}
	}
	if p.avatar.Clicked(gtx) && p.openAvatar != nil {
		p.openAvatar(p.chat.ID)
	}
	if name := p.chatUsername(); name != "" {
		if p.username.click.Clicked(gtx) {
			gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader("@" + name))})
			p.modal.Toast(l.T("info.username_copied"))
		}
		if p.usernameLink.click.Clicked(gtx) {
			gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader("https://t.me/" + name))})
			p.modal.Toast(l.T("info.link_copied"))
		}
	}
	registration, dataCenter := p.detailTexts(l)
	for _, row := range []struct {
		item *settingsItem
		text string
	}{{&p.registration, registration}, {&p.dataCenter, dataCenter}} {
		if row.text != "" && row.item.click.Clicked(gtx) {
			gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(row.text))})
			p.modal.Toast(l.T("info.copied"))
		}
	}
	if p.themesButton.click.Clicked(gtx) {
		p.OpenTheme()
	}
	if p.retry.Clicked(gtx) {
		p.load(p.section == "")
	}
	if p.moreButton.Clicked(gtx) {
		p.load(false)
	}
	overlayGtx := gtx
	if p.gift != nil || p.renderer.link != "" {
		gtx = gtx.Disabled()
	}
	p.giftGeneration++
	defer p.trimGiftRows()
	p.renderer.media.BeginFrame()
	defer p.renderer.media.EndFrame()
	covered := p.gift != nil || p.renderer.link != ""
	shown := p.modal.Layout(gtx, covered, func(gtx layout.Context) layout.Dimensions {
		size := image.Pt(min(gtx.Constraints.Max.X, gtx.Dp(640)), min(gtx.Constraints.Max.Y, gtx.Dp(740)))
		gtx.Constraints = layout.Exact(size)
		defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(16)).Push(gtx.Ops).Pop()
		fillRect(gtx, scheme(gtx).Surface.Color, size)
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							if p.section != "" || p.themePage {
								return navigationButton(gtx, &p.back, iconBack, l.T("settings.back"))
							}
							return layout.Dimensions{}
						}),
						layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
							title := p.chat.Title
							if p.section != "" {
								title = l.T("shared." + string(p.section))
								if total, ok := p.counts[p.section]; ok {
									title += " · " + groupDigits(total)
								}
							}
							if p.themePage {
								title = l.T("chat_theme.title")
							}
							return label(gtx, title, token.TypestyleTitleLarge, scheme(gtx).Surface.OnColor, 1)
						}),
						layout.Rigid(func(gtx layout.Context) layout.Dimensions {
							return navigationButton(gtx, &p.close, iconClear, l.T("viewer.close"))
						}),
					)
				})
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if p.themePage && p.themes != nil {
					return p.themes.LayoutChoices(gtx, l)
				}
				if p.loading && ((p.section == "" && p.counts == nil) || (p.section != "" && len(p.messages) == 0)) {
					return p.loader.page(gtx, l)
				}
				if p.section == "" {
					return p.layoutInfo(gtx, l)
				}
				return p.layoutMedia(gtx, l, animate && p.gift == nil)
			}),
		)
	})
	if !shown {
		p.Close()
		return
	}
	p.layoutGiftDialog(overlayGtx, l, animate)
	if p.renderer.link != "" {
		p.renderer.linkDialog(overlayGtx, l)
	}
}

// OpenTheme shows the page of the chat's theme.
func (p *chatInfo) OpenTheme() {
	if p.themes == nil {
		return
	}
	p.stop()
	p.themePage = true
	p.list.Position = layout.Position{}
	p.themes.LoadChoices()
}

func (p *chatInfo) goBack() {
	if p.themes != nil {
		p.themes.DiscardPreview()
	}
	p.stop()
	p.section = ""
	p.themePage = false
	p.problem = nil
	p.list = scroll.List{List: layout.List{Axis: layout.Vertical}}
	if p.counts == nil {
		p.load(true)
	}
}
func (p *chatInfo) layoutInfo(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	fillRect(gtx, scheme(gtx).SurfaceContainerLow, gtx.Constraints.Max)
	// One measured block gives the thumb exact pixel geometry. Three unequal
	// virtual rows made the average-height estimate change during a thumb drag.
	return p.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.Inset{Top: 8, Bottom: 16, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.layoutAvatar(gtx) }),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return label(gtx, p.renderer.chatStatusOnline(p.chat, gtx.Now, l), token.TypestyleBodyMedium, scheme(gtx).SurfaceVariant.OnColor, 2)
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.status(gtx, l) }), vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					name := p.chatUsername()
					if name == "" {
						return layout.Dimensions{}
					}
					return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return p.cardHeights[0].Card(gtx, func(gtx layout.Context) layout.Dimensions {
							subtitle := l.T("info.username")
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return p.username.Layout(gtx, iconCopy, "@"+name, subtitle)
								}),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return p.usernameLink.Layout(gtx, iconLink, l.T("info.copy_link"), "t.me/"+name)
								}),
							)
						}, 6)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					registration, dataCenter := p.detailTexts(l)
					if registration == "" && dataCenter == "" {
						return layout.Dimensions{}
					}
					return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						return p.cardHeights[1].Card(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									if registration == "" {
										return layout.Dimensions{}
									}
									return p.registration.Layout(gtx, iconCalendar, registration, l.T("info.registration"))
								}),
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									if dataCenter == "" {
										return layout.Dimensions{}
									}
									return p.dataCenter.Layout(gtx, iconDataCenter, dataCenter, l.T("info.dc"))
								}),
							)
						}, 6)
					})
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if p.themes == nil {
						return layout.Dimensions{}
					}
					subtitle := p.themes.appearance.Theme.Title
					if subtitle == "" {
						subtitle = l.T("chat_theme.default")
					}
					return p.cardHeights[2].Card(gtx, func(gtx layout.Context) layout.Dimensions {
						return p.themesButton.Layout(gtx, iconPalette, l.T("chat_theme.title"), subtitle)
					}, 6)
				}), vspace(12),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					kinds := p.visibleKinds()
					if len(kinds) == 0 {
						return layout.Dimensions{}
					}
					return p.cardHeights[3].Card(gtx, func(gtx layout.Context) layout.Dimensions {
						rows := make([]layout.FlexChild, 0, len(kinds))
						for _, kind := range kinds {
							rows = append(rows, layout.Rigid(func(gtx layout.Context) layout.Dimensions {
								// While counting, the ring of the status above tells so.
								subtitle := ""
								if n, ok := p.counts[kind]; ok {
									subtitle = l.SharedCount(kind, n)
								}
								return p.sections[kind].Layout(gtx, sharedIcons[kind], l.T("shared."+string(kind)), subtitle)
							}))
						}
						return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
					}, 6)
				}),
			)
		})
	})
}
func (p *chatInfo) visibleKinds() []model.SharedKind {
	kinds := make([]model.SharedKind, 0, len(p.kinds))
	for _, kind := range p.kinds {
		if n, known := p.counts[kind]; !known || n > 0 {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

func (p *chatInfo) status(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	if p.loading {
		return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(image.Pt(gtx.Dp(32), gtx.Dp(32)))
			return p.loader.Layout(gtx, l)
		})
	}
	if p.problem != nil {
		return textButton(gtx, &p.retry, l.T("history.retry"))
	}
	if p.section != "" && len(p.messages) == 0 {
		return label(gtx, l.SharedEmpty(p.section), token.TypestyleBodyMedium, scheme(gtx).SurfaceVariant.OnColor, 2)
	}
	return layout.Dimensions{}
}
func (p *chatInfo) layoutMedia(gtx layout.Context, l localization.Catalog, animate bool) layout.Dimensions {
	fillRect(gtx, scheme(gtx).SurfaceContainerLow, gtx.Constraints.Max)
	p.renderer.appearance = nil
	if p.section == model.SharedPolls || p.section == model.SharedSaved {
		p.renderer.appearance = p.themes
		if p.themes != nil {
			p.themes.Backdrop(gtx)
			gtx = p.themes.historyContext(gtx)
		}
	}
	if p.section == model.SharedGifts {
		return p.giftsGrid(gtx, l, animate)
	}
	grid := p.section == model.SharedPhotos || p.section == model.SharedVideos || p.section == model.SharedGIFs || p.section == model.SharedStories
	if grid {
		return layout.Inset{Left: 16, Right: 16, Top: 12, Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return p.layoutMediaRows(gtx, l, animate, true) })
	}
	return p.layoutMediaRows(gtx, l, animate, false)
}
func (p *chatInfo) layoutMediaRows(gtx layout.Context, l localization.Catalog, animate, grid bool) layout.Dimensions {
	cols := max(1, (gtx.Constraints.Max.X+gtx.Dp(3))/max(1, gtx.Dp(143)))
	rows := len(p.messages)
	if grid {
		rows = (rows + cols - 1) / cols
	}
	dims := p.list.Layout(gtx, rows+1, func(gtx layout.Context, i int) layout.Dimensions {
		if i == rows {
			return layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				if p.more && !p.loading && p.problem == nil {
					return textButton(gtx, &p.moreButton, l.T("shared.more"))
				}
				return p.status(gtx, l)
			})
		}
		if p.section == model.SharedLinks {
			return p.linkRow(gtx, p.messages[i], l)
		}
		if !grid {
			return p.renderer.row(gtx, p.messages[i], i == 0 || !sameMessageDay(p.messages[i-1], p.messages[i]), 0, l, animate)
		}
		start, end := i*cols, min(len(p.messages), (i+1)*cols)
		gap := gtx.Dp(3)
		width := gtx.Constraints.Max.X
		usable := max(1, width-gap*(cols-1))
		sum := float64(cols)
		aspects := make([]float64, end-start)
		for j := range aspects {
			aspects[j] = 1
		}
		if p.section == model.SharedGIFs {
			usable = max(1, width-gap*(end-start-1))
			sum = 0
			for j, m := range p.messages[start:end] {
				if m.Media != nil && m.Media.Height > 0 {
					aspects[j] = max(.5, min(2.5, float64(m.Media.Width)/float64(m.Media.Height)))
				}
				sum += aspects[j]
			}
		}
		height := max(1, int(float64(usable)/sum))
		if p.section == model.SharedGIFs {
			height = min(height, gtx.Dp(240))
		}
		x := 0
		for j, m := range p.messages[start:end] {
			w := max(1, int(float64(height)*aspects[j]))
			if p.section != model.SharedGIFs {
				w = max(1, usable/cols)
				if j < usable%cols {
					w++
				}
			}
			r := p.renderer.rows[m.Key.MessageID]
			if r == nil {
				r = &messageRow{}
				p.renderer.rows[m.Key.MessageID] = r
			}
			cell := gtx
			cell.Constraints = layout.Exact(image.Pt(w, height))
			offset(cell, image.Pt(x, 0), func(gtx layout.Context) layout.Dimensions {
				if m.Media == nil {
					return layout.Dimensions{Size: gtx.Constraints.Max}
				}
				return p.renderer.mediaTile(gtx, r, m, image.Pt(w, height), true, l, animate)
			})
			x += w + gap
		}
		return layout.Dimensions{Size: image.Pt(width, height+gap)}
	})
	if p.more && !p.loading && p.problem == nil && p.list.Position.First+p.list.Position.Count >= rows-2 {
		p.load(false)
	}
	return dims
}
func sameMessageDay(a, b model.Message) bool {
	return a.Date.Local().Format("2006-01-02") == b.Date.Local().Format("2006-01-02")
}

var sharedIcons = map[model.SharedKind]wdk.IconWidget{
	model.SharedPhotos: wdk.RequireIconWidget(icons.ImagePhoto), model.SharedVideos: wdk.RequireIconWidget(icons.AVVideocam),
	model.SharedFiles: wdk.RequireIconWidget(icons.EditorInsertDriveFile), model.SharedMusic: wdk.RequireIconWidget(icons.HardwareHeadset),
	model.SharedLinks: wdk.RequireIconWidget(icons.ContentLink), model.SharedVoice: wdk.RequireIconWidget(icons.AVMic),
	model.SharedPolls: wdk.RequireIconWidget(icons.SocialPoll), model.SharedGIFs: wdk.RequireIconWidget(icons.ActionGIF),
	model.SharedSaved: iconSaved, model.SharedStories: wdk.RequireIconWidget(icons.AVPlayCircleOutline),
	model.SharedGifts: wdk.RequireIconWidget(icons.ActionCardGiftcard), model.SharedGroups: iconGroups,
}

// chatUsername is the open chat's public username, when it has one.
func (p *chatInfo) chatUsername() string {
	if p.usernames == nil || p.chat.ID == 0 {
		return ""
	}
	return p.usernames.Username(p.chat.ID)
}

// detailTexts are the rows of the chat's details: when a user registered,
// and where the chat's photo is kept; empty when not known.
func (p *chatInfo) detailTexts(l localization.Catalog) (registration, dataCenter string) {
	if p.details == nil || p.chat.ID == 0 {
		return "", ""
	}
	if p.chat.Kind == model.KindUser || p.chat.Kind == model.KindBot {
		at, how := model.RegisteredAround(p.chat.ID)
		key := map[model.Registration]string{model.RegisteredAbout: "info.registered_about", model.RegisteredBefore: "info.registered_before", model.RegisteredAfter: "info.registered_after"}[how]
		registration = l.Format(key, map[string]string{"date": at.Local().Format("01.2006")})
	}
	if dc := p.details.ChatDetails(p.chat.ID).PhotoDC; dc > 0 {
		dataCenter = "DC " + strconv.Itoa(dc)
		if name := model.DataCenterName(dc); name != "" {
			dataCenter += ", " + name
		}
	}
	return registration, dataCenter
}

// layoutAvatar draws the chat's avatar over its info, centered; a click on
// it shows the photo in the viewer.
func (p *chatInfo) layoutAvatar(gtx layout.Context) layout.Dimensions {
	if p.drawAvatar == nil || p.chat.Kind == model.KindSaved {
		return layout.Dimensions{}
	}
	return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.N.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{}
			return p.avatar.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				pointer.CursorPointer.Add(gtx.Ops)
				return p.drawAvatar(gtx, p.chat.ID, p.chat.Kind, p.chat.Title, pageAvatarSize)
			})
		})
	})
}

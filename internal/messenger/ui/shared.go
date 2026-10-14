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
	"gioui.org/op"
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
	cardHeights       [6]heightTransition
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
	// openChat, when set, goes to the chat shown, which is not open: the
	// info of a group's member who has no chat with the account yet.
	openChat func()
	goToChat settingsItem
	// closed is set for a chat the account may not read: its info has none
	// of its media.
	closed bool
	// profile is the chat's profile, as profiles told it: the cover over
	// the info, the user's channel and the collectible usernames, whose
	// chips open collectible.
	profiles      model.PeerProfileSource
	profile       model.PeerProfile
	profileResult chan profileResult
	profileCancel context.CancelFunc
	cover         coverState
	fades         coverFades
	motion        coverMotion
	badges        badgesLayout
	channel       settingsItem
	chips         map[string]*folderChip
	collectible   *collectibleDialog
	// openChannel opens the user's channel, with a way back.
	openChannel func(model.Chat)
	// avatarSprite is the chat's avatar as a picture of side px, which the
	// avatar's motion blurs; nil while there is none.
	avatarSprite func(gtx layout.Context, id int64, kind model.ChatKind, title string, px int) image.Image
}

func newChatInfo(source model.ConversationStore, images *imageOps, invalidate func()) *chatInfo {
	p := &chatInfo{invalidate: invalidate, sections: map[model.SharedKind]*settingsItem{}}
	p.source, _ = source.(model.SharedMediaSource)
	p.usernames, _ = source.(model.UsernameSource)
	p.details, _ = source.(model.ChatDetailer)
	p.collection, _ = source.(model.SharedCollectionSource)
	p.profiles, _ = source.(model.PeerProfileSource)
	p.chips = map[string]*folderChip{}
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
	p.openChat, p.closed = nil, false
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
	p.closeCollectible()
	p.profile = model.PeerProfile{}
	p.cover = coverState{}
	p.fades = coverFades{places: shuffledPlaces()}
	p.motion = coverMotion{}
	// The profile known last shows at once, without fading in; Telegram's
	// replaces it when it comes.
	if p.profiles != nil && c.ID != 0 && c.Kind != model.KindSaved {
		p.profile, _ = p.profiles.CachedPeerProfile(c.ID)
	}
	p.loadProfile()
	p.load(true)
}

// closeCollectible drops the dialog of a collectible username.
func (p *chatInfo) closeCollectible() {
	if p.collectible != nil {
		p.collectible.cancel()
		p.collectible = nil
	}
}

// OpenOver opens the info of c over another chat, open, with a button that
// goes to c by openChat.
func (p *chatInfo) OpenOver(c model.Chat, openChat func()) {
	p.Open(c)
	p.openChat = openChat
}

// OpenClosed opens the info of c, a chat the account may not read: what
// it is, without its media, which are not asked for, and why in the toast.
func (p *chatInfo) OpenClosed(c model.Chat, why string) {
	p.Open(c)
	p.stop()
	p.closed = true
	p.kinds = nil
	p.modal.Toast(why)
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
	p.closeCollectible()
	if p.profileCancel != nil {
		p.profileCancel()
		p.profileCancel = nil
	}
	p.profileResult = nil
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
	p.cover.gradient, p.cover.gradientKey = nil, ""
	p.cover.glows = nil
	p.cover.patternSource, p.cover.patternTint = nil, nil
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
	p.drainProfile()
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
	if p.collectible != nil {
		p.collectible.modal.Update(gtx, false)
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
	if ch := p.profile.Channel; ch != nil && p.channel.click.Clicked(gtx) && p.openChannel != nil {
		open := p.openChannel
		p.modal.Close()
		open(ch.Chat)
	}
	for _, u := range p.profile.Usernames {
		if c := p.chips[u.Name]; c != nil && u.Collectible && c.Clicked(gtx) {
			p.openCollectible(u.Name)
		}
	}
	if p.openChat != nil && p.goToChat.click.Clicked(gtx) {
		open := p.openChat
		p.modal.Close()
		open()
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
	covered := p.gift != nil || p.collectible != nil || p.renderer.link != ""
	if covered {
		gtx = gtx.Disabled()
	}
	p.giftGeneration++
	defer p.trimGiftRows()
	defer mediaFrame(gtx, p.renderer.media)()
	shown := p.modal.Layout(gtx, covered, func(gtx layout.Context) layout.Dimensions {
		size := image.Pt(min(gtx.Constraints.Max.X, gtx.Dp(640)), min(gtx.Constraints.Max.Y, gtx.Dp(740)))
		gtx.Constraints = layout.Exact(size)
		defer clip.UniformRRect(image.Rectangle{Max: size}, gtx.Dp(16)).Push(gtx.Ops).Pop()
		// The header has no color of its own: it is the body's.
		fillRect(gtx, scheme(gtx).SurfaceContainerLow, size)
		if p.section == "" && !p.themePage {
			// The info scrolls under the header, which shows the name
			// only once the cover's has gone under it.
			p.layoutInfo(gtx, l, animate && p.gift == nil)
			p.layoutInfoHeader(gtx, l, animate)
			return layout.Dimensions{Size: size}
		}
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				title := l.T("shared." + string(p.section))
				if total, ok := p.counts[p.section]; ok {
					title += " · " + groupDigits(total)
				}
				if p.themePage {
					title = l.T("chat_theme.title")
				}
				return p.layoutHeader(gtx, l, title)
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				if p.themePage && p.themes != nil {
					return p.themes.LayoutChoices(gtx, l)
				}
				if p.loading && len(p.messages) == 0 {
					return p.loader.page(gtx, l)
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
	p.layoutCollectible(overlayGtx, l)
	if p.renderer.link != "" {
		p.renderer.linkDialog(overlayGtx, l)
	}
}

// layoutHeader draws the header of a section or the theme's page: back,
// the title, and the close button. The info's is layoutInfoHeader.
func (p *chatInfo) layoutHeader(gtx layout.Context, l localization.Catalog, title string) layout.Dimensions {
	content := scheme(gtx).Surface.OnColor
	gtx.Constraints.Min = image.Pt(gtx.Constraints.Max.X, 0)
	return layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if p.section != "" || p.themePage {
					return navigationButton(gtx, &p.back, iconBack, l.T("settings.back"))
				}
				return layout.Dimensions{}
			}),
			layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
				return label(gtx, title, token.TypestyleTitleLarge, content, 1)
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				return navigationButtonColor(gtx, &p.close, iconClear, l.T("viewer.close"), content)
			}),
		)
	})
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
func (p *chatInfo) layoutInfo(gtx layout.Context, l localization.Catalog, animate bool) layout.Dimensions {
	fillRect(gtx, scheme(gtx).SurfaceContainerLow, gtx.Constraints.Max)
	// One measured block gives the thumb exact pixel geometry. Three unequal
	// virtual rows made the average-height estimate change during a thumb drag.
	return p.list.Layout(gtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.layoutCover(gtx, l, animate) }),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.layoutCards(gtx, l, animate) }),
		)
	})
}

// layoutCards are the info's cards under the cover.
func (p *chatInfo) layoutCards(gtx layout.Context, l localization.Catalog, animate bool) layout.Dimensions {
	return layout.Inset{Top: 12, Bottom: 16, Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
			// Loading is told in the header; a failure here, with a retry.
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if p.loading || p.problem == nil {
					return layout.Dimensions{}
				}
				return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions { return p.status(gtx, l) })
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				if p.openChat == nil {
					return layout.Dimensions{}
				}
				return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
					return p.cardHeights[4].Card(gtx, func(gtx layout.Context) layout.Dimensions {
						return p.goToChat.Layout(gtx, iconChats, l.T("info.open_chat"), l.T("info.open_chat_new"))
					}, 6)
				})
			}),
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.layoutChannel(gtx, l, animate) }),
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
			layout.Rigid(func(gtx layout.Context) layout.Dimensions { return p.layoutCollectibles(gtx) }),
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
				if p.themes == nil || p.openChat != nil || p.closed {
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
}

// layoutChannel is the card of a user's channel, as the cards of the
// sections: its avatar, its name, and its subscribers with its last post.
func (p *chatInfo) layoutChannel(gtx layout.Context, l localization.Catalog, animate bool) layout.Dimensions {
	ch := p.profile.Channel
	if ch == nil {
		return layout.Dimensions{}
	}
	title := l.ChatTitle(ch.Chat)
	subtitle := richLabel{runs: channelLine(*ch, l), style: token.TypestyleBodySmall, color: scheme(gtx).SurfaceVariant.OnColor, maxLines: 1,
		media: p.renderer.media, images: p.renderer.images, animate: animate}
	return layout.Inset{Bottom: 12}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		return p.cardHeights[5].Card(gtx, func(gtx layout.Context) layout.Dimensions {
			size := gtx.Dp(40)
			return p.channel.layoutWith(gtx, func(gtx layout.Context) layout.Dimensions {
				if p.drawAvatar == nil {
					return layout.Dimensions{Size: gtx.Constraints.Max}
				}
				return p.drawAvatar(gtx, ch.Chat.ID, ch.Chat.Kind, ch.Chat.Title, 40)
			}, size, title, subtitle.Layout)
		}, 6)
	})
}

// channelLine is the line under the name of a user's channel: its
// subscribers, then its last post as the chat list words it, but with its
// text whole, custom emoji too.
func channelLine(ch model.PersonalChannel, l localization.Catalog) []model.TextRun {
	var runs []model.TextRun
	if n := ch.Chat.Members; n > 0 {
		runs = append(runs, model.TextRun{Text: l.Count("status.subscribers", n, map[string]string{"count": groupDigits(n)})})
	}
	var post []model.TextRun
	if ch.Post.Service != nil || ch.Post.Text == "" {
		if _, text := l.Preview(ch.Post, ch.Chat.Title, true); text != "" {
			post = append(post, model.TextRun{Text: text})
		}
	} else {
		media := ch.Post
		media.Text = ""
		if _, prefix := l.Preview(media, ch.Chat.Title, true); prefix != "" {
			post = append(post, model.TextRun{Text: prefix + ", "})
		}
		post = append(post, model.TextRuns(ch.Post.Text, ch.Entities)...)
	}
	if len(runs) > 0 && len(post) > 0 {
		runs = append(runs, model.TextRun{Text: " • "})
	}
	return append(runs, post...)
}

// layoutCollectibles are the chips of the collectible usernames, in lines
// as many as they take; a click tells about one.
func (p *chatInfo) layoutCollectibles(gtx layout.Context) layout.Dimensions {
	var names []string
	for _, u := range p.profile.Usernames {
		if u.Collectible {
			names = append(names, u.Name)
		}
	}
	if len(names) == 0 {
		return layout.Dimensions{}
	}
	gap := gtx.Dp(8)
	width := gtx.Constraints.Max.X
	gtx.Constraints.Min = image.Point{}
	x, y, line := 0, 0, 0
	for _, name := range names {
		c := p.chips[name]
		if c == nil {
			c = new(folderChip)
			p.chips[name] = c
		}
		macro := op.Record(gtx.Ops)
		dims := c.Layout(gtx, "@"+name, true, 0)
		call := macro.Stop()
		if x > 0 && x+dims.Size.X > width {
			x, y = 0, y+line+gap
			line = 0
		}
		offset(gtx, image.Pt(x, y), func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return dims
		})
		x += dims.Size.X + gap
		line = max(line, dims.Size.Y)
	}
	return layout.Dimensions{Size: image.Pt(width, y+line+gtx.Dp(12))}
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

// layoutAvatar draws the chat's avatar in the cover; a click on it shows
// the photo in the viewer.
func (p *chatInfo) layoutAvatar(gtx layout.Context) layout.Dimensions {
	if p.drawAvatar == nil {
		return layout.Dimensions{}
	}
	gtx.Constraints.Min = image.Point{}
	return p.avatar.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		pointer.CursorPointer.Add(gtx.Ops)
		return p.drawAvatar(gtx, p.chat.ID, p.chat.Kind, p.chat.Title, pageAvatarSize)
	})
}

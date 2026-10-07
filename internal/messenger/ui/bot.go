// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"strings"
	"sync"
	"time"

	"gio-mw/token"

	"gioui.org/io/clipboard"
	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/op/clip"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/model"
)

// Bots, as Telegram Desktop shows them: the buttons under a message ask the
// bot (a callback button), open a link or copy a text; a keyboard the bot
// sets takes the place under the composer, with buttons that send their
// text; and an empty chat with a bot has a Start button instead of the
// composer.

const (
	// keyboardButton and keyboardGap are the height of a key of the reply
	// keyboard and the space around it, in dp.
	keyboardButton = 40
	keyboardGap    = 6
	// botWait is how long a bot has to answer a button.
	botWait = 30 * time.Second
)

// botPage is the part of a chat page that is for bots.
type botPage struct {
	height        heightTransition
	heightChat    int64
	shownKeyboard *model.ReplyKeyboard
	// keyboard is the chat's reply keyboard, and keyboardKey the message
	// that set it; hidden, by chat, is the one the account hid.
	keyboard    *model.ReplyKeyboard
	keyboardKey model.MessageKey
	// keyboardBot is the bot that set it, whose Mini Apps its buttons open.
	keyboardBot int64
	hidden      map[int64]model.MessageKey
	keys        [][]surface
	hide        surface
	start       surface
	// menu is the bot's menu button beside the composer's field.
	menu surface
	// commands are the rows of the commands menu.
	commands []surface
	// empty is set while the chat is a bot's with no messages in it.
	empty bool

	mu sync.Mutex
	// pressing are the callback buttons asked of the bot, by place, and
	// outcome what the last one came to, to be told on the next frame.
	pressing map[string]bool
	outcome  *botOutcome
}

type botOutcome struct {
	answer model.BotAnswer
	err    error
}

// updateBot works out what the chat's bot state is from the messages the
// page shows; history is the store's, for whether they end where the chat
// does.
func (p *chatPage) updateBot(c model.Chat, history model.History) {
	b := &p.bot
	b.keyboard, b.keyboardKey, b.keyboardBot = nil, model.MessageKey{}, 0
	if !history.HasNewer {
		b.keyboard, b.keyboardKey = model.ActiveKeyboard(p.messages)
		for i := len(p.messages) - 1; b.keyboard != nil && i >= 0; i-- {
			if p.messages[i].Key == b.keyboardKey {
				b.keyboardBot = p.botOf(c.ID, p.messages[i])
			}
		}
		if b.keyboard != nil && b.hidden[c.ID] == b.keyboardKey && !b.keyboard.Persistent {
			b.keyboard = nil
		}
	}
	b.empty = c.Kind == model.KindBot && len(p.messages) == 0 && !history.LoadingOlder && !history.HasOlder && history.Err == nil
	if b.keyboard != nil && b.keysDiffer() {
		b.keys = nil
		for _, row := range b.keyboard.Rows {
			b.keys = append(b.keys, make([]surface, len(row)))
		}
	}
}

// keysDiffer reports whether the keys' surfaces differ in shape from the
// keyboard's rows.
func (b *botPage) keysDiffer() bool {
	if len(b.keys) != len(b.keyboard.Rows) {
		return true
	}
	for i, row := range b.keyboard.Rows {
		if len(b.keys[i]) != len(row) {
			return true
		}
	}
	return false
}

// keyboardHeight is how much higher than its bar the composer is for the
// reply keyboard, with the gap over the bar of a floating one.
func (p *chatPage) keyboardHeight(gtx layout.Context, classic bool, page image.Point) int {
	b := &p.bot
	if b.heightChat != p.chat {
		b.height = heightTransition{}
		b.shownKeyboard = nil
		b.heightChat = p.chat
	}
	k := b.keyboard
	h := 0
	if k != nil && len(k.Rows) > 0 && !p.frozen.Frozen() {
		b.shownKeyboard = k
		rows := len(k.Rows)
		h = min(rows*gtx.Dp(keyboardButton)+(rows+1)*gtx.Dp(keyboardGap), page.Y*2/5)
		if !classic {
			h += gtx.Dp(replyGap)
		}
	}
	height := b.height.Value(gtx, h, true)
	if height == 0 && h == 0 {
		b.shownKeyboard = nil
	}
	return height
}

// layoutKeyboard draws the reply keyboard in rect, over the composer's bar,
// and sends the text of the key pressed.
func (p *chatPage) layoutKeyboard(gtx layout.Context, chat int64, rect image.Rectangle, classic bool, backdrop *blurBackdrop, l localization.Catalog) {
	b := &p.bot
	k := b.shownKeyboard
	if b.keyboard == nil {
		gtx = gtx.Disabled()
	}
	if k == nil || rect.Empty() {
		return
	}
	if b.keyboard != nil && b.hide.Clicked(gtx) {
		if b.hidden == nil {
			b.hidden = map[int64]model.MessageKey{}
		}
		b.hidden[chat] = b.keyboardKey
		gtx.Execute(op.InvalidateCmd{})
		return
	}
	inRect(gtx, rect, func(gtx layout.Context) layout.Dimensions {
		sc := scheme(gtx)
		size := gtx.Constraints.Max
		radius := gtx.Dp(16)
		if classic {
			radius = 0
		}
		defer clip.UniformRRect(image.Rectangle{Max: size}, radius).Push(gtx.Ops).Pop()
		overlayFill(gtx, backdrop, size, rect.Min, sc.SurfaceContainerHigh, radius)
		if classic {
			fillRect(gtx, sc.OutlineVariant, image.Pt(size.X, gtx.Dp(1)))
		}
		gap := gtx.Dp(keyboardGap)
		// The hide button takes a column at the end.
		closeW := gtx.Dp(36)
		keysW := size.X - closeW - gap
		rows := len(k.Rows)
		keyH := max((size.Y-(rows+1)*gap)/max(rows, 1), gtx.Dp(24))
		for y, row := range k.Rows {
			top := gap + y*(keyH+gap)
			if top+keyH > size.Y {
				break
			}
			width := (keysW - (len(row)+1)*gap) / max(len(row), 1)
			for x, btn := range row {
				left := gap + x*(width+gap)
				inRect(gtx, image.Rect(left, top, left+width, top+keyH), func(gtx layout.Context) layout.Dimensions {
					return p.layoutKey(gtx, chat, &b.keys[y][x], btn, l)
				})
			}
		}
		inRect(gtx, image.Rect(size.X-closeW, 0, size.X, size.Y), func(gtx layout.Context) layout.Dimensions {
			area := gtx.Constraints.Max
			d := min(gtx.Dp(32), area.X)
			circle := image.Rectangle{Max: image.Pt(d, d)}.Add(image.Pt((area.X-d)/2, gap))
			content := sc.SurfaceVariant.OnColor
			style := surfaceStyle{area: circle, radius: d / 2, background: content.SetOpacity(0), content: content, button: l.T("bot.hide_keyboard")}
			return b.hide.Layout(gtx, area, style, func(gtx layout.Context) layout.Dimensions {
				return offset(gtx, circle.Min.Add(image.Pt((d-gtx.Dp(20))/2, (d-gtx.Dp(20))/2)), func(gtx layout.Context) layout.Dimensions {
					return exact(gtx, image.Pt(gtx.Dp(20), gtx.Dp(20)), func(gtx layout.Context) layout.Dimensions { return iconClear(gtx, content) })
				})
			})
		})
		return layout.Dimensions{Size: size}
	})
}

// layoutKey draws one key of the reply keyboard and does what it is for.
func (p *chatPage) layoutKey(gtx layout.Context, chat int64, s *surface, btn model.MessageButton, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	size := gtx.Constraints.Max
	webView := (btn.Kind == "webview" || btn.Kind == "simple_webview") && p.openWebApp != nil && p.bot.keyboardBot != 0
	usable := btn.Kind == "text" && p.composer != nil || webView
	if usable && s.Clicked(gtx) {
		if webView {
			kind := model.WebViewInline
			if btn.Kind == "simple_webview" {
				kind = model.WebViewSimple
			}
			p.openWebApp(gtx, p, model.WebViewRequest{Kind: kind, Chat: chat, Bot: p.bot.keyboardBot, URL: btn.URL}, btn.Text)
		} else {
			p.composer.submit(chat, model.OutgoingMessage{Text: btn.Text})
		}
	}
	content, fill := sc.Primary.Color, sc.Primary.Color.SetOpacity(0.12)
	if !usable {
		content, fill = sc.SurfaceVariant.OnColor.SetOpacity(0.6), sc.SurfaceVariant.OnColor.SetOpacity(0.06)
	}
	style := surfaceStyle{radius: gtx.Dp(10), background: fill, content: content, button: btn.Text}
	return s.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints = layout.Exact(size)
		return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Min = image.Point{}
			gtx.Constraints.Max.X = max(size.X-gtx.Dp(12), 0)
			return centeredLabel(gtx, btn.Text, token.TypestyleLabelLarge, content, 1)
		})
	})
}

// layoutStart draws the Start button of an empty chat with a bot in rect,
// the composer's bar, and sends /start when it is pressed.
func (p *chatPage) layoutStart(gtx layout.Context, chat int64, rect image.Rectangle, classic bool, backdrop *blurBackdrop, l localization.Catalog) {
	b := &p.bot
	if b.start.Clicked(gtx) && p.composer != nil {
		p.composer.submit(chat, model.OutgoingMessage{Text: "/start"})
	}
	inRect(gtx, rect, func(gtx layout.Context) layout.Dimensions {
		sc := scheme(gtx)
		size := gtx.Constraints.Max
		radius := size.Y / 2
		if classic {
			radius = 0
		}
		defer clip.UniformRRect(image.Rectangle{Max: size}, radius).Push(gtx.Ops).Pop()
		overlayFill(gtx, backdrop, size, rect.Min, sc.SurfaceContainerHigh, radius)
		if classic {
			fillRect(gtx, sc.OutlineVariant, image.Pt(size.X, gtx.Dp(1)))
		}
		style := surfaceStyle{radius: radius, background: sc.Primary.Color.SetOpacity(0), content: sc.Primary.Color, button: l.T("bot.start")}
		return b.start.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(size)
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Point{}
				return label(gtx, strings.ToUpper(l.T("bot.start")), token.TypestyleLabelLargeEmphasized, sc.Primary.Color, 1)
			})
		})
	})
}

// pressButton does what the button b at row y and column x of message m is
// for: a callback button asks the bot in the background, a copy button copies
// its text.
func (p *chatPage) pressButton(gtx layout.Context, m model.Message, y, x int, b model.MessageButton, l localization.Catalog) {
	switch b.Kind {
	case "copy":
		gtx.Execute(clipboard.WriteCmd{Type: "application/text", Data: io.NopCloser(strings.NewReader(b.Copy))})
		p.toast.Show(l.T("info.copied"))
	case "callback":
		store, ok := p.source.(model.BotStore)
		if !ok {
			return
		}
		id := buttonID(m.Key, y, x)
		bp := &p.bot
		bp.mu.Lock()
		if bp.pressing[id] {
			bp.mu.Unlock()
			return
		}
		if bp.pressing == nil {
			bp.pressing = map[string]bool{}
		}
		bp.pressing[id] = true
		bp.mu.Unlock()
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), botWait)
			defer cancel()
			answer, err := store.PressButton(ctx, m.Key, b.Data)
			bp.mu.Lock()
			delete(bp.pressing, id)
			bp.outcome = &botOutcome{answer: answer, err: err}
			bp.mu.Unlock()
			p.invalidate()
		}()
	}
}

func buttonID(key model.MessageKey, y, x int) string {
	return fmt.Sprintf("%d/%d/%d/%d", key.ChatID, key.MessageID, y, x)
}

// pending reports whether the callback button at row y and column x of the
// message is waiting for its bot.
func (p *chatPage) pending(key model.MessageKey, y, x int) bool {
	p.bot.mu.Lock()
	defer p.bot.mu.Unlock()
	return p.bot.pressing[buttonID(key, y, x)]
}

// botUpdate tells what the last button pressed came to: the bot's notice, a
// link it asks to open, or why there was no answer.
func (p *chatPage) botUpdate(l localization.Catalog) {
	bp := &p.bot
	bp.mu.Lock()
	o := bp.outcome
	bp.outcome = nil
	bp.mu.Unlock()
	if o == nil {
		return
	}
	switch {
	case errors.Is(o.err, model.ErrBotSilent), errors.Is(o.err, context.DeadlineExceeded):
		p.toast.Show(l.T("bot.silent"))
	case errors.Is(o.err, model.ErrBotPassword):
		p.toast.Show(l.T("bot.password"))
	case o.err != nil:
		p.toast.Show(l.T("bot.failed"))
	default:
		if o.answer.Text != "" {
			p.toast.Show(o.answer.Text)
		}
		if o.answer.URL != "" {
			p.askLink(o.answer.URL)
		}
	}
}

// commandRows is how many commands the menu shows at most.
const commandRows = 6

// layoutCommands draws, over the composer, the commands of the bot the chat
// is with that the text being typed starts, as Telegram Desktop's command
// autocomplete does; a click sends the command. above is the top of the
// composer, and pad its margin.
func (p *chatPage) layoutCommands(gtx layout.Context, chat int64, size image.Point, pad, above int, l localization.Catalog) {
	c := p.composer
	source, ok := p.source.(model.BotInfoSource)
	if !ok || c == nil || p.kind != model.KindBot || p.bot.empty {
		return
	}
	d := c.draft(chat)
	text := d.editor.Text()
	if !strings.HasPrefix(text, "/") {
		return
	}
	matches := model.MatchCommands(source.BotInfo(chat).Commands, text)
	if len(matches) == 0 {
		return
	}
	matches = matches[:min(len(matches), commandRows)]
	b := &p.bot
	for len(b.commands) < len(matches) {
		b.commands = append(b.commands, surface{})
	}
	row := gtx.Dp(48)
	w := min(gtx.Dp(420), size.X-2*pad)
	h := len(matches)*row + 2*gtx.Dp(4)
	rect := image.Rect(pad, above-gtx.Dp(8)-h, pad+w, above-gtx.Dp(8))
	inRect(gtx, rect, func(gtx layout.Context) layout.Dimensions {
		sc := scheme(gtx)
		radius := gtx.Dp(12)
		menu := gtx.Constraints.Max
		defer clip.UniformRRect(image.Rectangle{Max: menu}, radius).Push(gtx.Ops).Pop()
		overlayFill(gtx, p.menuBackdrop(), menu, rect.Min, sc.SurfaceContainerHigh, radius)
		for i, cmd := range matches {
			top := gtx.Dp(4) + i*row
			inRect(gtx, image.Rect(0, top, menu.X, top+row), func(gtx layout.Context) layout.Dimensions {
				if b.commands[i].Clicked(gtx) {
					d.editor.SetText("")
					d.text = ""
					c.submit(chat, model.OutgoingMessage{Text: "/" + cmd.Command})
				}
				style := surfaceStyle{background: sc.Surface.OnColor.SetOpacity(0), content: sc.Surface.OnColor, button: "/" + cmd.Command}
				return b.commands[i].Layout(gtx, gtx.Constraints.Max, style, func(gtx layout.Context) layout.Dimensions {
					gtx.Constraints = layout.Exact(gtx.Constraints.Max)
					return layout.W.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min = image.Point{}
						return layout.Inset{Left: 16, Right: 16}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
							return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
								layout.Rigid(func(gtx layout.Context) layout.Dimensions {
									return label(gtx, "/"+cmd.Command, token.TypestyleBodyMediumEmphasized, sc.Primary.Color, 1)
								}),
								layout.Rigid(layout.Spacer{Width: 12}.Layout),
								layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
									gtx.Constraints.Min = image.Point{}
									return label(gtx, cmd.Description, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 1)
								}),
							)
						})
					})
				})
			})
		}
		return layout.Dimensions{Size: menu}
	})
}

// layoutBotMenu draws the menu button of the bot the chat is with in area,
// at its start, and opens the Mini App when it is pressed. It returns the
// width the button took, 0 for a chat that has none.
func (p *chatPage) layoutBotMenu(gtx layout.Context, chat int64, area image.Rectangle, l localization.Catalog) int {
	source, ok := p.source.(model.BotInfoSource)
	if !ok || p.kind != model.KindBot || p.openWebApp == nil || p.bot.empty {
		return 0
	}
	menu := source.BotInfo(chat).Menu
	if menu == nil {
		return 0
	}
	if p.bot.menu.Clicked(gtx) {
		p.openWebApp(gtx, p, model.WebViewRequest{Kind: model.WebViewMenu, Chat: chat, Bot: chat, URL: menu.URL}, menu.Text)
	}
	text := menu.Text
	if text == "" {
		text = l.T("bot.menu")
	}
	width := min(gtx.Dp(112), area.Dx()/3)
	height := min(gtx.Dp(32), area.Dy())
	rect := image.Rect(area.Min.X, area.Min.Y+(area.Dy()-height)/2, area.Min.X+width, area.Min.Y+(area.Dy()+height)/2)
	inRect(gtx, rect, func(gtx layout.Context) layout.Dimensions {
		sc := scheme(gtx)
		size := gtx.Constraints.Max
		style := surfaceStyle{radius: size.Y / 2, background: sc.Primary.Color.SetOpacity(0.12), content: sc.Primary.Color, button: text}
		return p.bot.menu.Layout(gtx, size, style, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints = layout.Exact(size)
			return layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
				gtx.Constraints.Min = image.Point{}
				gtx.Constraints.Max.X = max(size.X-gtx.Dp(16), 0)
				return label(gtx, text, token.TypestyleLabelLargeEmphasized, sc.Primary.Color, 1)
			})
		})
	})
	return width + gtx.Dp(4)
}

// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"errors"
	"image"
	"strings"
	"time"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tgerr"

	"gio-mw/token"
	"gio-mw/widget/button"
	"gio-mw/widget/scroll"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/account"
	"komarugram/internal/messenger/countries"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/login"
	"komarugram/internal/messenger/security"
)

const loginWidth = unit.Dp(420)

// protectEdge is how far the scrolling part of the offer to protect reaches
// past the card's content on each side, for the edges of its fields.
const protectEdge = unit.Dp(4)

// loginPage is the sign-in window: the phone number, then the code, then the
// 2FA password if the account has one. It shows what login.Login says and
// hands the answers back.
type loginPage struct {
	height heightTransition
	login  *login.Login
	// security draws the offer to protect local data, at StepProtect.
	security *securityView
	skip     *button.Button
	// install draws the offer to install the program, at StepInstall.
	install *installView
	// protect scrolls the text of the offer to protect local data.
	protect scroll.List

	phone             *phoneField
	code              *codeInput
	password          *textField
	next, back, retry *button.Button

	// seen is the Seq of the state the fields were last prepared for.
	seen int
}

func newLoginPage(l *login.Login, security *securityView, invalidate func()) *loginPage {
	return &loginPage{
		login:    l,
		security: security,
		install:  newInstallView(invalidate),
		skip:     button.Text(),
		protect:  scroll.List{List: layout.List{Axis: layout.Vertical}},
		phone:    newPhoneField(),
		code:     newCodeInput(),
		password: newTextField('•', ""),
		next:     button.Filled(),
		back:     button.Text(),
		retry:    button.Filled(),
	}
}

// done reports whether the account is signed in and the page has nothing
// more to show.
func (p *loginPage) done() bool {
	return p.login.State().Step == login.StepDone
}

// inputField is what the sign-in asks of the field of a step.
type inputField interface {
	Text() string
	Clear()
	Focus(gtx layout.Context)
	Submitted(gtx layout.Context) bool
}

// field returns the field of the step, nil for a step without one.
func (p *loginPage) field(step login.Step) inputField {
	switch step {
	case login.StepPhone:
		return p.phone
	case login.StepCode:
		return p.code
	case login.StepPassword:
		return p.password
	}
	return nil
}

// Update handles input for the current state.
func (p *loginPage) Update(gtx layout.Context, l localization.Catalog) {
	st := p.login.State()
	if st.Step == login.StepInstall {
		p.install.Update(gtx, p.login, l)
		return
	}
	p.code.setLength(st.CodeLength)
	field := p.field(st.Step)

	if st.Seq != p.seen {
		p.seen = st.Seq
		if field != nil {
			// A code or password that was refused is not worth keeping;
			// a phone number is, to be corrected.
			if st.Step != login.StepPhone {
				field.Clear()
			}
			field.Focus(gtx)
		}
	}

	if st.Step == login.StepProtect {
		p.security.UpdateSettings(gtx)
		p.security.FocusSetup(gtx)
		// Protection that is on now was just enabled here; its migration
		// has to finish before the account is added.
		enabled := p.security == nil || p.security.manager == nil
		if !enabled {
			state := p.security.manager.State()
			enabled = state.Enabled && state.Unlocked && !state.Busy
		}
		if p.skip.Clicked(gtx) || enabled {
			p.login.Continue()
		}
	}
	if p.back.Clicked(gtx) {
		p.login.Back()
	}
	if p.retry.Clicked(gtx) {
		p.login.Retry()
	}
	if st.Step == login.StepPhone {
		p.phone.Update(gtx)
	}
	submitted := field != nil && field.Submitted(gtx)
	if (p.next.Clicked(gtx) || submitted) && field != nil && canSubmit(st, field.Text()) {
		p.login.Submit(field.Text())
	}
}

// canSubmit reports whether text is worth sending as the answer to st.
func canSubmit(st login.State, text string) bool {
	if st.Busy {
		return false
	}
	if st.Step == login.StepPassword {
		return text != ""
	}
	if st.Step == login.StepPhone {
		// A plus alone is no number.
		return countries.Digits(text) != ""
	}
	if st.Step == login.StepCode && st.CodeLength > 0 && st.CodeLength <= codeMaxLength {
		// The code has as many digits as it was said to have.
		return len(countries.Digits(text)) == st.CodeLength
	}
	return strings.TrimSpace(text) != ""
}

// Layout draws the sign-in card centered on the window.
// With private set, the phone number the code went to is not shown.
func (p *loginPage) Layout(gtx layout.Context, l localization.Catalog, private bool) layout.Dimensions {
	sc := scheme(gtx)
	size := gtx.Constraints.Max
	fillRect(gtx, sc.SurfaceContainerLow, size)

	st := p.login.State()
	layout.Center.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
		gtx.Constraints.Min = layout.Constraints{}.Min
		return layout.UniformInset(16).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			gtx.Constraints.Max.X = min(gtx.Constraints.Max.X, gtx.Dp(loginWidth))
			return p.height.Card(gtx, func(gtx layout.Context) layout.Dimensions {
				switch st.Step {
				case login.StepProtect:
					return p.layoutProtect(gtx, l)
				case login.StepInstall:
					return p.install.Layout(gtx, l)
				}
				if private {
					st.Phone = ""
				}
				return p.layoutStep(gtx, st, l)
			}, defaultCardPadding)
		})
	})
	if st.Step == login.StepPhone {
		p.phone.Dialog(gtx, l)
	} else {
		p.phone.picker.modal.Hide()
	}
	return layout.Dimensions{Size: size}
}

func (p *loginPage) layoutStep(gtx layout.Context, st login.State, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	title, body := loginTexts(st, l)
	problem := loginProblem(st.Problem, l)
	field := p.field(st.Step)

	rows := []layout.FlexChild{
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, title, token.TypestyleHeadlineSmall, sc.Surface.OnColor, 0)
		}),
	}
	if body != "" {
		rows = append(rows, vspace(8), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, body, token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}))
	}
	if field != nil {
		title := l.T(map[login.Step]string{
			login.StepPhone:    "login.field_phone",
			login.StepCode:     "login.field_code",
			login.StepPassword: "login.field_password",
		}[st.Step])
		rows = append(rows, vspace(20), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			switch f := field.(type) {
			case *phoneField:
				return f.Layout(gtx, l, title, problem != "")
			case *codeInput:
				f.setLength(st.CodeLength)
				return f.Layout(gtx, title, problem != "")
			}
			return field.(*textField).Layout(gtx, title, problem != "")
		}))
	}
	if problem != "" {
		color := sc.Error.Color
		if st.Step == login.StepFailed {
			color = sc.Surface.OnColor
		}
		rows = append(rows, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, problem, token.TypestyleBodyMedium, color, 0)
		}))
	}

	switch st.Step {
	case login.StepPhone, login.StepCode, login.StepPassword:
		text := ""
		if field != nil {
			text = field.Text()
		}
		if canSubmit(st, text) {
			p.next.Enable()
		} else {
			p.next.Disable()
		}
		nextLabel := l.T("login.next")
		switch {
		case st.Busy:
			nextLabel = l.T("login.checking")
		case st.Step != login.StepPhone:
			nextLabel = l.T("login.sign_in")
		}
		rows = append(rows, vspace(20), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if st.Step == login.StepPhone {
						return layout.Dimensions{}
					}
					return p.back.Layout(gtx, l.T("login.back"))
				}),
				layout.Flexed(1, func(gtx layout.Context) layout.Dimensions {
					return layout.Dimensions{Size: gtx.Constraints.Min}
				}),
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return p.next.Layout(gtx, nextLabel)
				}),
			)
		}))
	case login.StepFailed:
		rows = append(rows, vspace(20), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Spacing: layout.SpaceStart}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					return p.retry.Layout(gtx, l.T("login.retry"))
				}),
			)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// layoutProtect offers to protect local data before the account is added.
// The title and the buttons stay in sight, and what lies between them, the
// text and the form, scrolls where the window is too low to show all of it.
func (p *loginPage) layoutProtect(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	form, buttons := p.security.setupParts(gtx, l, func(gtx layout.Context) layout.Dimensions {
		return p.skip.Layout(gtx, l.T("protect.skip"))
	})
	// part lays a widget out as it is, to place it once the room is known.
	part := func(w layout.Widget) (op.CallOp, image.Point) {
		gtx := gtx
		gtx.Constraints.Min = image.Point{}
		macro := op.Record(gtx.Ops)
		dims := w(gtx)
		return macro.Stop(), dims.Size
	}
	titleCall, titleSize := part(func(gtx layout.Context) layout.Dimensions {
		return label(gtx, l.T("protect.title"), token.TypestyleHeadlineSmall, sc.Surface.OnColor, 0)
	})
	var buttonsCall op.CallOp
	var buttonsSize image.Point
	if buttons != nil {
		buttonsCall, buttonsSize = part(buttons)
	}
	gap := gtx.Dp(8)
	room := max(gtx.Constraints.Max.Y-titleSize.Y-gap-buttonsSize.Y, 0)

	// The list clips what is outside it, and the outline of a field is drawn
	// on its edge, half of it outside: the list reaches a little past the
	// card's content, into its padding, and what it holds is kept inside.
	edge := gtx.Dp(protectEdge)
	listGtx := gtx
	listGtx.Constraints.Max.X += 2 * edge
	listGtx.Constraints.Min = image.Pt(listGtx.Constraints.Max.X, 0)
	listGtx.Constraints.Max.Y = room
	macro := op.Record(gtx.Ops)
	listDims := p.protect.Layout(listGtx, 1, func(gtx layout.Context, _ int) layout.Dimensions {
		rows := []layout.FlexChild{
			layout.Rigid(func(gtx layout.Context) layout.Dimensions {
				body := "protect.body"
				if p.security != nil && p.security.manager != nil {
					body = passwordOnly(p.security.manager.State(), body)
				}
				return label(gtx, l.T(body), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
			}),
			vspace(20),
		}
		return layout.Inset{Left: protectEdge, Right: protectEdge}.Layout(gtx, func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Axis: layout.Vertical}.Layout(gtx, append(rows, form...)...)
		})
	})
	listCall := macro.Stop()

	place := func(x, y int, call op.CallOp, size image.Point) {
		offset(gtx, image.Pt(x, y), func(gtx layout.Context) layout.Dimensions {
			call.Add(gtx.Ops)
			return layout.Dimensions{Size: size}
		})
	}
	place(0, 0, titleCall, titleSize)
	place(-edge, titleSize.Y+gap, listCall, listDims.Size)
	bottom := titleSize.Y + gap + listDims.Size.Y
	if buttons != nil {
		place(0, bottom, buttonsCall, buttonsSize)
		bottom += buttonsSize.Y
	}
	return layout.Dimensions{Size: image.Pt(gtx.Constraints.Max.X, bottom)}
}

// loginTexts returns the title and the explanation of a step.
func loginTexts(st login.State, l localization.Catalog) (title, body string) {
	switch st.Step {
	case login.StepPhone:
		return l.T("login.title_phone"), l.T("login.body_phone")
	case login.StepCode:
		body = l.T(map[account.CodeKind]string{
			account.CodeApp:  "login.sent_app",
			account.CodeSMS:  "login.sent_sms",
			account.CodeCall: "login.sent_call",
		}[st.Delivery])
		if body == "" {
			body = l.T("login.sent")
		}
		if st.Phone != "" {
			body += " " + l.Format("login.number", map[string]string{"phone": st.Phone})
		}
		if st.CodeLength > 0 {
			body += " " + l.Count("login.code_length", st.CodeLength, nil)
		}
		return l.T("login.title_code"), body
	case login.StepPassword:
		body = l.T("login.body_password")
		if st.Hint != "" {
			body += " " + l.Format("login.hint", map[string]string{"password_hint": st.Hint})
		}
		return l.T("login.title_password"), body
	case login.StepFailed:
		return l.T("login.title_failed"), ""
	}
	return l.T("login.title_connecting"), l.T("login.body_connecting")
}

// loginProblem says what went wrong with a sign-in; empty for no error.
func loginProblem(err error, l localization.Catalog) string {
	if err == nil {
		return ""
	}
	if wait, ok := tgerr.AsFloodWait(err); ok {
		return l.Format("login.flood_wait", map[string]string{"time": waitText(wait, l)})
	}
	key := ""
	switch {
	case errors.Is(err, account.ErrNoConnection):
		key = "login.no_connection"
	case errors.Is(err, account.ErrAccountExists):
		key = "login.account_exists"
	case errors.Is(err, security.ErrLocked):
		key = "login.locked"
	case errors.Is(err, account.ErrInUse):
		key = "login.in_use"
	case errors.Is(err, auth.ErrPasswordInvalid):
		key = "login.password_invalid"
	case errors.Is(err, account.ErrNoAccount):
		key = "login.no_account"
	case errors.Is(err, account.ErrCodeUnsupported):
		key = "login.code_unsupported"
	case tgerr.Is(err, "PHONE_NUMBER_INVALID"):
		key = "login.phone_invalid"
	case tgerr.Is(err, "PHONE_NUMBER_BANNED"):
		key = "login.phone_banned"
	case tgerr.Is(err, "PHONE_CODE_INVALID", "PHONE_CODE_EMPTY"):
		key = "login.code_invalid"
	case tgerr.Is(err, "PHONE_CODE_EXPIRED"):
		key = "login.code_expired"
	case tgerr.Is(err, "PHONE_NUMBER_FLOOD", "PHONE_PASSWORD_FLOOD"):
		key = "login.flood"
	case tgerr.Is(err, "API_ID_INVALID", "API_ID_PUBLISHED_FLOOD"):
		key = "login.api_id"
	default:
		return l.Format("login.failed", map[string]string{"error": err.Error()})
	}
	return l.T(key)
}

// waitText is how long to wait, rounded up to seconds, minutes or hours.
func waitText(d time.Duration, l localization.Catalog) string {
	switch secs := int(d.Round(time.Second) / time.Second); {
	case secs < 60:
		return l.Count("duration.seconds", max(secs, 1), nil)
	case secs < 3600:
		return l.Count("duration.minutes", (secs+59)/60, nil)
	default:
		return l.Count("duration.hours", (secs+3599)/3600, nil)
	}
}

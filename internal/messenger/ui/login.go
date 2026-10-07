// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"strings"

	"gio-mw/token"
	"gio-mw/widget/button"

	"gioui.org/layout"
	"gioui.org/unit"

	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/login"
)

const loginWidth = unit.Dp(420)

// loginPage is the sign-in window: the phone number, then the code, then the
// 2FA password if the account has one. It shows what login.Login says and
// hands the answers back.
type loginPage struct {
	height heightTransition
	login  *login.Login
	// security draws the offer to protect local data, at StepProtect.
	security *securityView
	skip     *button.Button

	phone, code, password *textField
	next, back, retry     *button.Button

	// seen is the Seq of the state the fields were last prepared for.
	seen int
}

func newLoginPage(l *login.Login, security *securityView) *loginPage {
	return &loginPage{
		login:    l,
		security: security,
		skip:     button.Text(),
		phone:    newTextField(0, "0123456789+-() "),
		code:     newTextField(0, "0123456789 "),
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

// field returns the field of the step, nil for a step without one.
func (p *loginPage) field(step login.Step) *textField {
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
func (p *loginPage) Update(gtx layout.Context) {
	st := p.login.State()
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
				if st.Step == login.StepProtect {
					return p.layoutProtect(gtx, l)
				}
				if private {
					st.Phone = ""
				}
				return p.layoutStep(gtx, st)
			}, defaultCardPadding)
		})
	})
	return layout.Dimensions{Size: size}
}

func (p *loginPage) layoutStep(gtx layout.Context, st login.State) layout.Dimensions {
	sc := scheme(gtx)
	title, body := loginTexts(st)
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
		title := map[login.Step]string{
			login.StepPhone:    "Номер телефона",
			login.StepCode:     "Код",
			login.StepPassword: "Пароль",
		}[st.Step]
		rows = append(rows, vspace(20), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return field.Layout(gtx, title, st.Problem != "")
		}))
	}
	if st.Problem != "" {
		color := sc.Error.Color
		if st.Step == login.StepFailed {
			color = sc.Surface.OnColor
		}
		rows = append(rows, vspace(12), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, st.Problem, token.TypestyleBodyMedium, color, 0)
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
		nextLabel := "Далее"
		switch {
		case st.Busy:
			nextLabel = "Проверяем…"
		case st.Step != login.StepPhone:
			nextLabel = "Войти"
		}
		rows = append(rows, vspace(20), layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return layout.Flex{Alignment: layout.Middle}.Layout(gtx,
				layout.Rigid(func(gtx layout.Context) layout.Dimensions {
					if st.Step == login.StepPhone {
						return layout.Dimensions{}
					}
					return p.back.Layout(gtx, "Назад")
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
					return p.retry.Layout(gtx, "Повторить")
				}),
			)
		}))
	}
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx, rows...)
}

// layoutProtect offers to protect local data before the account is added.
func (p *loginPage) layoutProtect(gtx layout.Context, l localization.Catalog) layout.Dimensions {
	sc := scheme(gtx)
	return layout.Flex{Axis: layout.Vertical}.Layout(gtx,
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("protect.title"), token.TypestyleHeadlineSmall, sc.Surface.OnColor, 0)
		}),
		vspace(8),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return label(gtx, l.T("protect.body"), token.TypestyleBodyMedium, sc.SurfaceVariant.OnColor, 0)
		}),
		vspace(20),
		layout.Rigid(func(gtx layout.Context) layout.Dimensions {
			return p.security.SetupLayout(gtx, l, func(gtx layout.Context) layout.Dimensions {
				return p.skip.Layout(gtx, l.T("protect.skip"))
			})
		}),
	)
}

// loginTexts returns the title and the explanation of a step.
func loginTexts(st login.State) (title, body string) {
	switch st.Step {
	case login.StepPhone:
		return "Вход в Telegram", "Введите номер телефона в международном формате, например +7 900 123-45-67. На него придёт код."
	case login.StepCode:
		body = st.Delivery
		if st.Phone != "" {
			body += " Номер: " + st.Phone + "."
		}
		if st.CodeLength > 0 {
			body += fmt.Sprintf(" В коде %d %s.", st.CodeLength, plural(st.CodeLength, "цифра", "цифры", "цифр"))
		}
		return "Введите код", body
	case login.StepPassword:
		body = "Аккаунт защищён облачным паролем (двухфакторная аутентификация). Введите его."
		if st.Hint != "" {
			body += " Подсказка: " + st.Hint + "."
		}
		return "Облачный пароль", body
	case login.StepFailed:
		return "Нет подключения", ""
	}
	return "Подключение…", "Проверяем сохранённый сеанс."
}

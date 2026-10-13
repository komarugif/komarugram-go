// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"fmt"
	"testing"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tgerr"

	"komarugram/internal/messenger/account"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/login"
)

func TestLoginProblem(t *testing.T) {
	for _, c := range []struct {
		lang string
		err  error
		want string
	}{
		{"ru", nil, ""},
		{"ru", tgerr.New(400, "PHONE_NUMBER_INVALID"), "Неверный номер телефона."},
		{"ru", tgerr.New(400, "PHONE_CODE_EXPIRED"), "Код устарел. Запросите новый."},
		{"ru", fmt.Errorf("%w: locked", account.ErrInUse), "Этот сеанс уже открыт в другом окне приложения."},
		{"ru", auth.ErrPasswordInvalid, "Неверный пароль."},
		{"ru", tgerr.New(420, "FLOOD_WAIT_30"), "Слишком много попыток. Подождите 30 секунд."},
		{"ru", tgerr.New(420, "FLOOD_WAIT_600"), "Слишком много попыток. Подождите 10 минут."},
		{"en", tgerr.New(420, "FLOOD_WAIT_60"), "Too many attempts. Wait 1 minute."},
		{"en", account.ErrNoAccount, "There is no Telegram account for this number. Signing up is not supported here."},
		{"en", fmt.Errorf("dial: refused"), "Could not connect: dial: refused"},
	} {
		if got := loginProblem(c.err, localization.For(c.lang)); got != c.want {
			t.Errorf("%s %v: %q, want %q", c.lang, c.err, got, c.want)
		}
	}
}

func TestLoginTexts(t *testing.T) {
	st := login.State{Step: login.StepCode, Delivery: account.CodeSMS, Phone: "+79001234567", CodeLength: 5}
	if _, body := loginTexts(st, localization.For("ru")); body != "Код отправлен в SMS. Номер: +79001234567. В коде 5 цифр." {
		t.Errorf("ru: %q", body)
	}
	if _, body := loginTexts(st, localization.For("en")); body != "The code was sent by SMS. Number: +79001234567. The code has 5 digits." {
		t.Errorf("en: %q", body)
	}
}

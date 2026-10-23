// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"image"
	"testing"
	"time"

	"gio-mw/defaults"
	"gio-mw/defaults/schemes"
	"gio-mw/wdk"

	"gioui.org/layout"
	"gioui.org/op"
	"gioui.org/unit"

	"komarugram/internal/messenger/countries"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/login"
)

func phoneContext() layout.Context {
	return layout.Context{
		Ops:         new(op.Ops),
		Now:         time.Now(),
		Constraints: layout.Exact(image.Pt(400, 300)),
		Metric:      unit.Metric{PxPerDp: 1, PxPerSp: 1},
	}
}

// typeInto types s into the field, a key at a time, a frame after each.
func typeInto(f *phoneField, gtx layout.Context, s string) {
	for _, r := range s {
		f.editor.Insert(string(r))
		f.Submitted(gtx)
	}
}

// caretOf is where the field's caret is.
func caretOf(f *phoneField) int {
	caret, _ := f.editor.Selection()
	return caret
}

func TestPhoneFieldGroupsWhatIsTyped(t *testing.T) {
	gtx := phoneContext()
	f := newPhoneField()
	typeInto(f, gtx, "79001234567")
	if got, want := f.Text(), "+7 900 123 45 67"; got != want {
		t.Fatalf("typed digits: %q, want %q", got, want)
	}
	if caretOf(f) != len([]rune(f.Text())) {
		t.Errorf("caret at %d, want the end", caretOf(f))
	}
	if f.Digits() != "79001234567" {
		t.Errorf("digits %q", f.Digits())
	}
}

func TestPhoneFieldTakesAPastedNumber(t *testing.T) {
	gtx := phoneContext()
	f := newPhoneField()
	f.editor.Insert("+1 (234) 567-8910")
	f.Submitted(gtx)
	if got, want := f.Text(), "+1 234 567 8910"; got != want {
		t.Errorf("pasted: %q, want %q", got, want)
	}
	// Too many digits are cut where a number ends.
	f.editor.SetText("")
	f.Submitted(gtx)
	f.editor.Insert("7900123456789012345678")
	f.Submitted(gtx)
	if n := len(f.Digits()); n != countries.MaxDigits {
		t.Errorf("%d digits, want %d", n, countries.MaxDigits)
	}
}

func TestPhoneFieldBackspaceTakesTheDigitBesideASeparator(t *testing.T) {
	gtx := phoneContext()
	f := newPhoneField()
	typeInto(f, gtx, "7900123")
	// The caret is after the space that follows 900.
	f.editor.SetCaret(7, 7)
	f.Submitted(gtx)
	f.editor.Delete(-1)
	f.Submitted(gtx)
	if got, want := f.Text(), "+7 901 23"; got != want {
		t.Fatalf("after Backspace: %q, want %q", got, want)
	}
	// The caret stays where the digit was.
	if got, want := caretOf(f), 5; got != want {
		t.Errorf("caret at %d, want %d", got, want)
	}
}

func TestPhoneFieldDeleteTakesTheDigitBesideASeparator(t *testing.T) {
	gtx := phoneContext()
	f := newPhoneField()
	typeInto(f, gtx, "7900123")
	// The caret is before the space that follows 900.
	f.editor.SetCaret(6, 6)
	f.Submitted(gtx)
	f.editor.Delete(1)
	f.Submitted(gtx)
	if got, want := f.Text(), "+7 900 23"; got != want {
		t.Fatalf("after Delete: %q, want %q", got, want)
	}
}

func TestPhoneFieldEditsInTheMiddleKeepTheCaret(t *testing.T) {
	gtx := phoneContext()
	f := newPhoneField()
	typeInto(f, gtx, "79001234567")
	// Between 123 and 45, a digit is typed.
	f.editor.SetCaret(11, 11)
	f.Submitted(gtx)
	typeInto(f, gtx, "0")
	if got, want := f.Text(), "+7 900 123 04 567"; got != want {
		t.Fatalf("inserted: %q, want %q", got, want)
	}
	if got, want := caretOf(f), 12; got != want {
		t.Errorf("caret at %d, want %d", got, want)
	}
}

func TestPhoneFieldPickedCountryStaysTheCountryOfItsCode(t *testing.T) {
	gtx := phoneContext()
	f := newPhoneField()
	f.pick(gtx, "KZ")
	if f.Text() != "+7" || f.chosen != "KZ" {
		t.Fatalf("picked Kazakhstan: %q, %q", f.Text(), f.chosen)
	}
	typeInto(f, gtx, "9")
	c, ok := countries.Match(f.Digits(), f.chosen)
	if !ok || c.ISO != "KZ" {
		t.Errorf("country of %q is %q, want KZ", f.Digits(), c.ISO)
	}
	// Another code is another country, whatever was picked.
	f.editor.SetText("")
	f.Submitted(gtx)
	typeInto(f, gtx, "380")
	if c, _ := countries.Match(f.Digits(), f.chosen); c.ISO != "UA" {
		t.Errorf("country of %q is %q, want UA", f.Digits(), c.ISO)
	}
}

func TestPhoneStepNeedsADigit(t *testing.T) {
	st := login.State{Step: login.StepPhone}
	if canSubmit(st, "+") {
		t.Error("a plus alone is a number")
	}
	if !canSubmit(st, "+7") {
		t.Error("+7 is not")
	}
}

func TestEveryCountryHasANameInEveryLanguage(t *testing.T) {
	for _, lang := range []string{"en", "ru"} {
		l := localization.For(lang)
		for _, c := range countries.All() {
			key := "country." + c.ISO
			if l.T(key) == key {
				t.Errorf("%s has no name in %s", c.ISO, lang)
			}
		}
	}
}

func TestCountryPickerSearch(t *testing.T) {
	l := localization.For("ru")
	p := newCountryPicker()
	p.refresh(l)
	if len(p.shown) != len(countries.All()) {
		t.Fatalf("%d countries listed, want all %d", len(p.shown), len(countries.All()))
	}
	// A name is found as the language spells it, and as English does; a code
	// with its plus or without it.
	for query, want := range map[string]string{
		"герм": "DE", "Germ": "DE", "+49": "DE", "49": "DE", "казах": "KZ", "kazakh": "KZ", "  россия ": "RU",
	} {
		p.search.SetText(query)
		p.refresh(l)
		found := false
		for _, e := range p.shown {
			found = found || e.iso == want
		}
		if !found {
			t.Errorf("%q does not find %s, found %d countries", query, want, len(p.shown))
		}
	}
	p.search.SetText("zzz")
	p.refresh(l)
	if len(p.shown) != 0 {
		t.Errorf("%q finds %d countries", "zzz", len(p.shown))
	}
}

// TestPasswordEyeShowsWhatIsTypedWhileItIsHeld checks that the eye of a new
// password shows it for as long as it is held down, and that the fields that
// ask for a password already chosen have none.
func TestPasswordEyeShowsWhatIsTypedWhileItIsHeld(t *testing.T) {
	view := newSecurityView(nil, func() {})
	if view.master.peek == nil || view.confirm.peek == nil {
		t.Fatal("the fields of a new master password have no eye")
	}
	if view.unlock.peek != nil || view.decryptPassword.peek != nil {
		t.Error("a password already chosen has an eye")
	}
	gtx := phoneContext()
	gtx.Values = map[string]any{}
	wdk.InitMaterialThemeInContext(gtx, defaults.NewTheme(gtx, schemes.SchemeBaselineLight()))
	field := view.master
	for _, held := range []bool{false, true, false} {
		field.peek.down = held
		field.Layout(gtx, "Master password", false)
		want := '•'
		if held {
			want = 0
		}
		if field.editor.Mask != want {
			t.Errorf("held %v: mask %q, want %q", held, field.editor.Mask, want)
		}
	}
}

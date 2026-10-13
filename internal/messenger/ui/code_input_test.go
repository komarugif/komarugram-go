// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gioui.org/layout"

	"komarugram/internal/messenger/account"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/login"
)

// typeCode types s into the field, a digit at a time, a frame after each, and
// reports whether any of the frames said the code was to be taken.
func typeCode(c *codeInput, gtx layout.Context, s string) (taken bool) {
	for _, r := range s {
		c.boxes.Insert(string(r))
		if c.Submitted(gtx) {
			taken = true
		}
	}
	return taken
}

func TestCodeInputTakesTheCodeWhenItIsAllThere(t *testing.T) {
	gtx := phoneContext()
	c := newCodeInput()
	c.setLength(5)
	if typeCode(c, gtx, "1234") {
		t.Fatal("a code of four digits was taken, of five")
	}
	if !typeCode(c, gtx, "5") || c.Text() != "12345" {
		t.Fatalf("the last digit did not take the code: %q", c.Text())
	}
	// No digit more than the code has.
	if typeCode(c, gtx, "6") || c.Text() != "12345" {
		t.Errorf("a sixth digit: %q", c.Text())
	}
	// A digit taken away, and typed again, is the code again.
	c.boxes.Delete(-1)
	if c.Submitted(gtx) || c.Text() != "1234" {
		t.Fatalf("after a digit was deleted: %q", c.Text())
	}
	if !typeCode(c, gtx, "9") || c.Text() != "12349" {
		t.Errorf("a digit typed again: %q", c.Text())
	}
}

func TestCodeInputTakesAPastedCodeAndOnlyDigits(t *testing.T) {
	gtx := phoneContext()
	c := newCodeInput()
	c.setLength(5)
	c.boxes.Insert("1a2 b3")
	if c.Submitted(gtx) || c.Text() != "123" {
		t.Fatalf("letters and spaces were kept: %q", c.Text())
	}
	c.Clear()
	c.Submitted(gtx)
	c.boxes.Insert("12 345")
	if !c.Submitted(gtx) || c.Text() != "12345" {
		t.Errorf("a code pasted with a space: %q", c.Text())
	}
	// Longer than the code, it is cut.
	c.Clear()
	c.Submitted(gtx)
	c.boxes.Insert("9876543")
	c.Submitted(gtx)
	if c.Text() != "98765" {
		t.Errorf("a long paste: %q", c.Text())
	}
}

func TestCodeInputKeepsTheCaretAfterTheLastDigit(t *testing.T) {
	gtx := phoneContext()
	c := newCodeInput()
	c.setLength(5)
	typeCode(c, gtx, "123")
	// A click, or a key, put it before the second digit.
	c.boxes.SetCaret(1, 1)
	c.Submitted(gtx)
	if start, end := c.boxes.Selection(); start != 3 || end != 3 {
		t.Errorf("the caret is at %d, %d, want after the last digit", start, end)
	}
	// What is selected stays selected, to be replaced.
	c.boxes.SetCaret(0, 3)
	c.Submitted(gtx)
	if start, end := c.boxes.Selection(); start == end {
		t.Error("the selection was lost")
	}
}

func TestCodeInputWithoutALengthIsAPlainField(t *testing.T) {
	gtx := phoneContext()
	c := newCodeInput()
	c.setLength(0)
	if c.boxed() {
		t.Fatal("boxes for a code of no length")
	}
	c.plain.editor.Insert("12345")
	if c.Submitted(gtx) || c.Text() != "12345" {
		t.Errorf("a plain field: %q", c.Text())
	}
	// A length that no sign-in has is no length.
	c.setLength(codeMaxLength + 1)
	if c.boxed() {
		t.Error("boxes for a code too long to have them")
	}
}

func TestCodeIsToBeCompleteToBeSent(t *testing.T) {
	st := login.State{Step: login.StepCode, CodeLength: 5}
	if canSubmit(st, "1234") || !canSubmit(st, "12345") {
		t.Error("a code of a known length is sent short, or not sent whole")
	}
	st.Busy = true
	if canSubmit(st, "12345") {
		t.Error("a code is sent while the last is being checked")
	}
	// Where the length is not told, anything typed will do.
	if !canSubmit(login.State{Step: login.StepCode}, "123") {
		t.Error("a code of an unknown length is not sent")
	}
	// The cloud password is whatever the user types.
	if !canSubmit(login.State{Step: login.StepPassword, CodeLength: 5}, "x") {
		t.Error("the password is not sent")
	}
}

// TestRenderCodeStep draws the code step with none of its digits, with some,
// with all, and refused, in ACCOUNTS_PNG_DIR.
func TestRenderCodeStep(t *testing.T) {
	dir := os.Getenv("ACCOUNTS_PNG_DIR")
	if dir == "" {
		t.Skip("set ACCOUNTS_PNG_DIR to a directory")
	}
	l := localization.For("ru")
	for name, scene := range map[string]struct {
		digits  string
		refused bool
		length  int
	}{
		"code-empty": {}, "code-2": {digits: "12"}, "code-full": {digits: "12345"},
		"code-refused": {digits: "12345", refused: true}, "code-6": {digits: "123", length: 6}, "code-unknown": {digits: "123", length: -1},
	} {
		signIn := login.New(nil)
		ctx, cancel := context.WithCancel(context.Background())
		length := 5
		switch {
		case scene.length > 0:
			length = scene.length
		case scene.length < 0:
			length = 0
		}
		var problem error
		if scene.refused {
			problem = account.ErrCodeUnsupported
		}
		go signIn.Code(ctx, account.CodeInfo{Kind: account.CodeApp, Length: length}, problem)
		for signIn.State().Step != login.StepCode {
			time.Sleep(time.Millisecond)
		}
		page := newLoginPage(signIn, nil, nil)
		frame := 0
		renderPNG(t, filepath.Join(dir, name+".png"), image.Pt(900, 560), 4, func(gtx layout.Context) {
			frame++
			page.Update(gtx, l)
			if frame == 1 {
				// The page clears the field when the step comes.
				page.code.boxes.SetText(scene.digits)
				page.code.plain.editor.SetText(scene.digits)
			}
			page.Layout(gtx, l, false)
		})
		cancel()
	}
}

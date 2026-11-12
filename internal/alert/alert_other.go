// SPDX-License-Identifier: Unlicense OR MIT

//go:build !windows

package alert

import (
	"errors"
	"komarugram/pkg/program"
	"os/exec"
	"runtime"
	"strings"
	"unicode"
)

// show asks the first program there is for the box.
func show(title, text string) bool {
	for _, c := range commands(runtime.GOOS, title, text) {
		path, err := exec.LookPath(c[0])
		if err != nil {
			continue
		}
		// The dialog's exit status tells how it was closed, not whether it
		// was shown: a program that ran has shown it.
		err = program.Command(path, c[1:]...).Run()
		if exited := new(exec.ExitError); err == nil || errors.As(err, &exited) {
			return true
		}
	}
	return false
}

// commands lists the programs that can show the box on goos, in the order
// they are tried: the desktop's own dialogs, as the file chooser does, then
// yad, then X11's xmessage.
func commands(goos, title, text string) [][]string {
	if goos == "darwin" {
		return [][]string{{"osascript",
			"-e", "on run argv",
			"-e", "display alert (item 1 of argv) message (item 2 of argv) as critical",
			"-e", "end run", title, text}}
	}
	return [][]string{
		{"kdialog", "--title", title, "--error", text},
		{"zenity", "--error", "--no-markup", "--title", title, "--text", text},
		{"yad", "--title", title, "--window-icon", "dialog-error", "--image", "dialog-error",
			"--text", text, "--no-markup", "--selectable-labels", "--button", "yad-ok:0", "--center", "--fixed"},
		{"xmessage", "-center", latin1(title + "\n\n" + text)},
	}
}

// latin1 turns s into the ISO 8859-1 bytes xmessage draws. Xaw draws a
// label's bytes with the core font, which is ISO 8859-1 unless a font set
// is asked for, and a font set needs an X core font with the text's
// characters: Xwayland and most X servers of today have only the built-in
// "fixed", so UTF-8 came out as mojibake. What ISO 8859-1 lacks is spelt
// in its letters, so that every font shows it: Cyrillic transliterated,
// typographic punctuation in its ASCII form, the rest as "?".
func latin1(s string) string {
	b := make([]byte, 0, len(s))
	for _, r := range []rune(s) {
		switch {
		case r <= 0xff:
			b = append(b, byte(r))
		case translit[unicode.ToLower(r)] != "":
			t := translit[unicode.ToLower(r)]
			if unicode.IsUpper(r) {
				t = strings.ToUpper(t[:1]) + t[1:]
			}
			b = append(b, t...)
		case unicode.Is(unicode.Cyrillic, r):
			// Hard and soft signs have no letter of their own.
		default:
			b = append(b, '?')
		}
	}
	return string(b)
}

var translit = map[rune]string{
	'а': "a", 'б': "b", 'в': "v", 'г': "g", 'д': "d", 'е': "e", 'ё': "yo",
	'ж': "zh", 'з': "z", 'и': "i", 'й': "y", 'к': "k", 'л': "l", 'м': "m",
	'н': "n", 'о': "o", 'п': "p", 'р': "r", 'с': "s", 'т': "t", 'у': "u",
	'ф': "f", 'х': "kh", 'ц': "ts", 'ч': "ch", 'ш': "sh", 'щ': "shch",
	'ы': "y", 'э': "e", 'ю': "yu", 'я': "ya",
	'є': "ye", 'і': "i", 'ї': "yi", 'ґ': "g", 'ў': "u",

	'‐': "-", '‑': "-", '–': "-", '—': "-", '−': "-", '…': "...",
	'‘': "'", '’': "'", '‚': "'", '“': "\"", '”': "\"", '„': "\"",
	' ': " ", ' ': " ", '•': "*",
}

// SPDX-License-Identifier: Unlicense OR MIT

package savefile

import (
	"bytes"
	"testing"
)

func TestSafeName(t *testing.T) {
	for in, want := range map[string]string{
		"report.pdf":            "report.pdf",
		"../../etc/passwd":      ".._.._etc_passwd",
		`a\b:c*d?"e"<f>|g`:      "a_b_c_d__e__f__g",
		"photo‮gpj.exe":         "photo_gpj.exe",
		"line\nbreak":           "line_break",
		"CON":                   "_CON",
		"com1.txt":              "_com1.txt",
		"console.txt":           "console.txt",
		"run.lnk":               "run.lnk.download",
		"dots...":               "dots..._",
		"":                      "_",
		"Отчёт за октябрь.docx": "Отчёт за октябрь.docx",
		string(bytes.Repeat([]byte("я"), 150)) + ".zip": string(bytes.Repeat([]byte("я"), 98)) + ".zip",
	} {
		if got := SafeName(in); got != want {
			t.Errorf("%q: %q, want %q", in, got, want)
		}
	}
}

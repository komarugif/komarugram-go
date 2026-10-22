// SPDX-License-Identifier: Unlicense OR MIT

package savefile

import (
	"bytes"
	"testing"
	"time"

	"komarugram/internal/messenger/model"
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

// A sticker is named after its document, not the name every sticker
// carries; the others after what the sender named them.
func TestName(t *testing.T) {
	date := time.Date(2026, 10, 22, 12, 0, 0, 0, time.Local)
	for _, c := range []struct {
		m    model.Message
		want string
	}{
		{model.Message{Kind: model.MessageSticker, Date: date, Media: &model.MessageMedia{ID: "document/5170233102089322756", FileName: "sticker.webp", MIMEType: "image/webp"}}, "sticker_5170233102089322756.webp"},
		{model.Message{Kind: model.MessageSticker, Date: date, Media: &model.MessageMedia{ID: "document/42", MIMEType: "application/x-tgsticker"}}, "sticker_42.tgs"},
		{model.Message{Kind: model.MessageFile, Date: date, Media: &model.MessageMedia{ID: "document/7", FileName: "report.pdf"}}, "report.pdf"},
		{model.Message{Kind: model.MessageVideo, Date: date, Media: &model.MessageMedia{ID: "document/8", MIMEType: "video/mp4"}}, "video_2026-10-22_12-00-00.mp4"},
	} {
		if got := Name(c.m); got != c.want {
			t.Errorf("%q, want %q", got, c.want)
		}
	}
}

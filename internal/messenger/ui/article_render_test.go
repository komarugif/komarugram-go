// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
	"unicode/utf16"

	"gioui.org/layout"

	"komarugram/internal/messenger/formula"
	"komarugram/internal/messenger/localization"
	"komarugram/internal/messenger/mockstore"
	"komarugram/internal/messenger/model"
)

// articleFixture is an article with every kind of block, as the demo bot's
// "All Types" sends.
func articleFixture() model.RichPage {
	text := func(s string) model.RichText { return model.RichText{Text: s} }
	styled := func(parts ...any) model.RichText {
		var out model.RichText
		for i := 0; i < len(parts); i++ {
			s := parts[i].(string)
			var kinds []string
			for i+1 < len(parts) {
				k, ok := parts[i+1].([]string)
				if !ok {
					break
				}
				kinds, i = k, i+1
			}
			t := text(s)
			for _, kind := range kinds {
				t.Entities = append(t.Entities, model.Entity{Kind: kind, Length: len(utf16.Encode([]rune(s)))})
			}
			out.Append(t)
		}
		return out
	}
	five := 5
	inline := styled("Формула ", "E = mc^2", []string{"math"}, ", индекс x", "i", []string{"sub"}, ", степень 2", "10", []string{"sup"}, ", ", "маркер", []string{"marked"}, " и ", "спойлер", []string{"spoiler"}, ".")
	photo := func(id string, w, h int) model.RichMedia {
		return model.RichMedia{Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: id, MIMEType: "image/png", Width: w, Height: h}}
	}
	var blocks []model.RichBlock
	for level := 1; level <= 6; level++ {
		blocks = append(blocks, model.RichBlock{Kind: model.RichHeading, Level: level, Text: text(fmt.Sprintf("Заголовок %d", level))})
	}
	blocks = append(blocks,
		model.RichBlock{Kind: model.RichAuthorDate, Text: text("Автор статьи"), Date: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)},
		model.RichBlock{Kind: model.RichParagraph, Text: inline},
		model.RichBlock{Kind: model.RichThinking, Text: text("Размышление модели, курсивом.")},
		model.RichBlock{Kind: model.RichList, Ordered: true, Start: &five, Items: []model.RichListItem{
			{Text: text("Пятый пункт с длинным текстом, который переносится на вторую строку в узком пузыре.")},
			{Blocks: []model.RichBlock{
				{Kind: model.RichParagraph, Text: text("Шестой, с вложенным списком:")},
				{Kind: model.RichList, Items: []model.RichListItem{{Text: text("вложенный")}, {Checkbox: true, Checked: true, Text: text("готово")}}},
			}},
		}},
		model.RichBlock{Kind: model.RichList, Ordered: true, Type: "i", Items: []model.RichListItem{{Text: text("римский")}, {Text: text("второй")}}},
		model.RichBlock{Kind: model.RichQuote, Pullquote: true, Text: text("Выносная цитата посередине."), Caption: text("Кто-то известный")},
		model.RichBlock{Kind: model.RichQuote, Blocks: []model.RichBlock{
			{Kind: model.RichParagraph, Text: text("Цитата из блоков: абзац")},
			{Kind: model.RichList, Items: []model.RichListItem{{Text: text("и список в ней")}}},
		}, Caption: text("Автор")},
		model.RichBlock{Kind: model.RichMath, Formula: `v = \frac{s}{t},\quad \text{Скорость} = \int_0^\infty e^{-x^2}\,dx`},
		model.RichBlock{Kind: model.RichMath, Formula: `\sum_{i=1}^{n} i = 1 + 2 + 3 + 4 + 5 + 6 + 7 + 8 + 9 + 10 + 11 + 12 + 13 + 14 + 15 + \cdots + n = \frac{n(n+1)}{2}`},
		model.RichBlock{Kind: model.RichMath, Formula: `\frac{1}{`},
		model.RichBlock{Kind: model.RichDivider},
		model.RichBlock{Kind: model.RichTable, Text: text("Таблица с объединёнными ячейками"), Bordered: true, Striped: true, Rows: []model.RichTableRow{
			{Cells: []model.RichTableCell{{Header: true, Text: text("Блок")}, {Header: true, Text: text("Этап")}, {Header: true, Text: text("Готово"), Align: "center"}}},
			{Cells: []model.RichTableCell{{Text: text("Формулы"), Rowspan: 2, VAlign: "middle"}, {Text: text("4")}, {Text: text("нет"), Align: "center"}}},
			{Cells: []model.RichTableCell{{Text: text("RaTeX")}, {Text: text("нет"), Align: "center"}}},
			{Cells: []model.RichTableCell{{Text: text("Статьи: весь движок целиком, с таблицами"), Colspan: 2}, {Text: text("да"), Align: "right"}}},
		}},
		model.RichBlock{Kind: model.RichTable, Text: text("Широкая таблица прокручивается вбок"), Bordered: true, Rows: []model.RichTableRow{
			{Cells: []model.RichTableCell{{Header: true, Text: text("Язык")}, {Header: true, Text: text("Типизация")}, {Header: true, Text: text("Сборка мусора")}, {Header: true, Text: text("Параллелизм")}, {Header: true, Text: text("Первый выпуск")}}},
			{Cells: []model.RichTableCell{{Text: text("Go")}, {Text: text("статическая")}, {Text: text("есть")}, {Text: text("горутины")}, {Text: text("2009")}}},
		}},
		model.RichBlock{Kind: model.RichDetails, Open: true, Text: text("Открытые подробности"), Blocks: []model.RichBlock{{Kind: model.RichParagraph, Text: text("Текст внутри подробностей.")}}},
		model.RichBlock{Kind: model.RichDetails, Text: text("Закрытые подробности"), Blocks: []model.RichBlock{{Kind: model.RichParagraph, Text: text("Не видно.")}}},
		model.RichBlock{Kind: model.RichMediaBlock, Media: []model.RichMedia{photo("demo/photo", 640, 360), photo("demo/photo2", 360, 640), photo("demo/photo3", 640, 480)}, Caption: text("Коллаж из трёх фото")},
		model.RichBlock{Kind: model.RichMediaBlock, Slideshow: true, Media: []model.RichMedia{photo("demo/photo", 640, 360), photo("demo/photo2", 640, 360)}, Caption: text("Слайдшоу")},
		model.RichBlock{Kind: model.RichMediaBlock, Media: []model.RichMedia{{Kind: model.MessageVideo, Media: &model.MessageMedia{ID: "demo/video", MIMEType: "video/mp4", Width: 640, Height: 360, Duration: 5 * time.Second}}}, Caption: text("Видео")},
		model.RichBlock{Kind: model.RichMediaBlock, Media: []model.RichMedia{{Kind: model.MessageMusic, Media: &model.MessageMedia{ID: "demo/song", Title: "Песня", Performer: "Исполнитель", Size: 4 << 20}}}},
		model.RichBlock{Kind: model.RichButtons, Buttons: []model.RichButton{
			{Text: text("Открыть"), Button: model.MessageButton{Kind: "url", URL: "https://telegram.org"}, Style: "primary"},
			{Text: text("Удалить"), Button: model.MessageButton{Kind: "callback"}, Style: "danger"},
		}},
		model.RichBlock{Kind: model.RichButtons, Align: "center", Buttons: []model.RichButton{{Text: text("По центру"), Button: model.MessageButton{Kind: "copy", Copy: "x"}}}},
		model.RichBlock{Kind: model.RichEmbedPost, Author: "Канал", Date: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Blocks: []model.RichBlock{{Kind: model.RichParagraph, Text: text("Встроенный пост.")}}},
		model.RichBlock{Kind: model.RichEmbed, URL: "https://example.com/embed", Caption: text("Встраивание")},
		model.RichBlock{Kind: model.RichChannel, Title: "Новости Go", Username: "golang_news"},
		model.RichBlock{Kind: model.RichMap, Latitude: 55.7558, Longitude: 37.6173},
		model.RichBlock{Kind: model.RichRelated, Text: text("Похожие статьи"), Related: []model.RichArticle{{URL: "https://example.com/a", Title: "Первая статья", Description: "О чём она", Author: "Автор"}}},
		model.RichBlock{Kind: model.RichUnsupported},
		model.RichBlock{Kind: model.RichFooter, Text: text("Сноска внизу статьи")},
	)
	return model.RichPage{Blocks: blocks}
}

// articleStore draws an article's photos as gradients, as wide and high as
// the photos say.
type articleStore struct{ benchmarkHistory }

func (articleStore) Media(_ context.Context, m model.Message) ([]byte, error) {
	w, h := 320, 180
	if m.Media != nil && m.Media.Width > 0 && m.Media.Height > 0 {
		w, h = m.Media.Width/2, m.Media.Height/2
	}
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{R: uint8(40 + 180*x/w), G: uint8(90 + 120*y/h), B: 200, A: 255})
		}
	}
	var out bytes.Buffer
	err := png.Encode(&out, img)
	return out.Bytes(), err
}

// TestRenderArticle draws rich messages as articles in their bubbles: the
// demo's, whole and as the part Telegram sends, and one with every kind of
// block, narrow and wide, in both themes: ARTICLE_PNG_DIR=/tmp/article.
func TestRenderArticle(t *testing.T) {
	dir := os.Getenv("ARTICLE_PNG_DIR")
	if dir == "" {
		t.Skip("set ARTICLE_PNG_DIR to a directory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	pages := map[string]model.RichPage{"demo": mockstore.RichExample(false), "part": mockstore.RichExample(true), "all": articleFixture()}
	for name, page := range pages {
		for _, dark := range []bool{false, true} {
			for _, width := range []int{360, 640} {
				p := newChatPage(articleStore{}, func() {})
				p.images = &imageOps{}
				p.rows = map[model.MessageID]*messageRow{}
				// A part has its button only where the whole can be shown.
				p.openArticle = func(model.Message, string) {}
				summary := page.Summary()
				m := model.Message{Key: model.MessageKey{MessageID: 1}, Text: summary.Text, Entities: summary.Entities, Rich: &page, Date: time.Date(2026, 10, 6, 12, 30, 0, 0, time.UTC), ContentRevision: 1}
				height := 1400
				if name == "all" {
					height = 4200
				}
				path := filepath.Join(dir, fmt.Sprintf("article-%s-%d-dark-%t.png", name, width, dark))
				draw := func(gtx layout.Context) {
					p.images.BeginFrame()
					layout.UniformInset(12).Layout(gtx, func(gtx layout.Context) layout.Dimensions {
						gtx.Constraints.Min = image.Point{}
						return p.row(gtx, m, false, 0, localization.For("ru"), false)
					})
					p.images.EndFrame()
				}
				// The first frame asks for the formulas, which are laid out
				// off it; the picture is of the frame after.
				renderToast(t, path, image.Pt(width, height), dark, draw)
				waitFormulas(t)
				renderToast(t, path, image.Pt(width, height), dark, draw)
				p.Close()
			}
		}
	}
}

// waitFormulas waits for the formulas asked for to be laid out.
func waitFormulas(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for formula.Pending() > 0 {
		if time.Now().After(deadline) {
			t.Fatal("the formulas were not laid out")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

import (
	"context"
	"errors"
	"strings"
	"unicode/utf16"

	"komarugram/internal/messenger/model"
)

// TextBlocksExample is shared by the live demo and the render checks.
func TextBlocksExample() (string, []model.Entity) {
	code := "func greet() {\n    fmt.Println(\"Привет, мир! 👋\")\n}"
	quote := "Цитата сохраняет форматирование.\nСсылка: Telegram и скрытый спойлер.\nТретья строка цитаты.\nЧетвёртая строка видна после раскрытия."
	text := "Код и цитаты в сообщении 👋\n" + code + "\n" + quote + "\nТекст после блоков можно выделить вместе с ними."
	entity := func(kind, part string) model.Entity {
		i := strings.Index(text, part)
		return model.Entity{Kind: kind, Offset: len(utf16.Encode([]rune(text[:i]))), Length: len(utf16.Encode([]rune(part)))}
	}
	pre, q := entity("pre", code), entity("quote", quote)
	pre.Language, q.Collapsed = "go", true
	link := entity("url", "Telegram")
	link.URL = "https://telegram.org"
	return text, []model.Entity{pre, q, entity("bold", "форматирование"), link, entity("spoiler", "спойлер")}
}

// RichExample is a rich message of the demo: an article with most kinds of
// blocks. part cuts it short, as Telegram sends a long one; RichMessage
// gives it whole.
func RichExample(part bool) model.RichPage {
	text := func(s string) model.RichText { return model.RichText{Text: s} }
	styled := func(s string, kinds ...string) model.RichText {
		t := text(s)
		for _, kind := range kinds {
			t.Entities = append(t.Entities, model.Entity{Kind: kind, Length: len(utf16.Encode([]rune(s)))})
		}
		return t
	}
	var intro model.RichText
	intro.Append(text("Статья в сообщении: "))
	intro.Append(styled("жирный", "bold"))
	intro.Append(text(", "))
	intro.Append(styled("отмеченный", "marked"))
	intro.Append(text(", H"))
	intro.Append(styled("2", "sub"))
	intro.Append(text("O и ссылка на "))
	link := styled("Telegram", "url")
	link.Entities[0].URL = "https://telegram.org"
	intro.Append(link)
	intro.Append(text("."))
	// Links to an anchor in the part, and to one only the whole article
	// has.
	var jump model.RichText
	jump.Append(text("Перейти: "))
	toTable := styled("к таблице", "url")
	toTable.Entities[0].URL = "#table"
	jump.Append(toTable)
	jump.Append(text(" · "))
	toEnd := styled("к окончанию", "url")
	toEnd.Entities[0].URL = "#end"
	jump.Append(toEnd)
	page := model.RichPage{Part: part, Blocks: []model.RichBlock{
		{Kind: model.RichHeading, Level: 1, Text: text("Rich-сообщение")},
		{Kind: model.RichParagraph, Text: intro},
		{Kind: model.RichParagraph, Text: jump},
		{Kind: model.RichList, Items: []model.RichListItem{
			{Text: text("Маркированный пункт")},
			{Checkbox: true, Checked: true, Text: text("Сделанная задача")},
			{Checkbox: true, Text: text("Задача на потом")},
		}},
		{Kind: model.RichList, Ordered: true, Items: []model.RichListItem{{Text: text("Первый шаг")}, {Text: text("Второй шаг")}}},
		{Kind: model.RichQuote, Collapsed: true, Text: text("Цитата статьи, которая сворачивается до трёх строк."), Caption: text("Автор цитаты")},
		{Kind: model.RichCode, Language: "go", Text: text("fmt.Println(\"Привет\")")},
		{Kind: model.RichMath, Formula: `\frac{a}{b} = \sqrt{x^2 + y^2}`},
		{Kind: model.RichMediaBlock, Media: []model.RichMedia{{Kind: model.MessagePhoto, Media: &model.MessageMedia{ID: "demo/photo", MIMEType: "image/png", Width: 640, Height: 360}}}, Caption: text("Фото в статье")},
		{Kind: model.RichTable, Anchor: "table", Text: text("Таблица"), Bordered: true, Rows: []model.RichTableRow{
			{Cells: []model.RichTableCell{{Header: true, Text: text("Блок")}, {Header: true, Text: text("Этап")}}},
			{Cells: []model.RichTableCell{{Text: text("Формулы")}, {Text: text("4")}}},
		}},
	}}
	if part {
		return page
	}
	header := func(s string) model.RichTableCell { return model.RichTableCell{Header: true, Text: text(s)} }
	cell := func(s string) model.RichTableCell { return model.RichTableCell{Text: text(s)} }
	return model.RichPage{Blocks: append(page.Blocks,
		// Wider than the article: it scrolls sideways.
		model.RichBlock{Kind: model.RichTable, Text: text("Широкая таблица"), Bordered: true, Striped: true, Rows: []model.RichTableRow{
			{Cells: []model.RichTableCell{header("Язык"), header("Типизация"), header("Сборка мусора"), header("Параллелизм"), header("Первый выпуск")}},
			{Cells: []model.RichTableCell{cell("Go"), cell("статическая"), cell("есть"), cell("горутины и каналы"), cell("2009")}},
			{Cells: []model.RichTableCell{cell("Rust"), cell("статическая"), cell("нет, владение"), cell("потоки и async"), cell("2015")}},
		}},
		model.RichBlock{Kind: model.RichDivider},
		model.RichBlock{Kind: model.RichDetails, Text: text("Подробнее"), Blocks: []model.RichBlock{{Kind: model.RichParagraph, Anchor: "end", Text: text("Окончание статьи, которое пришло только целиком.")}}},
		model.RichBlock{Kind: model.RichFooter, Text: text("Конец статьи")},
	)}
}

// RichMessage implements model.RichStore: the demo's rich message whole.
func (s *Store) RichMessage(_ context.Context, key model.MessageKey) (model.RichPage, error) {
	for _, m := range s.History(key.ChatID).Messages {
		if m.Key == key && m.Rich != nil {
			return RichExample(false), nil
		}
	}
	return model.RichPage{}, errors.New("no rich message")
}

// InstantView implements model.InstantViewStore: every page's view is the
// demo's article.
func (s *Store) InstantView(ctx context.Context, url string) (model.RichPage, error) {
	if err := ctx.Err(); err != nil {
		return model.RichPage{}, err
	}
	return RichExample(false), nil
}

// SPDX-License-Identifier: Unlicense OR MIT

package mockstore

// demoMarkdown is the demo's Markdown file, which opens in the client:
// what Telegram's dialect of Markdown has.
const demoMarkdown = `# Пример Markdown

Файл открывается в клиенте, как статья: **жирный**, *курсив*, ~~зачёркнутый~~,
` + "`код`" + `, ==отмеченный==, ||спойлер|| и [ссылка](https://telegram.org).
Формула в строке: $E = mc^2$, а сноска — вот здесь.[^1]

## Списки

- [x] Сделано
- [ ] Не сделано
  1. Вложенный пункт
  2. Ещё один

## Таблица

| Язык | Типизация | Год |
|:-----|:---------:|----:|
| Go   | статическая | 2009 |
| Rust | статическая | 2015 |

## Код и формулы

` + "```go\nfunc main() {\n\tfmt.Println(\"Привет\")\n}\n```" + `

$$
\int_0^\infty e^{-x^2}\,dx = \frac{\sqrt{\pi}}{2}
$$

> Цитата с текстом
> на двух строках.

<details>
<summary>Подробнее</summary>

Скрытый текст, который открывается.

</details>

[К началу](#пример-markdown)

[^1]: Текст сноски.
`

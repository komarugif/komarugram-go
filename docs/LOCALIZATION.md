# Localization

How the client words its interface, and how to add a language.

## Where texts are

`internal/messenger/localization`:

| File | What |
|---|---|
| `languages/ru.strings`, `languages/en.strings` | The client's texts, a file a language, in the format of Telegram's language packs: `"key" = "text";` a line, `//` comments. `\n`, `\t`, `\"`, `\\` are escapes. Keys are sorted, a blank line between their prefixes. |
| `telegram_keys.strings` | The client's keys whose text Telegram Desktop has: `"history.photo" = "lng_in_dlg_photo";`. Only keys whose placeholders the client fills as tdesktop does. |
| `strings.go` | The table of languages the client comes with (`bundled`) and the parser. |
| `plural.go` | CLDR's plural rules for whole numbers, by language. |
| `pack.go` | Telegram's language pack as kept in memory (`Pack`). |

A plural text adds the CLDR form to its key: `key#one`, `key#few`,
`key#many`, `key#other` (also `#zero`, `#two`). The key alone is the text
when no form fits. Placeholders are `{name}`, filled by `l.Format` and
`l.Count` (`{count}`); some older texts are `fmt` verbs (`%s`, `%d`).
A text marks its bold parts with `**`, as Telegram Desktop's do; only a
text drawn by `richLabel` (`markedRuns`) may have them, which a
Telegram key mapped to it brings too.

Every language has `language.name` (its name in itself, for the settings)
and `number.decimal`; `language.rtl` = `"true"` marks one written right to
left.

## How a text is found

`localization.For(code)` is a catalog, cheap to make for every frame and
comparable: a cache of texts can keep the catalog it was made with and
compare. `l.T(key)` looks, in order:

1. in Telegram's pack for the language, when one is installed and the key
   is in `telegram_keys.strings`; then in the pack of its base language
   (a translation's);
2. in the client's file of the language, then of its main language (`pt`
   for `pt-br`), then English, then Russian, which is complete;
3. the key itself.

A plural form is looked for language by language: English's own text of a
key before Russian's form of it.

Files are parsed when a language is first asked for, not at start: a Russian
interface never parses English unless Russian lacks a text. A text without
escapes is a slice of the file in the binary, not a copy on the heap
(about 120 KiB of heap for Russian and the key table).

## Telegram's language packs

`internal/messenger/langpack` keeps them: one for all accounts, whole in
`<config>/komarugram-go/language/<code>.json`, and in memory only the texts
`telegram_keys.strings` names (about one in fifteen). A pack is asked for
whole once (`langpack.getLangPack`, `getLanguage` for its plural rule, base
language and direction), then only what changed since its version
(`getDifference`): at start, a day later, and when Telegram says it changed
(`updateLangPack`, `updateLangPackTooLong`). The windows ask for the pack of
their language with `RequestLanguagePack`, every frame; it is cheap.

## Data, not words

Stores and other packages below the interface hand data, which the interface
words in its language:

- a chat's or topic's last message is a `model.Preview` (text, a media key
  such as `preview.photo`, a sticker's emoji, the sender or `Outgoing`, a
  service message), worded by `l.Preview`;
- Saved Messages and a deleted account have no title: `l.ChatTitle`;
- sign-in errors are errors (`login.State.Problem`), worded by the sign-in
  screen;
- reusable views take a `Strings` struct, filled from the catalog
  (`localization/viewtexts`); `appwindow` words its own failure dialogs in
  the language `appwindow.SetLanguage` sets.

What is still worded below the interface: a deleted account's name inside
messages and reactions the store keeps, in the language of the moment.

## Adding a language

1. Copy `languages/en.strings` to `languages/<code>.strings`, with Telegram's
   code, and translate it; keys left out fall back to English.
2. Add it to `bundled` in `strings.go`, with its `//go:embed` line: the
   settings offer it in that order.
3. If CLDR's plural rule for it is not in `plural.go`, add it there.
4. `go test ./internal/messenger/localization`: the files must parse, and
   every key must have a Russian text.

Telegram's pack for the language is loaded by itself; the texts it has take
the place of the file's.

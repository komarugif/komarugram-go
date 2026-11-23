# Environment variables

These variables replace a setting for one run, or name a file the application would otherwise download or look for itself.

| Variable | What it does |
|---|---|
| `KOMARUGRAM_PROXY` | A proxy link, used over the ones kept in the settings: `tg://proxy?server=…&port=…&secret=…` (MTProto), `socks5://user:pass@host:port` or `socks5://host:port` (SOCKS5), `wss://web.telegram.org` (Telegram over WebSocket): the default of the `-proxy` flag. |
| `KOMARUGRAM_FONT`, `KOMARUGRAM_FONT_EXTRA`, `KOMARUGRAM_FONT_MONO`, `KOMARUGRAM_FONT_EMOJI` | A font file for the text, for the characters it lacks, for code and for emoji, over the fonts of the settings. |
| `KOMARUGRAM_EMOJI_SET` | The directory of an emoji pack to draw emoji with, over the one chosen in the settings. |
| `KOMARUGRAM_EMOJI_PACKS` | A catalog of emoji packs, a directory or an http(s) URL, for the settings to offer in place of the one built in. |
| `KOMARUGRAM_MASCOT` | A folder or a URL with `index.json` that the mascot's scenes in the About section are taken from, in place of [clawd-animations](https://github.com/komarugif/clawd-animations). |
| `KOMARUGRAM_AVCDEC` | Your own build of the H.264 and MPEG-4 Part 2 decoders, in place of the one downloaded from [libavcodec-wasm](https://github.com/komarugif/libavcodec-wasm): the path to an `avcdec.wasm` file, or its URL. |
| `KOMARUGRAM_AACDEC` | Your own build of the AAC decoder, in place of the one downloaded from [fdk-aac-wasm](https://github.com/komarugif/fdk-aac-wasm): the path to an `aacdec.wasm` file, or its URL. Without an AAC decoder only M4A sound does not play. |
| `KOMARUGRAM_MINIAPP_BROWSER` | The browser Mini Apps, and the video player where it plays in a browser, run in: a program on `PATH`, the path to one, or the application id of a flatpak. It wins over the browser picked in the settings. |
| `KOMARUGRAM_TRAY_LIB`, `KOMARUGRAM_VOICE_LIB` | Haiku only: the path to `libtrayhaiku` and to `libvoicehaiku`, which are otherwise looked for beside the program and in `lib` beside it. |

The tests that need a real account or the machine's TPM are off unless asked for:

| Variable | What it does |
|---|---|
| `KOMARUGRAM_TEST_LIVE_HISTORY=1` | Checks the history of the account on this machine, only reading it. |
| `KOMARUGRAM_TEST_MEDIA_CHANNEL` | The title of the chat that check takes its media from. |
| `KOMARUGRAM_TEST_LIVE_EMOJI_SETS` | A directory the emoji packs of the catalog are installed into from Telegram, the way the settings do it, reading only. |
| `KOMARUGRAM_TEST_TPM=1` | Uses the machine's TPM. |

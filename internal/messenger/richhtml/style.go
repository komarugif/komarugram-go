// SPDX-License-Identifier: Unlicense OR MIT

package richhtml

// style is the page's own style: a column of text in the reader's fonts,
// light or dark as the reader's system is. Telegram Desktop's is GPLv3, and
// none of it is here.
const style = `:root {
  color-scheme: light dark;
  --text: #1c1d21;
  --dim: #6b7079;
  --link: #2a6dd6;
  --plate: #f1f2f5;
  --line: #d9dce2;
  --accent: #3d8bd9;
  --mark: rgba(255, 213, 79, .45);
  --code: #4e7391;
  --page: #ffffff;
}
@media (prefers-color-scheme: dark) {
  :root {
    --text: #e6e7ea;
    --dim: #9aa0a9;
    --link: #78aef5;
    --plate: #23262c;
    --line: #3a3e46;
    --accent: #5aa2ea;
    --mark: rgba(255, 213, 79, .3);
    --code: #8fb8d8;
    --page: #17181c;
  }
}
* { box-sizing: border-box; }
html { background: var(--page); color: var(--text); }
body {
  margin: 0;
  font: 17px/1.55 system-ui, -apple-system, "Segoe UI", Roboto, "Noto Sans", sans-serif;
  overflow-wrap: break-word;
}
article { max-width: 720px; margin: 0 auto; padding: 32px 20px 64px; }
a { color: var(--link); text-decoration: none; }
a:hover { text-decoration: underline; }
h1, h2, h3, h4, h5, h6 { line-height: 1.25; margin: 1.4em 0 .5em; }
h1 { font-size: 1.9em; }
h2 { font-size: 1.5em; }
h3 { font-size: 1.25em; }
h4, h5, h6 { font-size: 1.05em; }
article > :first-child { margin-top: 0; }
p { margin: .6em 0; }
.footer, .byline, figcaption, cite, small { color: var(--dim); font-size: .88em; }
cite { display: block; font-style: normal; margin-top: .4em; }
code, pre {
  font-family: ui-monospace, "SF Mono", "Cascadia Mono", "Noto Sans Mono", "Liberation Mono", monospace;
  font-size: .9em;
}
code { color: var(--code); }
pre.code {
  position: relative;
  background: var(--plate);
  border-radius: 8px;
  padding: 12px 14px;
  overflow-x: auto;
  tab-size: 4;
  white-space: pre;
}
pre.code code { color: inherit; }
pre.code code[data-language]::before {
  content: attr(data-language);
  display: block;
  color: var(--dim);
  font-size: .82em;
  margin-bottom: 6px;
}
mark { background: var(--mark); color: inherit; border-radius: 2px; }
blockquote {
  margin: .8em 0;
  padding: 2px 0 2px 14px;
  border-left: 3px solid var(--accent);
}
[dir="rtl"] blockquote { border-left: 0; border-right: 3px solid var(--accent); padding: 2px 14px 2px 0; }
blockquote.pullquote { border: 0; text-align: center; font-style: italic; font-size: 1.1em; }
blockquote.thinking { color: var(--dim); font-style: italic; border-left-color: var(--line); }
blockquote.post { border-left-color: var(--line); }
.post-header { margin-bottom: .3em; }
hr { border: 0; border-top: 1px solid var(--line); margin: 1.5em 0; }
ul, ol { padding-inline-start: 1.6em; margin: .6em 0; }
li { margin: .2em 0; }
li.task { list-style: none; margin-inline-start: -1.3em; }
figure { margin: 1em 0; }
figure.media img, figure.media video { display: block; max-width: 100%; height: auto; border-radius: 8px; }
figure.media audio { width: 100%; }
.collage .items { display: grid; grid-template-columns: repeat(auto-fit, minmax(160px, 1fr)); gap: 4px; }
.collage .items img, .collage .items video { width: 100%; height: 100%; object-fit: cover; border-radius: 4px; }
.slideshow .items { display: flex; overflow-x: auto; scroll-snap-type: x mandatory; gap: 8px; }
.slideshow .items > * { flex: 0 0 100%; scroll-snap-align: center; }
.slideshow .items img, .slideshow .items video { width: 100%; }
figcaption { margin-top: .4em; }
.missing {
  background: var(--plate);
  color: var(--dim);
  border-radius: 8px;
  padding: 24px;
  text-align: center;
}
a.file::before { content: "📎 "; }
.audio { background: var(--plate); border-radius: 8px; padding: 8px 12px; }
figure.table { overflow-x: auto; }
table { border-collapse: collapse; min-width: 50%; }
th, td { padding: 6px 10px; text-align: start; vertical-align: top; }
th { font-weight: 600; }
table.bordered th, table.bordered td { border: 1px solid var(--line); }
table.striped tr:nth-child(even) { background: var(--plate); }
table.compact th, table.compact td { padding: 2px 6px; }
figure.table figcaption { font-weight: 600; color: var(--text); margin: 0 0 .4em; }
details { margin: .8em 0; border-top: 1px solid var(--line); border-bottom: 1px solid var(--line); padding: 6px 0; }
summary { cursor: pointer; font-weight: 600; }
.math { overflow-x: auto; overflow-y: hidden; }
div.math { text-align: center; margin: 1em 0; font-size: 1.2em; }
.buttons { display: flex; flex-wrap: wrap; gap: 6px; margin: .8em 0; }
.buttons.left { justify-content: flex-start; }
.buttons.center { justify-content: center; }
.buttons.right { justify-content: flex-end; }
.buttons > .button { flex: 1 1 0; }
.buttons.left > .button, .buttons.center > .button, .buttons.right > .button { flex: 0 0 auto; }
.button {
  display: inline-block;
  text-align: center;
  padding: 6px 14px;
  border-radius: 8px;
  background: var(--plate);
  color: var(--link);
}
.button.primary { background: var(--accent); color: #fff; }
.button.danger { color: #d64545; }
.button.success { color: #2f9e55; }
.button.link { background: none; }
.related .card {
  display: block;
  padding: 10px 0;
  border-bottom: 1px solid var(--line);
  color: inherit;
}
.related .card b, .related .card span, .related .card small { display: block; }
.related .card span { color: var(--dim); }
.channel a { font-weight: 600; }
.spoiler {
  background: var(--dim);
  color: transparent;
  border-radius: 3px;
  cursor: pointer;
  transition: color .2s, background .2s;
}
.spoiler * { color: transparent; }
.spoiler:focus, .spoiler:focus * { background: none; color: inherit; outline: none; }
.spoiler-media { display: block; overflow: hidden; border-radius: 8px; cursor: pointer; }
.spoiler-media > * { filter: blur(28px); transition: filter .3s; }
.spoiler-media:focus { outline: none; }
.spoiler-media:focus > * { filter: none; }
@media print {
  .spoiler, .spoiler * { color: inherit; background: none; }
  .spoiler-media > * { filter: none; }
}
`

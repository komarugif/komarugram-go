// SPDX-License-Identifier: Unlicense OR MIT

// Package codehighlight colors the code blocks of messages, as Telegram
// Desktop does: Prism.js's grammars tokenize them (pkg/prism) on a goroutine
// of their own, and the eight classes of colors Telegram Desktop maps the
// tokens to are cached for the last blocks asked for.
package codehighlight

import (
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"komarugram/pkg/prism"
)

// Class is the color class of a token, as Telegram Desktop groups Prism's
// token types; zero is plain text.
type Class uint8

const (
	Plain       Class = iota
	Comment           // comment, block-comment, prolog, doctype, cdata
	Punctuation       // punctuation
	Constant          // property, tag, boolean, number, constant, symbol, deleted
	String            // selector, attr-name, string, char, builtin
	Operator          // operator, entity, url
	Keyword           // atrule, attr-value, keyword, function
	ClassName         // class-name
	Inserted          // inserted
)

var classes = map[string]Class{
	"comment": Comment, "block-comment": Comment, "prolog": Comment, "doctype": Comment, "cdata": Comment,
	"punctuation": Punctuation,
	"property":    Constant, "tag": Constant, "boolean": Constant, "number": Constant, "constant": Constant, "symbol": Constant, "deleted": Constant,
	"selector": String, "attr-name": String, "string": String, "char": String, "builtin": String,
	"operator": Operator, "entity": Operator, "url": Operator,
	"atrule": Keyword, "attr-value": Keyword, "keyword": Keyword, "function": Keyword,
	"class-name": ClassName,
	"inserted":   Inserted,
}

// classOf is the class of a token. Telegram Desktop looks at its type only;
// a type it has no color for takes its alias's here, so that, say, a
// doc-comment (an alias of comment) is colored as a comment.
func classOf(typ, alias string) Class {
	if c, ok := classes[typ]; ok {
		return c
	}
	return classes[alias]
}

// aliases are the names people write after ``` that Prism's grammars do
// not know, by the ones they do: Telegram Desktop's two (diff and patch to
// git) and ours.
var aliases = map[string]string{
	"diff": "git", "patch": "git",
	"c++": "cpp", "hpp": "cpp", "h": "c", "c#": "csharp", "golang": "go", "rs": "rust",
	"zsh": "bash", "ps1": "powershell", "1c": "bsl", "asm": "nasm", "proto": "protobuf",
	"vue": "markup", "svelte": "markup", "delphi": "pascal", "pl": "perl", "fs": "fsharp",
	"ex": "elixir", "erl": "erlang", "clj": "clojure", "ml": "ocaml", "gql": "graphql",
	"make": "makefile", "jsonc": "json", "console": "shell-session",
}

// Span is bytes Start to End of a block's text, in a color class.
type Span struct {
	Start, End int
	Class      Class
}

const (
	// cacheSize is how many blocks' colors are kept, as in Telegram Desktop.
	cacheSize = 256
	// matchTimeout and blockDeadline bound the time a block takes: Prism's
	// grammars backtrack badly on some text, which comes from strangers.
	matchTimeout  = 50 * time.Millisecond
	blockDeadline = 250 * time.Millisecond
	// idle is how long the grammars stay loaded after the last block.
	idle = 2 * time.Minute
)

// Key identifies a block: its language and text.
type Key uint64

// KeyOf is the key of text in language.
func KeyOf(language, text string) Key {
	h := fnv.New64a()
	h.Write([]byte(Language(language)))
	h.Write([]byte{0})
	h.Write([]byte(text))
	return Key(h.Sum64())
}

// Language is the grammar's name for what a block says its language is.
func Language(language string) string {
	language = strings.ToLower(strings.TrimSpace(language))
	if a, ok := aliases[language]; ok {
		return a
	}
	return language
}

type request struct {
	key            Key
	language, text string
}

// highlighter is the one the process has: what it colored, and the queue of
// what it is asked to color.
var highlighter struct {
	mu      sync.Mutex
	cache   map[Key][]Span
	order   []Key
	pending map[Key][]func()
	queue   []request
	running bool
	// wake tells the worker that the queue has more.
	wake chan struct{}
	// load is how the grammars are loaded, and idle how long the worker
	// waits; tests replace them.
	load func() (*prism.Grammars, error)
	idle time.Duration
}

// Lookup returns the colors of the block of key, if they are ready. A block
// whose language no grammar knows, or that could not be colored, has
// colors with no span.
func Lookup(key Key) ([]Span, bool) {
	h := &highlighter
	h.mu.Lock()
	defer h.mu.Unlock()
	spans, ok := h.cache[key]
	return spans, ok
}

// Request asks for the colors of text in language, whose key is key. done
// runs, on another goroutine, once Lookup has them; at once when it does.
func Request(key Key, language, text string, done func()) {
	h := &highlighter
	h.mu.Lock()
	if _, ok := h.cache[key]; ok {
		h.mu.Unlock()
		done()
		return
	}
	if h.pending == nil {
		h.pending = map[Key][]func(){}
	}
	waiting, queued := h.pending[key]
	h.pending[key] = append(waiting, done)
	if h.wake == nil {
		h.wake = make(chan struct{}, 1)
	}
	if !queued {
		h.queue = append(h.queue, request{key, Language(language), text})
		select {
		case h.wake <- struct{}{}:
		default:
		}
	}
	start := !h.running
	h.running = true
	h.mu.Unlock()
	if start {
		go work()
	}
}

// work colors what is queued, and lets the grammars go when nothing has
// been for a while.
func work() {
	h := &highlighter
	var grammars *prism.Grammars
	var failed bool
	timer := time.NewTimer(idle)
	defer timer.Stop()
	for {
		h.mu.Lock()
		if len(h.queue) == 0 {
			wake, wait := h.wake, h.idle
			h.mu.Unlock()
			if wait == 0 {
				wait = idle
			}
			timer.Reset(wait)
			select {
			case <-wake:
			case <-timer.C:
				h.mu.Lock()
				if len(h.queue) == 0 {
					// The grammars go with this goroutine.
					h.running = false
					h.mu.Unlock()
					return
				}
				h.mu.Unlock()
			}
			continue
		}
		r := h.queue[0]
		h.queue = h.queue[1:]
		h.mu.Unlock()
		if grammars == nil && !failed {
			load := h.load
			if load == nil {
				load = func() (*prism.Grammars, error) { return prism.Embedded(matchTimeout) }
			}
			var err error
			grammars, err = load()
			failed = err != nil
		}
		var spans []Span
		if grammars != nil {
			spans = colors(grammars, r.text, r.language)
		}
		h.mu.Lock()
		if h.cache == nil {
			h.cache = map[Key][]Span{}
		}
		if _, ok := h.cache[r.key]; !ok {
			h.order = append(h.order, r.key)
		}
		h.cache[r.key] = spans
		for len(h.order) > cacheSize {
			delete(h.cache, h.order[0])
			h.order = h.order[1:]
		}
		done := h.pending[r.key]
		delete(h.pending, r.key)
		h.mu.Unlock()
		for _, f := range done {
			f()
		}
	}
}

// colors is text's spans of a class: what a deadline cut short keeps the
// colors found, and the rest plain.
func colors(g *prism.Grammars, text, language string) []Span {
	tokens, _ := g.Tokenize(text, language, time.Now().Add(blockDeadline))
	var out []Span
	for _, t := range tokens {
		c := classOf(t.Type, t.Alias)
		if c == Plain {
			continue
		}
		if n := len(out); n > 0 && out[n-1].End == t.Start && out[n-1].Class == c {
			out[n-1].End = t.End
			continue
		}
		out = append(out, Span{Start: t.Start, End: t.End, Class: c})
	}
	return out
}

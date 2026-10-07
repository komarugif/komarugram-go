// SPDX-License-Identifier: Unlicense OR MIT

package prism

import (
	"errors"
	"time"
	"unicode/utf8"
)

// Span is a piece of the text: bytes Start to End, inside a token of Type,
// with Alias, the innermost token around them; Type is empty outside
// tokens. A tokenization's spans cover the text, in order.
type Span struct {
	Start, End  int
	Type, Alias string
}

var (
	// ErrUnknownLanguage is the error of a language the grammars do not
	// know.
	ErrUnknownLanguage = errors.New("prism: unknown language")
	// ErrStopped is the error of a tokenization cut short: a match took too
	// long, or the deadline passed. The spans found so far are returned,
	// as libprisma keeps the tokens it has when a match fails.
	ErrStopped = errors.New("prism: stopped")
)

// maxDepth bounds how deep grammars tokenize inside one another, as in
// libprisma: some include themselves.
const maxDepth = 32

// Tokenize splits text in language into spans. When deadline is not zero,
// no match begins after it.
func (g *Grammars) Tokenize(text, language string, deadline time.Time) ([]Span, error) {
	l, ok := g.languages[language]
	if !ok {
		return nil, ErrUnknownLanguage
	}
	t := tokenizer{g: g, deadline: deadline}
	runes := []rune(text)
	list := t.tokenize(runes, &g.grammars[l.grammar])
	// Runes into bytes, counting an invalid byte as the one rune it reads as.
	offsets := make([]int, 0, len(runes)+1)
	for i := range text {
		offsets = append(offsets, i)
	}
	offsets = append(offsets, len(text))
	var spans []Span
	pos := 0
	var walk func(list *tokenList, typ, alias string)
	walk = func(list *tokenList, typ, alias string) {
		for n := list.head.next; n != list.head; n = n.next {
			if n.syntax {
				walk(n.children, n.typ, n.alias)
				continue
			}
			end := pos + len(n.text)
			if end > pos {
				if k := len(spans) - 1; k >= 0 && spans[k].Type == typ && spans[k].Alias == alias {
					spans[k].End = offsets[end]
				} else {
					spans = append(spans, Span{Start: offsets[pos], End: offsets[end], Type: typ, Alias: alias})
				}
			}
			pos = end
		}
	}
	walk(list, "", "")
	if t.stopped {
		return spans, ErrStopped
	}
	return spans, nil
}

// tokenizer is one tokenization: libprisma's SyntaxHighlighter.
type tokenizer struct {
	g        *Grammars
	deadline time.Time
	depth    int
	stopped  bool
}

type rematch struct {
	token string
	reach int
	j     int
}

// tokenize tokenizes text with grammar. Where a match fails, it keeps what
// it found, as libprisma catches what Boost throws.
func (t *tokenizer) tokenize(text []rune, gr *grammar) *tokenList {
	list := newTokenList(text)
	if t.depth >= maxDepth {
		return list
	}
	t.depth++
	defer func() { t.depth-- }()
	if err := t.matchGrammar(text, byteLen(text), list, gr, list.head, 0, nil); err != nil {
		t.stopped = true
	}
	return list
}

// match finds p in text from pos, as libprisma's Pattern::match does: in
// what follows pos only, as if it began there. It returns where the match
// is and how long, without what a lookbehind group matched.
func (t *tokenizer) match(p *pattern, text []rune, pos int) (int, int, bool, error) {
	if !t.deadline.IsZero() && time.Now().After(t.deadline) {
		return 0, 0, false, ErrStopped
	}
	re, err := t.g.regexp(p)
	if err != nil {
		return 0, 0, false, err
	}
	m, err := re.FindRunesMatch(text[pos:])
	if err != nil || m == nil {
		return 0, 0, false, err
	}
	start, length := pos+m.Index, m.Length
	if p.lookbehind {
		if g := m.GroupByNumber(1); g != nil && len(g.Captures) > 0 {
			start += g.Length
			length -= g.Length
		}
	}
	return start, length, true, nil
}

// matchGrammar is libprisma's: textBytes is how long text is in bytes.
func (t *tokenizer) matchGrammar(text []rune, textBytes int, list *tokenList, gr *grammar, startNode *node, startPos int, re *rematch) error {
	for _, token := range gr.tokens {
		for x, id := range token.patterns {
			if re != nil && re.j == x && re.token == token.name {
				return nil
			}
			p := &t.g.patterns[id]
			pos := startPos
			for current := startNode.next; current != list.head; pos, current = pos+current.length(), current.next {
				if re != nil && pos >= re.reach {
					break
				}
				if list.length > textBytes {
					// Something went terribly wrong, as libprisma says.
					return nil
				}
				if current.syntax {
					continue
				}
				str := current.text
				removeCount := 1
				var from, length int
				if p.greedy {
					start, n, ok, err := t.match(p, text, pos)
					if err != nil {
						return err
					}
					if !ok || start >= len(text) {
						break
					}
					to := start + n
					q := pos + current.length()
					for start >= q {
						current = current.next
						if current == list.head {
							// The tokens do not add up to the text.
							return nil
						}
						q += current.length()
					}
					q -= current.length()
					pos = q
					// A match that begins inside another token is invalid.
					if current.syntax {
						continue
					}
					for k := current; k != list.head && (q < to || !k.syntax); k = k.next {
						removeCount++
						q += k.length()
					}
					removeCount--
					str = text[pos:q]
					from, length = start-pos, n
				} else {
					start, n, ok, err := t.match(p, str, 0)
					if err != nil {
						return err
					}
					if !ok {
						continue
					}
					from, length = start, n
				}
				match := str[from : from+length]
				before, after := str[:from], str[from+length:]
				reach := pos + len(str)
				if re != nil && reach > re.reach {
					re.reach = reach
				}
				removeFrom := current.prev
				if len(before) > 0 {
					removeFrom = list.addText(removeFrom, before)
					pos += len(before)
				}
				list.removeRange(removeFrom, removeCount)
				var children *tokenList
				if p.inside >= 0 {
					children = t.tokenize(match, &t.g.grammars[p.inside])
				} else {
					children = newTokenList(match)
				}
				current = list.addSyntax(removeFrom, token.name, children, p.alias, len(match))
				if len(after) > 0 {
					list.addText(current, after)
				}
				if removeCount > 1 {
					// A greedy match took tokens away: match again what it
					// reached.
					nested := rematch{token: token.name, reach: reach, j: x}
					if err := t.matchGrammar(text, textBytes, list, gr, current.prev, pos, &nested); err != nil {
						return err
					}
					if re != nil && nested.reach > re.reach {
						re.reach = nested.reach
					}
				}
			}
		}
	}
	return nil
}

// tokenList is libprisma's TokenList: a ring of text and tokens around
// head, which holds nothing.
type tokenList struct {
	head   *node
	length int
}

type node struct {
	prev, next *node
	syntax     bool
	text       []rune
	// A token's type, alias, length in runes and what is inside it.
	typ, alias string
	n          int
	children   *tokenList
}

func (n *node) length() int {
	if n.syntax {
		return n.n
	}
	return len(n.text)
}

func newTokenList(text []rune) *tokenList {
	head := &node{syntax: true}
	n := &node{prev: head, next: head, text: text}
	head.next, head.prev = n, n
	return &tokenList{head: head, length: 1}
}

func (l *tokenList) insert(after, n *node) *node {
	n.prev, n.next = after, after.next
	after.next.prev = n
	after.next = n
	l.length++
	return n
}

func (l *tokenList) addText(after *node, text []rune) *node {
	return l.insert(after, &node{text: text})
}

func (l *tokenList) addSyntax(after *node, typ string, children *tokenList, alias string, n int) *node {
	return l.insert(after, &node{syntax: true, typ: typ, alias: alias, n: n, children: children})
}

func (l *tokenList) removeRange(after *node, count int) {
	for i := 0; i < count && after.next != l.head; i++ {
		after.next = after.next.next
		after.next.prev = after
		l.length--
	}
}

// byteLen is how long text is in UTF-8, which libprisma counts in.
func byteLen(text []rune) int {
	n := 0
	for _, r := range text {
		n += utf8.RuneLen(r)
	}
	return n
}

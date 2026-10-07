// SPDX-License-Identifier: Unlicense OR MIT

package cmark

// Kind is a node's kind.
type Kind uint8

// Node kinds, as cmshim.c numbers them.
const (
	Unknown Kind = iota
	Document
	BlockQuote
	List
	Item
	CodeBlock
	HTMLBlock
	Paragraph
	Heading
	ThematicBreak
	FootnoteDefinition
	Text
	SoftBreak
	LineBreak
	Code
	HTMLInline
	Emphasis
	Strong
	Link
	Image
	FootnoteReference
	Strikethrough
	Table
	TableRow
	TableCell
)

// Delimiter is what follows an ordered list's numbers.
type Delimiter uint8

const (
	NoDelimiter Delimiter = iota
	Period
	Paren
)

// Task is a list item's checkbox, if it has one.
type Task uint8

const (
	NoTask Task = iota
	Open
	Done
)

// Alignment is a table column's: 'l', 'c', 'r', or 0 for none.
type Alignment byte

// Node is a node of a document's tree. Lines and columns are 1-based and
// count bytes, 0 when cmark does not know them; the end is the last byte.
// Which other fields mean something depends on Kind.
type Node struct {
	Kind                   Kind
	StartLine, StartColumn int
	EndLine, EndColumn     int
	Children               []*Node
	// Literal is the text of a text, a code span, a code block or HTML,
	// and the label of a footnote definition or reference.
	Literal string
	// Info is a code block's info string: its language first.
	Info string
	// URL and Title are a link's or an image's.
	URL, Title string
	// Level is a heading's.
	Level int
	// Ordered, Delimiter, Start and Tight are a list's.
	Ordered   bool
	Delimiter Delimiter
	Start     int
	Tight     bool
	// Task is a list item's.
	Task Task
	// Alignments are a table's columns'.
	Alignments []Alignment
	// Header is set for a table's header row.
	Header bool
	// Type is cmark's name of a node of an unknown kind.
	Type string
}

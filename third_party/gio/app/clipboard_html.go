// SPDX-License-Identifier: Unlicense OR MIT

package app

import (
	"bytes"
	"fmt"
)

// cfHTML is an HTML document as the Windows clipboard's "HTML Format" holds
// it: a header of the byte offsets of the document and of the fragment
// copied, which is all of the document's body, marked with the comments
// the format asks for, and a terminating NUL.
func cfHTML(doc []byte) []byte {
	start, end := 0, len(doc)
	lower := bytes.ToLower(doc)
	if i := bytes.Index(lower, []byte("<body")); i >= 0 {
		if j := bytes.IndexByte(doc[i:], '>'); j >= 0 {
			start = i + j + 1
		}
	}
	if i := bytes.LastIndex(lower, []byte("</body")); i >= start {
		end = i
	}
	const (
		open  = "<!--StartFragment-->"
		close = "<!--EndFragment-->"
		// The header is of a fixed length: each offset ten digits.
		format = "Version:0.9\r\nStartHTML:%010d\r\nEndHTML:%010d\r\nStartFragment:%010d\r\nEndFragment:%010d\r\n"
	)
	header := len(fmt.Sprintf(format, 0, 0, 0, 0))
	fragment := header + start + len(open)
	fragmentEnd := fragment + end - start
	total := header + len(doc) + len(open) + len(close)
	var b bytes.Buffer
	b.Grow(total + 1)
	fmt.Fprintf(&b, format, header, total, fragment, fragmentEnd)
	b.Write(doc[:start])
	b.WriteString(open)
	b.Write(doc[start:end])
	b.WriteString(close)
	b.Write(doc[end:])
	b.WriteByte(0)
	return b.Bytes()
}

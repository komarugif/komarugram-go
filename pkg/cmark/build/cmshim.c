// SPDX-License-Identifier: Unlicense OR MIT

// cmshim parses Markdown with cmark-gfm, as Telegram Desktop does
// (iv/markdown/iv_markdown_parse_convert.cpp: the table, strikethrough,
// autolink, tagfilter and tasklist extensions; footnotes, source positions,
// strikethrough by double tildes), and writes the tree for the host to read.
//
// The tree is written in pre-order, little-endian. Each node is
//
//	u8  kind (enum kind below)
//	u32 start line, start column, end line, end column (1-based; 0 unknown)
//	u32 count of children
//
// and then, by kind:
//
//	list:       u8 ordered, u8 delimiter (0 none, 1 period, 2 paren),
//	            u32 start, u8 tight
//	item:       u8 task (0 none, 1 open, 2 done)
//	heading:    u8 level
//	code block: str info, str literal
//	text, code, html block, html inline: str literal
//	footnote definition, footnote reference: str label (a reference's
//	                                        definition's)
//	link, image: str url, str title
//	table:      u16 columns, then that many u8 alignments ('l', 'c', 'r'
//	            or 0)
//	table row:  u8 header
//	unknown:    str the node's type
//
// where str is a u32 length and as many bytes of UTF-8.

#include <stdint.h>
#include <stdlib.h>
#include <string.h>

#include "cmark-gfm.h"
#include "cmark-gfm-core-extensions.h"
#include "strikethrough.h"
#include "table.h"

enum kind {
	K_UNKNOWN,
	K_DOCUMENT,
	K_BLOCK_QUOTE,
	K_LIST,
	K_ITEM,
	K_CODE_BLOCK,
	K_HTML_BLOCK,
	K_PARAGRAPH,
	K_HEADING,
	K_THEMATIC_BREAK,
	K_FOOTNOTE_DEFINITION,
	K_TEXT,
	K_SOFTBREAK,
	K_LINEBREAK,
	K_CODE,
	K_HTML_INLINE,
	K_EMPH,
	K_STRONG,
	K_LINK,
	K_IMAGE,
	K_FOOTNOTE_REFERENCE,
	K_STRIKETHROUGH,
	K_TABLE,
	K_TABLE_ROW,
	K_TABLE_CELL,
};

// Results of md_parse.
enum { OK, FAILED, TOO_LARGE };

static uint8_t *out;
static uint32_t out_len, out_cap;

static int grow(uint32_t n) {
	if (out_len + n <= out_cap) {
		return 1;
	}
	uint32_t cap = out_cap ? out_cap : 1 << 16;
	while (cap < out_len + n) {
		cap *= 2;
	}
	uint8_t *p = realloc(out, cap);
	if (!p) {
		return 0;
	}
	out = p;
	out_cap = cap;
	return 1;
}

static void put8(uint8_t v) {
	if (grow(1)) {
		out[out_len++] = v;
	}
}

static void put16(uint16_t v) {
	put8(v & 0xff);
	put8(v >> 8);
}

static void put32(uint32_t v) {
	put16(v & 0xffff);
	put16(v >> 16);
}

static void putstr(const char *s) {
	uint32_t n = s ? strlen(s) : 0;
	put32(n);
	if (n && grow(n)) {
		memcpy(out + out_len, s, n);
		out_len += n;
	}
}

static enum kind kind_of(cmark_node *node) {
	switch (cmark_node_get_type(node)) {
	case CMARK_NODE_DOCUMENT: return K_DOCUMENT;
	case CMARK_NODE_BLOCK_QUOTE: return K_BLOCK_QUOTE;
	case CMARK_NODE_LIST: return K_LIST;
	case CMARK_NODE_ITEM: return K_ITEM;
	case CMARK_NODE_CODE_BLOCK: return K_CODE_BLOCK;
	case CMARK_NODE_HTML_BLOCK: return K_HTML_BLOCK;
	case CMARK_NODE_PARAGRAPH: return K_PARAGRAPH;
	case CMARK_NODE_HEADING: return K_HEADING;
	case CMARK_NODE_THEMATIC_BREAK: return K_THEMATIC_BREAK;
	case CMARK_NODE_FOOTNOTE_DEFINITION: return K_FOOTNOTE_DEFINITION;
	case CMARK_NODE_TEXT: return K_TEXT;
	case CMARK_NODE_SOFTBREAK: return K_SOFTBREAK;
	case CMARK_NODE_LINEBREAK: return K_LINEBREAK;
	case CMARK_NODE_CODE: return K_CODE;
	case CMARK_NODE_HTML_INLINE: return K_HTML_INLINE;
	case CMARK_NODE_EMPH: return K_EMPH;
	case CMARK_NODE_STRONG: return K_STRONG;
	case CMARK_NODE_LINK: return K_LINK;
	case CMARK_NODE_IMAGE: return K_IMAGE;
	case CMARK_NODE_FOOTNOTE_REFERENCE: return K_FOOTNOTE_REFERENCE;
	default: break;
	}
	cmark_node_type type = cmark_node_get_type(node);
	if (type == CMARK_NODE_STRIKETHROUGH) return K_STRIKETHROUGH;
	if (type == CMARK_NODE_TABLE) return K_TABLE;
	if (type == CMARK_NODE_TABLE_ROW) return K_TABLE_ROW;
	if (type == CMARK_NODE_TABLE_CELL) return K_TABLE_CELL;
	return K_UNKNOWN;
}

static void put_node(cmark_node *node) {
	enum kind k = kind_of(node);
	put8(k);
	put32(cmark_node_get_start_line(node));
	put32(cmark_node_get_start_column(node));
	put32(cmark_node_get_end_line(node));
	put32(cmark_node_get_end_column(node));
	uint32_t children = 0;
	for (cmark_node *c = cmark_node_first_child(node); c; c = cmark_node_next(c)) {
		children++;
	}
	put32(children);
	switch (k) {
	case K_LIST:
		put8(cmark_node_get_list_type(node) == CMARK_ORDERED_LIST);
		switch (cmark_node_get_list_delim(node)) {
		case CMARK_PERIOD_DELIM: put8(1); break;
		case CMARK_PAREN_DELIM: put8(2); break;
		default: put8(0); break;
		}
		put32(cmark_node_get_list_start(node));
		put8(cmark_node_get_list_tight(node) != 0);
		break;
	case K_ITEM: {
		const char *type = cmark_node_get_type_string(node);
		if (type && !strcmp(type, "tasklist")) {
			put8(cmark_gfm_extensions_get_tasklist_item_checked(node) ? 2 : 1);
		} else {
			put8(0);
		}
		break;
	}
	case K_HEADING:
		put8(cmark_node_get_heading_level(node));
		break;
	case K_CODE_BLOCK:
		putstr(cmark_node_get_fence_info(node));
		putstr(cmark_node_get_literal(node));
		break;
	case K_TEXT:
	case K_CODE:
	case K_HTML_BLOCK:
	case K_HTML_INLINE:
	case K_FOOTNOTE_DEFINITION:
		putstr(cmark_node_get_literal(node));
		break;
	case K_FOOTNOTE_REFERENCE: {
		// A reference's own literal is its number; the label is its
		// definition's.
		cmark_node *def = cmark_node_parent_footnote_def(node);
		putstr(cmark_node_get_literal(def ? def : node));
		break;
	}
	case K_LINK:
	case K_IMAGE:
		putstr(cmark_node_get_url(node));
		putstr(cmark_node_get_title(node));
		break;
	case K_TABLE: {
		uint16_t columns = cmark_gfm_extensions_get_table_columns(node);
		uint8_t *align = cmark_gfm_extensions_get_table_alignments(node);
		put16(columns);
		for (uint16_t i = 0; i < columns; i++) {
			put8(align ? align[i] : 0);
		}
		break;
	}
	case K_TABLE_ROW:
		put8(cmark_gfm_extensions_get_table_row_is_header(node) != 0);
		break;
	case K_UNKNOWN:
		putstr(cmark_node_get_type_string(node));
		break;
	default:
		break;
	}
}

__attribute__((export_name("md_alloc"))) void *md_alloc(uint32_t n) {
	return malloc(n ? n : 1);
}

__attribute__((export_name("md_free"))) void md_free(void *p) {
	free(p);
}

__attribute__((export_name("md_out"))) uint8_t *md_out(void) {
	return out;
}

__attribute__((export_name("md_out_len"))) uint32_t md_out_len(void) {
	return out_len;
}

// md_parse parses len bytes of Markdown at src, and writes the tree unless
// it has more than max_nodes nodes or nests deeper than max_depth.
__attribute__((export_name("md_parse"))) int md_parse(const char *src, uint32_t len, uint32_t max_nodes, uint32_t max_depth) {
	static int registered;
	if (!registered) {
		cmark_gfm_core_extensions_ensure_registered();
		registered = 1;
	}
	out_len = 0;
	int options = CMARK_OPT_DEFAULT | CMARK_OPT_SOURCEPOS | CMARK_OPT_FOOTNOTES | CMARK_OPT_STRIKETHROUGH_DOUBLE_TILDE;
	cmark_parser *parser = cmark_parser_new(options);
	if (!parser) {
		return FAILED;
	}
	static const char *extensions[] = {"table", "strikethrough", "autolink", "tagfilter", "tasklist"};
	for (size_t i = 0; i < sizeof extensions / sizeof *extensions; i++) {
		cmark_syntax_extension *ext = cmark_find_syntax_extension(extensions[i]);
		if (!ext || !cmark_parser_attach_syntax_extension(parser, ext)) {
			cmark_parser_free(parser);
			return FAILED;
		}
	}
	cmark_parser_feed(parser, src, len);
	cmark_node *root = cmark_parser_finish(parser);
	cmark_parser_free(parser);
	if (!root) {
		return FAILED;
	}
	int result = OK;
	uint32_t nodes = 0;
	cmark_iter *iter = cmark_iter_new(root);
	cmark_event_type ev;
	while ((ev = cmark_iter_next(iter)) != CMARK_EVENT_DONE) {
		if (ev != CMARK_EVENT_ENTER) {
			continue;
		}
		cmark_node *node = cmark_iter_get_node(iter);
		uint32_t depth = 0;
		for (cmark_node *p = cmark_node_parent(node); p && depth <= max_depth; p = cmark_node_parent(p)) {
			depth++;
		}
		if (++nodes > max_nodes || depth > max_depth) {
			result = TOO_LARGE;
			break;
		}
		put_node(node);
	}
	cmark_iter_free(iter);
	cmark_node_free(root);
	if (result == OK && out_len == 0) {
		result = FAILED;
	}
	return result;
}

/* config.h for building cmark-gfm with clang for wasm32-wasip1, in place of
   the one CMake generates from src/config.h.in. */
#ifndef CMARK_CONFIG_H
#define CMARK_CONFIG_H
#include <stdbool.h>
#define HAVE_STDBOOL_H
#define HAVE___BUILTIN_EXPECT
#define HAVE___ATTRIBUTE__
#define CMARK_ATTRIBUTE(list) __attribute__ (list)
#ifndef CMARK_INLINE
#define CMARK_INLINE inline
#endif
#endif

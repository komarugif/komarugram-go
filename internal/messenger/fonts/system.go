// SPDX-License-Identifier: Unlicense OR MIT

package fonts

import (
	"os"
	"sync"

	"github.com/go-text/typesetting/fontscan"
)

// emojiProbe is the emoji a font of emoji has, whatever its version: 😀.
const emojiProbe = 0x1F600

// SystemHasEmoji reports whether a font of the system has emoji, so that
// they are drawn with no pack or font of the user's: Haiku has none, nor a
// Linux without an emoji font installed. It reads the index of the
// system's fonts that Gio's shaper makes, in the cache directory, once.
// When the fonts cannot be read it reports true: there is nothing to tell.
var SystemHasEmoji = sync.OnceValue(func() bool {
	dir, err := os.UserCacheDir()
	if err != nil {
		return true
	}
	fonts, err := fontscan.SystemFonts(quiet{}, dir)
	if err != nil {
		return true
	}
	return hasEmoji(fonts)
})

func hasEmoji(fonts []fontscan.Footprint) bool {
	for _, f := range fonts {
		if f.Runes.Contains(emojiProbe) {
			return true
		}
	}
	return false
}

// quiet drops fontscan's log: the shaper logs the same scan.
type quiet struct{}

func (quiet) Printf(string, ...any) {}

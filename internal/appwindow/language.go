// SPDX-License-Identifier: Unlicense OR MIT

package appwindow

import (
	"sync/atomic"

	"komarugram/internal/messenger/localization"
)

var language atomic.Value // string

// SetLanguage sets the language of what the host shows by itself when a
// window fails: Russian until it is set.
func SetLanguage(code string) { language.Store(code) }

func texts() localization.Catalog {
	code, _ := language.Load().(string)
	return localization.For(code)
}

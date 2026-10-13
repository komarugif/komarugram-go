// SPDX-License-Identifier: Unlicense OR MIT

package tgstore

import (
	"sync/atomic"

	"komarugram/internal/crash"
	"komarugram/internal/messenger/langpack"
)

// RequestLanguagePack has Telegram's pack for language installed, from
// disk first, then up to date with Telegram over this account's
// connection; one account's does for all. The store's windows redraw when
// a pack changes. It is cheap enough for every frame.
func (s *Store) RequestLanguagePack(language string) {
	shownLanguage.Store(language)
	c := s.history
	c.mu.Lock()
	closing, api, ctx := c.closing, c.api, c.ctx
	c.mu.Unlock()
	if closing {
		return
	}
	langpack.Watch(s, s.changed)
	if !langpack.Due(language, api != nil) {
		return
	}
	var a langpack.API // Not a nil *tg.Client in it: Load tells nil by it.
	if api != nil {
		a = api
	}
	c.wg.Go(func() {
		defer crash.Recover("language pack", func(*crash.Panic) {})
		langpack.Load(ctx, language, a)
	})
}

// shownLanguage is the language the interface asked for last.
var shownLanguage atomic.Value // string

// interfaceLanguage is the language of the interface: what little the
// store words itself, a deleted account's name in what it keeps, is in it.
func interfaceLanguage() string {
	language, _ := shownLanguage.Load().(string)
	return language
}

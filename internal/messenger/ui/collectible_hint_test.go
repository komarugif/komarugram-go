// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"testing"

	"komarugram/internal/messenger/localization"
)

// Every language the client comes with has the hint's picture: a language
// added has it drawn by cmd/hint-images.
func TestCollectibleHintForEveryLanguage(t *testing.T) {
	for _, lang := range localization.Languages() {
		if collectibleHintMask(lang) == nil {
			t.Errorf("no hint picture for %q: run go run ./cmd/hint-images -font Caveat.ttf", lang)
		}
	}
}

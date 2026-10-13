// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"sync"

	"komarugram/internal/messenger/localization"
)

// languageCodes are the languages the settings offer, the ones the client
// comes with; languageNames, their names in themselves.
func languageCodes() []string {
	var codes []string
	for _, l := range localization.Languages() {
		codes = append(codes, string(l))
	}
	return codes
}

var languageNames = sync.OnceValue(func() map[string]string {
	names := map[string]string{}
	for _, l := range localization.Languages() {
		names[string(l)] = l.Name()
	}
	return names
})

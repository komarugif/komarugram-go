// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"

	"komarugram/internal/messenger/preferences"
)

// TestMain gives the themes of the tests the fonts the environment names
// (KOMARUGRAM_FONT and the others of internal/messenger/fonts), so that a
// render test shows a font without the settings. No test opens the
// browser: the links they click are only noted.
func TestMain(m *testing.M) {
	applyFonts(preferences.Fonts{}, nil)
	openBrowser = func(target string) error {
		browserOpened.Store(target)
		return nil
	}
	fetchSmall = func(context.Context, string) ([]byte, error) { return nil, errors.New("tests reach no server") }
	os.Exit(m.Run())
}

// browserOpened is the last link a test would have opened in the browser.
var browserOpened atomic.Value

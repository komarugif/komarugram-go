// SPDX-License-Identifier: Unlicense OR MIT

package ui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
	// Nothing a test saves lands in the downloads or the pictures of the
	// one who runs it.
	saved, err := os.MkdirTemp("", "komarugram-ui-saved-")
	if err != nil {
		panic(err)
	}
	downloadsDir = func() string { return filepath.Join(saved, "Downloads") }
	picturesDir = func() string { return filepath.Join(saved, "Pictures") }
	code := m.Run()
	os.RemoveAll(saved)
	os.Exit(code)
}

// browserOpened is the last link a test would have opened in the browser.
var browserOpened atomic.Value
